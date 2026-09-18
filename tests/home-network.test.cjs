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
        receive() { this.onmessage && this.onmessage({ data: JSON.stringify(snapshot(now)) }) }
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
    const eventListeners = new Map()
    const addListener = (target, type, handler) => {
        const key = `${target}:${type}`
        if (!eventListeners.has(key)) eventListeners.set(key, [])
        eventListeners.get(key).push(handler)
    }
    const removeListener = (target, type, handler) => {
        const key = `${target}:${type}`
        const list = eventListeners.get(key)
        if (list) {
            const idx = list.indexOf(handler)
            if (idx !== -1) list.splice(idx, 1)
        }
    }
    const dispatch = (target, type, event = {}) => {
        const list = eventListeners.get(`${target}:${type}`) || []
        list.forEach(h => h(event))
    }
    const doc = {
        documentElement: { getAttribute: () => 'light' },
        hidden: false,
        visibilityState: 'visible',
        addEventListener: (type, handler) => addListener('document', type, handler),
        removeEventListener: (type, handler) => removeListener('document', type, handler),
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
        addEventListener: (type, handler) => addListener('window', type, handler),
        removeEventListener: (type, handler) => removeListener('window', type, handler),
    }
    const context = vm.createContext({
        window, Date: Clock, Vue, WebSocket: Socket, NodekeepNetwork: network, console,
        document: doc,
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
        advance(seconds, receive = false) {
            const step = receive ? 2 : seconds
            let remaining = seconds
            while (remaining > 0) {
                const dt = Math.min(step, remaining)
                now += dt * 1000
                if (receive) {
                    const currentSocket = sockets.at(-1)
                    if (currentSocket && currentSocket.readyState === 1) {
                        currentSocket.receive()
                    }
                }
                context.statusCards.tickNetwork()
                const fired = []
                for (const [id, timer] of timers) {
                    if (timer.at <= now) {
                        fired.push({ id, callback: timer.callback })
                    }
                }
                for (const { id, callback } of fired) {
                    timers.delete(id)
                    callback()
                }
                remaining -= dt
            }
        },
        setHidden(hidden) {
            doc.hidden = hidden
            doc.visibilityState = hidden ? 'hidden' : 'visible'
            dispatch('document', 'visibilitychange')
        },
        triggerActivity(event = 'mousemove') {
            dispatch('window', event)
        },
        triggerWake(event = 'click') {
            dispatch('document', event)
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

test('page enters dormancy after 15 minutes of inactivity and user action wakes it up', () => {
    const page = createPage()
    assert.equal(page.app.isDormant, false)
    assert.equal(page.sockets.length, 1)
    assert.equal(page.sockets[0].readyState, 1)

    // Advance 15 minutes (900 seconds) without any interaction
    page.advance(900)
    assert.equal(page.app.isDormant, true)
    assert.equal(page.app.heroStatusText, '已休眠')
    assert.equal(page.app.heroStatusClass, 'status-warning')
    assert.equal(page.app.networkStatus(page.app.servers[0]), '已休眠暂停')
    assert.equal(page.app.statusText(page.app.servers[0]), '已休眠')
    assert.equal(page.sockets[0].readyState, 2) // closed

    // While dormant, tickNetwork should not attempt reconnect
    page.advance(10)
    assert.equal(page.sockets.length, 1)

    // Wake up by user action (e.g. click)
    page.triggerWake('click')
    assert.equal(page.app.isDormant, false)
    assert.equal(page.sockets.length, 2) // reconnected
    page.sockets[1].receive()
    assert.equal(page.app.heroStatusText, '运行正常')
    assert.equal(page.app.statusText(page.app.servers[0]), '在线')
})

test('user activity resets idle timer and prevents dormancy', () => {
    const page = createPage()
    assert.equal(page.app.isDormant, false)

    // Advance 10 minutes with live server traffic
    page.advance(600, true)
    assert.equal(page.app.isDormant, false)

    // User moves mouse or types
    page.triggerActivity('mousemove')

    // Advance another 10 minutes (total 20 min from start, but only 10 min since last activity)
    page.advance(600, true)
    assert.equal(page.app.isDormant, false)
    assert.equal(page.sockets.length, 1)
    assert.equal(page.sockets[0].readyState, 1)

    // Now let full 15 minutes pass without activity
    page.advance(900, true)
    assert.equal(page.app.isDormant, true)
})

test('page hidden for 10 seconds pauses websocket, visible resumes it', () => {
    const page = createPage()
    assert.equal(page.app.isPageHidden, false)
    assert.equal(page.sockets.length, 1)

    // Switch tab away
    page.setHidden(true)
    // Less than 10s: should NOT pause yet (debounce)
    page.advance(5)
    assert.equal(page.app.isPageHidden, false)
    assert.equal(page.sockets[0].readyState, 1)

    // Exceed 10s: paused
    page.advance(6)
    assert.equal(page.app.isPageHidden, true)
    assert.equal(page.sockets[0].readyState, 2) // closed

    // Returning to visible resumes automatically
    page.setHidden(false)
    assert.equal(page.app.isPageHidden, false)
    assert.equal(page.sockets.length, 2) // reconnected
    page.sockets[1].receive()
    assert.equal(page.app.heroStatusText, '运行正常')
})

test('page hidden and returning within 10 seconds does not disconnect websocket', () => {
    const page = createPage()
    assert.equal(page.sockets.length, 1)

    page.setHidden(true)
    page.advance(4)
    page.setHidden(false)
    page.advance(10, true)

    // Still the same original socket, never closed
    assert.equal(page.sockets.length, 1)
    assert.equal(page.sockets[0].readyState, 1)
})

test('page hidden for more than 15 minutes enters dormancy and requires wake action', () => {
    const page = createPage()
    page.setHidden(true)
    // Advance 20 minutes (1200 seconds) while hidden
    page.advance(1200)

    // User switches back to tab
    page.setHidden(false)
    assert.equal(page.app.isDormant, true)
    assert.equal(page.app.heroStatusText, '已休眠')
    assert.equal(page.sockets.length, 1) // still paused, no reconnect yet

    // Wake up via button / user action
    page.app.wakeUp()
    assert.equal(page.app.isDormant, false)
    assert.equal(page.sockets.length, 2)
})



