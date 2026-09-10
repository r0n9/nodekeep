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
