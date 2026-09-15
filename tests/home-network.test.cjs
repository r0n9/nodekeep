const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const network = require('../resource/static/network-chart.js')

function createPage() {
    let now = 100000
    let timerID = 0
    const timers = new Map()
    const sockets = []
    const snapshot = time => [{
        ID: 1, Tag: '', Host: { Platform: 'linux' },
        LastActive: new Date(time).toISOString(),
        State: { NetInSpeed: 2048, NetOutSpeed: 512 },
    }]
    class Clock extends Date {
        static now() { return now }
    }
    class Socket {
        constructor() {
            this.readyState = 1
            sockets.push(this)
        }
        // Simulate a server that never acknowledges the close handshake.
        close() { this.readyState = 2 }
        receive() { this.onmessage({ data: JSON.stringify(snapshot(now)) }) }
    }
    class Vue {
        constructor(options) {
            Object.assign(this, options.data)
            for (const [name, method] of Object.entries(options.methods)) {
                this[name] = method.bind(this)
            }
            if (options.computed) {
                for (const [name, getter] of Object.entries(options.computed)) {
                    Object.defineProperty(this, name, {
                        get: () => getter.call(this),
                        configurable: true,
                    })
                }
            }
            this.$set = (target, key, value) => { target[key] = value }
            this.$delete = (target, key) => { delete target[key] }
            options.created.call(this)
            options.mounted.call(this)
        }
    }
    const window = {
        location: { protocol: 'http:', host: 'localhost' },
        matchMedia: () => ({ matches: false }),
        setInterval: () => 0,
        clearInterval: () => {},
        setTimeout: (callback, delay) => {
            timers.set(++timerID, { callback, at: now + delay })
            return timerID
        },
        clearTimeout: id => timers.delete(id),
    }
    const context = vm.createContext({
        window, Date: Clock, Vue, WebSocket: Socket, NodekeepNetwork: network, console,
        document: { documentElement: { getAttribute: () => 'light' } },
        localStorage: { getItem: () => null, setItem: () => {} },
        MutationObserver: class { observe() {} disconnect() {} },
    })
    const source = fs.readFileSync(path.join(__dirname, '../resource/template/theme-default/home.html'), 'utf8')
    const script = [...source.matchAll(/<script>([\s\S]*?)<\/script>/g)].at(-1)[1]
        .replace('{{.Servers}}', JSON.stringify(snapshot(now)))
        .replace('{{.Billings}}', '{}')
    vm.runInContext(script, context)
    return {
        app: context.statusCards,
        sockets,
        advance(seconds) {
            now += seconds * 1000
            context.statusCards.tickNetwork()
            for (const [id, timer] of timers) {
                if (timer.at <= now) {
                    timers.delete(id)
                    timer.callback()
                }
            }
        },
    }
}

test('a stalled close handshake does not delay reconnect or let old events replace current data', () => {
    const page = createPage()
    const first = page.sockets[0]
    const lateClose = first.onclose
    const lateMessage = first.onmessage
    first.close()
    page.advance(7)
    assert.equal(page.app.networkDisconnected, true)
    assert.equal(page.app.networkRate(page.app.servers[0], 'NetInSpeed'), '—')
    page.advance(3)
    assert.equal(page.sockets.length, 2)
    page.sockets[1].receive()
    assert.equal(page.app.networkDisconnected, false)
    assert.equal(page.app.networkRate(page.app.servers[0], 'NetInSpeed'), '2 KB/s')
    assert.ok(page.app.networkHistory[1].netIn.includes(null))
    lateClose()
    lateMessage({ data: '[]' })
    assert.equal(page.app.networkDisconnected, false)
    assert.equal(page.app.servers.length, 1)
    assert.equal(page.sockets.length, 2)
})

test('changing views preserves history and removing a node releases its history', () => {
    const { app } = createPage()
    const history = app.networkHistory[1]
    app.setViewMode('list')
    app.setViewMode('card')
    assert.equal(app.networkHistory[1], history)
    app.refreshServers([])
    assert.equal(Object.keys(app.networkHistory).length, 0)
})

test('network speed tiers distinguish offline, low (<100KB), mid (>=100KB), and high (>=1MB)', () => {
    const { app } = createPage()
    const server = app.servers[0]

    // Live low tier (2048 B/s = 2 KB/s)
    assert.equal(app.netSpeedClass(server, 'NetInSpeed'), 'nk-speed-tier-low')

    // Live mid tier (>= 100 KB/s)
    server.State.NetInSpeed = 100 * 1024
    assert.equal(app.netSpeedClass(server, 'NetInSpeed'), 'nk-speed-tier-mid')
    server.State.NetInSpeed = 500 * 1024
    assert.equal(app.netSpeedClass(server, 'NetInSpeed'), 'nk-speed-tier-mid')

    // Live high tier (>= 1 MB/s)
    server.State.NetInSpeed = 1024 * 1024
    assert.equal(app.netSpeedClass(server, 'NetInSpeed'), 'nk-speed-tier-high')
    server.State.NetInSpeed = 5 * 1024 * 1024
    assert.equal(app.netSpeedClass(server, 'NetInSpeed'), 'nk-speed-tier-high')

    // Calling with (live, speed) signature
    assert.equal(app.netSpeedClass(true, 50 * 1024), 'nk-speed-tier-low')
    assert.equal(app.netSpeedClass(true, 100 * 1024), 'nk-speed-tier-mid')
    assert.equal(app.netSpeedClass(true, 1024 * 1024), 'nk-speed-tier-high')

    // Offline / disconnected
    assert.equal(app.netSpeedClass(false, 10 * 1024 * 1024), 'nk-speed-offline')
    server.live = false
    assert.equal(app.netSpeedClass(server, 'NetInSpeed'), 'nk-speed-offline')
})

test('hero status text and class reflect node health and disconnection', () => {
    const { app } = createPage()
    assert.equal(app.heroStatusText, '运行正常')
    assert.equal(app.heroStatusClass, 'status-online')
    assert.equal(app.onlineCount, 1)
    assert.equal(app.offlineCount, 0)
    assert.equal(app.pendingCount, 0)

    // Mark server offline
    app.servers[0].live = false
    assert.equal(app.heroStatusText, '1 台离线')
    assert.equal(app.heroStatusClass, 'status-danger')
    assert.equal(app.onlineCount, 0)
    assert.equal(app.offlineCount, 1)

    // Disconnected state takes highest priority
    app.networkDisconnected = true
    assert.equal(app.heroStatusText, '连接重试中')
    assert.equal(app.heroStatusClass, 'status-warning')
})


