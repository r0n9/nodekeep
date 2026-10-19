(function (root) {
    'use strict'

    var OFFSCREEN_LEFT = -99999
    var MIN_WIDTH = 320
    var SCALE = 2

    // 文件名里不能出现路径分隔符与 Windows 保留字符，统一替换成下划线。
    function safeFileName(name) {
        var cleaned = String(name == null ? '' : name)
            .replace(/[\\/:*?"<>|\u0000-\u001f]/g, '_')
            .replace(/\s+/g, ' ')
            .trim()
        if (!cleaned) {
            cleaned = 'node'
        }
        if (cleaned.length > 60) {
            cleaned = cleaned.slice(0, 60).trim()
        }
        return cleaned
    }

    function timestamp(now) {
        var d = now || new Date()
        var pad = function (value) { return ('0' + value).slice(-2) }
        return d.getFullYear() + pad(d.getMonth() + 1) + pad(d.getDate()) +
            '-' + pad(d.getHours()) + pad(d.getMinutes()) + pad(d.getSeconds())
    }

    // <details> 的折叠态是浏览器用 UA shadow DOM 的 content-visibility 实现的，
    // 不是子元素 display:none，交给截图库处理不可靠。这里自己按真实 open 状态
    // 处理，保证「展开就带详情、折叠就没有详情」，且图里不留假交互。
    function prepareDetails(card) {
        var details = card.querySelectorAll('details')
        for (var i = 0; i < details.length; i++) {
            var node = details[i]
            if (node.open) {
                // 展开态：去掉可收起的箭头，summary 降级成普通小标题。
                var toggle = node.querySelector('.nk-node-details-toggle')
                if (toggle && toggle.parentNode) {
                    toggle.parentNode.removeChild(toggle)
                }
            } else if (node.parentNode) {
                // 折叠态：整块移除，图里不出现点不动的折叠条。
                node.parentNode.removeChild(node)
            }
        }
    }

    // cloneNode 只复制标签不复制位图，克隆里的 <canvas> 会是空白，
    // 必须用 toDataURL 把 uPlot 画好的位图搬到 <img> 上。
    function freezeCanvases(source, clone) {
        var sourceCanvases = source.querySelectorAll('canvas')
        var cloneCanvases = clone.querySelectorAll('canvas')
        for (var i = 0; i < cloneCanvases.length; i++) {
            var canvas = cloneCanvases[i]
            var origin = sourceCanvases[i]
            var img = document.createElement('img')
            try {
                img.src = origin.toDataURL()
            } catch (error) {
                // 画布被跨域内容污染时 toDataURL 会抛错；跳过比整体失败好。
                continue
            }
            var width = canvas.getAttribute('width') || origin.width
            var height = canvas.getAttribute('height') || origin.height
            img.setAttribute('width', width)
            img.setAttribute('height', height)
            // 只在拿得到正的 CSS 尺寸时才覆盖，否则保留 img 的固有尺寸，
            // 避免隐藏元素 clientWidth 为 0 时把图片压成 0px。
            var cssWidth = canvas.style.width || (origin.clientWidth > 0 ? origin.clientWidth + 'px' : '')
            var cssHeight = canvas.style.height || (origin.clientHeight > 0 ? origin.clientHeight + 'px' : '')
            if (cssWidth) {
                img.style.width = cssWidth
            }
            if (cssHeight) {
                img.style.height = cssHeight
            }
            img.style.display = 'block'
            canvas.parentNode.replaceChild(img, canvas)
        }
    }

    function waitForFonts() {
        if (document.fonts && document.fonts.ready && typeof document.fonts.ready.then === 'function') {
            return document.fonts.ready.catch(function () {})
        }
        return Promise.resolve()
    }

    function toBlob(canvas) {
        return new Promise(function (resolve, reject) {
            if (canvas.toBlob) {
                canvas.toBlob(function (blob) {
                    blob ? resolve(blob) : reject(new Error('导出图片失败'))
                }, 'image/png')
                return
            }
            try {
                var data = canvas.toDataURL('image/png')
                var base64 = data.split(',')[1]
                var binary = atob(base64)
                var bytes = new Uint8Array(binary.length)
                for (var i = 0; i < binary.length; i++) {
                    bytes[i] = binary.charCodeAt(i)
                }
                resolve(new Blob([bytes], { type: 'image/png' }))
            } catch (error) {
                reject(error)
            }
        })
    }

    function download(blob, fileName) {
        var url = URL.createObjectURL(blob)
        var link = document.createElement('a')
        link.href = url
        link.download = fileName
        link.style.display = 'none'
        document.body.appendChild(link)
        link.click()
        document.body.removeChild(link)
        // 立刻 revoke 在部分浏览器会中断下载，延后释放。
        root.setTimeout(function () { URL.revokeObjectURL(url) }, 10000)
    }

    // capture 把一张卡片渲染成 PNG 并触发下载。
    // options: { name, scale, backgroundColor }
    function capture(cardEl, options) {
        var opts = options || {}
        if (!cardEl) {
            return Promise.reject(new Error('找不到卡片元素'))
        }
        if (typeof root.html2canvas !== 'function') {
            return Promise.reject(new Error('截图组件未加载'))
        }

        var width = Math.max(MIN_WIDTH, Math.round(cardEl.getBoundingClientRect().width))
        var host = document.createElement('div')
        host.setAttribute('aria-hidden', 'true')
        host.style.position = 'fixed'
        host.style.left = OFFSCREEN_LEFT + 'px'
        host.style.top = '0'
        host.style.width = width + 'px'
        host.style.pointerEvents = 'none'
        host.style.zIndex = '-1'

        var clone = cardEl.cloneNode(true)
        clone.style.width = width + 'px'
        clone.style.margin = '0'
        freezeCanvases(cardEl, clone)
        prepareDetails(clone)
        host.appendChild(clone)
        document.body.appendChild(host)

        return waitForFonts()
            .then(function () {
                return root.html2canvas(clone, {
                    scale: opts.scale || SCALE,
                    backgroundColor: opts.backgroundColor === undefined ? null : opts.backgroundColor,
                    logging: false,
                    width: width,
                    windowWidth: width,
                })
            })
            .then(function (canvas) {
                return toBlob(canvas)
            })
            .then(function (blob) {
                download(blob, 'nodekeep-' + safeFileName(opts.name) + '-' + timestamp() + '.png')
            })
            .then(function () {
                if (host.parentNode) {
                    host.parentNode.removeChild(host)
                }
            }, function (error) {
                if (host.parentNode) {
                    host.parentNode.removeChild(host)
                }
                throw error
            })
    }

    root.NodeKeepCardExport = {
        capture: capture,
        safeFileName: safeFileName,
    }
})(window)
