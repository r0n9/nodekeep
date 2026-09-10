(function (root) {
    'use strict'

    const WINDOW_SECONDS = 60
    const GAP_SECONDS = 6
    const MAX_POINTS = 120

    function createHistory() {
        return { t: [], netIn: [], netOut: [], lastReport: 0, revision: 0 }
    }

    function speedValue(value) {
        return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
    }

    function formatSpeed(value) {
        if (speedValue(value) === null) {
            return '—'
        }
        const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB']
        const unit = value > 0 ? Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1) : 0
        const amount = value / Math.pow(1024, Math.max(0, unit))
        const digits = amount >= 100 || unit === 0 ? 0 : amount >= 10 ? 1 : 2
        return Number(amount.toFixed(digits)) + ' ' + units[Math.max(0, unit)] + '/s'
    }

    function isFresh(server, now) {
        const age = now - Date.parse(server.LastActive) / 1000
        return !!(server.reported && server.live && server.State && age >= -5 && age <= GAP_SECONDS)
    }

    function pushPoint(history, time, netIn, netOut) {
        history.t.push(time)
        history.netIn.push(netIn)
        history.netOut.push(netOut)
        history.revision++
    }

    function pruneHistory(history, now) {
        let count = Math.max(0, history.t.length - MAX_POINTS)
        while (count < history.t.length && history.t[count] < now - WINDOW_SECONDS) {
            count++
        }
        if (count > 0) {
            history.t.splice(0, count)
            history.netIn.splice(0, count)
            history.netOut.splice(0, count)
            history.revision++
        }
    }

    function breakHistory(history, now) {
        const last = history.t.length - 1
        if (last >= 0 && now > history.t[last] &&
            (history.netIn[last] !== null || history.netOut[last] !== null)) {
            pushPoint(history, now, null, null)
        }
        pruneHistory(history, now)
    }

    function recordSample(history, server, now) {
        pruneHistory(history, now)
        if (!isFresh(server, now)) {
            breakHistory(history, now)
            return
        }
        const reportTime = Date.parse(server.LastActive) / 1000
        // A WebSocket snapshot can repeat an unchanged agent report.
        if (reportTime <= history.lastReport) {
            return
        }
        const time = Math.min(reportTime, now)
        const last = history.t.length - 1
        if (last >= 0 && time <= history.t[last]) {
            return
        }
        if (last >= 0 && time - history.t[last] > GAP_SECONDS) {
            breakHistory(history, history.t[last] + 2)
        }
        history.lastReport = reportTime
        pushPoint(history, time, speedValue(server.State.NetInSpeed), speedValue(server.State.NetOutSpeed))
        pruneHistory(history, now)
    }

    const api = { WINDOW_SECONDS, GAP_SECONDS, createHistory, formatSpeed, isFresh, pruneHistory, breakHistory, recordSample }
    if (typeof module === 'object' && module.exports) {
        module.exports = api
    }
    if (!root || !root.Vue) {
        return
    }
    root.NodekeepNetwork = api

    root.Vue.component('network-chart', {
        props: {
            history: { type: Object, required: true },
            now: { type: Number, required: true },
            theme: { type: String, required: true },
        },
        data() {
            return {
                tooltip: { visible: false, time: '', netIn: '—', netOut: '—', left: 0 },
            }
        },
        template: [
            '<div class="nk-network-chart" tabindex="0" role="group"',
            ' aria-label="最近60秒网络速率趋势，按左右方向键查看采样"',
            ' :aria-describedby="tooltip.visible ? tooltipID : null"',
            ' @keydown="onKey" @focus="focusChart" @blur="hideTooltip" @mouseleave="hideTooltip"',
            ' @touchstart.passive="touchChart" @touchmove.passive="touchChart">',
            ' <div ref="canvas" class="nk-network-canvas" aria-hidden="true"></div>',
            ' <span v-if="!hasSamples" class="nk-network-chart-empty">等待速率数据</span>',
            ' <div class="nk-network-time" aria-hidden="true"><span>60秒前</span><span>30秒前</span><span>现在</span></div>',
            ' <div ref="tooltip" :id="tooltipID" class="nk-chart-tooltip nk-network-tooltip"',
            '  :class="{ visible: tooltip.visible }" :style="{ left: tooltip.left + \'px\' }" role="tooltip">',
            '  <div class="nk-chart-tooltip-time">{{ tooltip.time }}</div>',
            '  <div class="nk-chart-tooltip-row"><i class="inbound"></i><span>入站</span><strong>{{ tooltip.netIn }}</strong></div>',
            '  <div class="nk-chart-tooltip-row"><i class="outbound"></i><span>出站</span><strong>{{ tooltip.netOut }}</strong></div>',
            ' </div>',
            '</div>',
        ].join(''),
        computed: {
            hasSamples() {
                return this.history.netIn.some(value => value !== null) || this.history.netOut.some(value => value !== null)
            },
            tooltipID() {
                return 'network-tooltip-' + this._uid
            },
        },
        watch: {
            'history.revision': 'scheduleRender',
            now: 'scheduleRender',
            theme() {
                this.destroyChart()
                this.scheduleRender()
            },
        },
        mounted() {
            this.renderChart()
            if (root.ResizeObserver) {
                this._resizeObserver = new root.ResizeObserver(() => this.scheduleRender())
                this._resizeObserver.observe(this.$refs.canvas)
            } else {
                root.addEventListener('resize', this.scheduleRender)
            }
        },
        beforeDestroy() {
            root.cancelAnimationFrame(this._frame)
            if (this._resizeObserver) {
                this._resizeObserver.disconnect()
            }
            root.removeEventListener('resize', this.scheduleRender)
            this.destroyChart()
        },
        methods: {
            scheduleRender() {
                if (this._frame) {
                    return
                }
                this._frame = root.requestAnimationFrame(() => {
                    this._frame = null
                    this.renderChart()
                })
            },
            renderChart() {
                const el = this.$refs.canvas
                if (!el || !root.uPlot || !el.clientWidth) {
                    return
                }
                // Keep uPlot instances and their canvas state outside Vue's observer.
                const data = [this.history.t.slice(), this.history.netIn.slice(), this.history.netOut.slice()]
                const width = el.clientWidth
                if (this._chart) {
                    if (this._chart.width !== width) {
                        this._chart.setSize({ width: width, height: 80 })
                    }
                    this._chart.setData(data)
                    return
                }
                const css = root.getComputedStyle(el)
                const color = name => css.getPropertyValue(name).trim()
                this._chart = new root.uPlot({
                    width: width,
                    height: 80,
                    padding: [7, 3, 4, 0],
                    legend: { show: false },
                    cursor: {
                        y: false,
                        drag: { x: false, y: false, setScale: false },
                        points: { size: 5 },
                    },
                    scales: {
                        x: { time: true, range: () => [this.now - WINDOW_SECONDS, this.now] },
                        y: {
                            range: (u, min, max) => [0, Math.max(1024, Number(max) || 0) * 1.15],
                        },
                    },
                    axes: [
                        { show: false },
                        {
                            size: 60,
                            font: '11px "PingFang SC", "Microsoft YaHei", sans-serif',
                            stroke: color('--nk-muted'),
                            splits: (u, axis, min, max) => [0, max],
                            values: (u, values) => values.map(formatSpeed),
                            grid: { stroke: color('--nk-border'), width: 1 },
                            ticks: { show: false },
                        },
                    ],
                    series: [
                        {},
                        this.lineOptions('入站', color('--nk-net-in'), false),
                        this.lineOptions('出站', color('--nk-net-out'), true),
                    ],
                    hooks: {
                        setCursor: [chart => this.showTooltip(chart)],
                    },
                }, data, el)
            },
            lineOptions(label, color, dashed) {
                return {
                    label: label,
                    stroke: color,
                    width: 1.75,
                    dash: dashed ? [4, 3] : [],
                    spanGaps: false,
                    points: {
                        show: (chart, series) => chart.data[series].filter(value => value !== null).length === 1,
                        size: 4,
                    },
                }
            },
            showTooltip(chart) {
                const index = chart.cursor.idx
                if (!Number.isInteger(index) || chart.cursor.left < 0 || chart.cursor.top < 0 ||
                    !Number.isFinite(chart.data[0][index])) {
                    this.hideTooltip()
                    return
                }
                this._selectedIndex = index
                this.tooltip.time = new Date(chart.data[0][index] * 1000).toLocaleTimeString('zh-CN', { hour12: false })
                this.tooltip.netIn = formatSpeed(chart.data[1][index])
                this.tooltip.netOut = formatSpeed(chart.data[2][index])
                this.tooltip.visible = true
                this.$nextTick(() => {
                    if (!this.$refs.tooltip || !this.$refs.canvas) {
                        return
                    }
                    const left = chart.over.offsetLeft + chart.cursor.left + 10
                    const max = this.$refs.canvas.clientWidth - this.$refs.tooltip.offsetWidth - 4
                    this.tooltip.left = Math.max(4, Math.min(left, max))
                })
            },
            hideTooltip() {
                this.tooltip.visible = false
            },
            selectPoint(index) {
                if (!this._chart || !this._chart.data[0].length) {
                    return
                }
                const data = this._chart.data[0]
                index = Math.max(0, Math.min(index, data.length - 1))
                this._chart.setCursor({ left: this._chart.valToPos(data[index], 'x'), top: 20 })
            },
            focusChart() {
                this.selectPoint(this.history.t.length - 1)
            },
            onKey(event) {
                const last = this.history.t.length - 1
                const current = Number.isInteger(this._selectedIndex) ? this._selectedIndex : last
                const positions = { ArrowLeft: current - 1, ArrowRight: current + 1, Home: 0, End: last }
                if (Object.prototype.hasOwnProperty.call(positions, event.key)) {
                    event.preventDefault()
                    this.selectPoint(positions[event.key])
                } else if (event.key === 'Escape') {
                    this.hideTooltip()
                }
            },
            touchChart(event) {
                if (!this._chart || !event.touches.length) {
                    return
                }
                const bounds = this._chart.over.getBoundingClientRect()
                const left = Math.max(0, Math.min(event.touches[0].clientX - bounds.left, bounds.width))
                this._chart.setCursor({ left: left, top: 20 })
            },
            destroyChart() {
                if (this._chart) {
                    this._chart.destroy()
                    this._chart = null
                }
                this.hideTooltip()
            },
        },
    })
})(typeof window === 'undefined' ? null : window)
