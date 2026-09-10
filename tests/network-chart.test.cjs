const test = require('node:test')
const assert = require('node:assert/strict')
const network = require('../resource/static/network-chart.js')

function server(time, netIn = 2048, netOut = 512, live = true) {
    return {
        reported: true,
        live,
        LastActive: new Date(time * 1000).toISOString(),
        State: { NetInSpeed: netIn, NetOutSpeed: netOut },
    }
}

test('repeated and out-of-order snapshots do not invent fresh samples', () => {
    const history = network.createHistory()
    network.recordSample(history, server(100), 100)
    network.recordSample(history, server(100, 9999), 102)
    network.recordSample(history, server(99, 9999), 103)
    network.recordSample(history, server(104, 4096), 104)
    assert.deepEqual(history.t, [100, 104])
    assert.deepEqual(history.netIn, [2048, 4096])
})

test('missing reports create a gap when data resumes', () => {
    const history = network.createHistory()
    network.recordSample(history, server(100), 100)
    network.recordSample(history, server(110, 0, 0), 110)
    assert.equal(history.t.length, 3)
    assert.ok(history.t[1] > 100 && history.t[1] < 110)
    assert.deepEqual(history.netIn, [2048, null, 0])
    assert.deepEqual(history.netOut, [512, null, 0])
})

test('offline and stale reports leave gaps instead of repeating rates or inventing zeros', () => {
    for (const stale of [server(100), server(107, 2048, 512, false)]) {
        const history = network.createHistory()
        network.recordSample(history, server(100), 100)
        network.recordSample(history, stale, 107)
        network.recordSample(history, stale, 109)
        assert.deepEqual(history.netIn, [2048, null])
        assert.deepEqual(history.netOut, [512, null])
    }
})

test('a short transport disconnect is still visible after reconnection', () => {
    const history = network.createHistory()
    network.recordSample(history, server(100), 100)
    network.breakHistory(history, 101)
    network.recordSample(history, server(102), 102)
    assert.deepEqual(history.t, [100, 101, 102])
    assert.deepEqual(history.netIn, [2048, null, 2048])
})

test('only the last minute is kept, including when no further data arrives', () => {
    const history = network.createHistory()
    for (let time = 100; time <= 200; time += 2) {
        network.recordSample(history, server(time, time, time * 2), time)
    }
    assert.equal(history.t[0], 140)
    assert.equal(history.t.at(-1), 200)
    assert.deepEqual(history.netIn, history.t)
    assert.deepEqual(history.netOut, history.t.map(time => time * 2))
    network.pruneHistory(history, 261)
    assert.deepEqual(history.t, [])
    assert.deepEqual(history.netIn, [])
    assert.deepEqual(history.netOut, [])
})

test('high-frequency input stays bounded and different nodes keep separate histories', () => {
    const first = network.createHistory()
    const second = network.createHistory()
    network.recordSample(second, server(100, 77), 100)
    for (let i = 0; i < 1000; i++) {
        const time = 100 + i / 100
        network.recordSample(first, server(time), time)
    }
    assert.ok(first.t.length <= 120)
    assert.equal(first.netIn.length, first.t.length)
    assert.equal(first.netOut.length, first.t.length)
    assert.deepEqual(second.netIn, [77])
})

test('invalid rates remain missing while an idle link is a real zero', () => {
    const history = network.createHistory()
    const missing = server(100, NaN)
    delete missing.State.NetOutSpeed
    network.recordSample(history, missing, 100)
    network.recordSample(history, server(102, -1, null), 102)
    network.recordSample(history, server(104, 0, 0), 104)
    assert.deepEqual(history.netIn, [null, null, 0])
    assert.deepEqual(history.netOut, [null, null, 0])
    for (const value of [null, undefined, NaN, Infinity, -1]) {
        assert.equal(network.formatSpeed(value), '—')
    }
    assert.equal(network.formatSpeed(0), '0 B/s')
    assert.equal(network.formatSpeed(1024), '1 KB/s')
    assert.equal(network.formatSpeed(2.4 * 1024 * 1024), '2.4 MB/s')
})
