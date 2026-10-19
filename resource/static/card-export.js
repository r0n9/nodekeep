(function (root) {
    'use strict'

    var OFFSCREEN_LEFT = -99999
    var MIN_WIDTH = 320
    var SCALE = 2
    // html2canvas 会把文档克隆进一个 iframe，iframe 的宽度就是媒体查询的视口宽度。
    // 卡片本身只有 300~400px，若按卡片宽度设视口，`max-width: 640px` 会命中，
    // 导出图会变成移动端布局（详情单列、内边距变小），与屏幕上看到的卡片不一致。
    // 因此视口至少取桌面容器宽度，保证「不管用什么设备导出，都是桌面版布局」。
    var DESKTOP_MIN_WIDTH = 1180
    // 卡片下方签名带的高度与左右内边距（CSS px，未乘 scale）。
    // 高度按右侧两行文字（仓库 / 时间）留出上下呼吸空间。
    var FOOTER_HEIGHT = 46
    var FOOTER_PAD_X = 14
    // 右侧两行之间的行距（CSS px，未乘 scale）。
    var FOOTER_LINE_GAP = 4
    var FONT_STACK = '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
    // 签名带左侧的品牌标记。logo.svg 自带深色圆角底，浅色 / 暗色主题下都清晰。
    var LOGO_URL = '/static/logo.svg'
    var LOGO_SIZE = 15
    // 签名带右侧的仓库地址。GitHub mark 画得出来时用短名，否则退回完整地址。
    var REPO_SLUG = 'r0n9/nodekeep'
    var REPO_FULL = 'github.com/' + REPO_SLUG
    // GitHub 官方 mark（viewBox 0 0 16 16）。用 Path2D 画，不依赖图标字体。
    var GITHUB_MARK_PATH = 'M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.012 8.012 0 0 0 16 8c0-4.42-3.58-8-8-8z'

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

    function pad2(value) {
        return ('0' + value).slice(-2)
    }

    function timestamp(now) {
        var d = now || new Date()
        return d.getFullYear() + pad2(d.getMonth() + 1) + pad2(d.getDate()) +
            '-' + pad2(d.getHours()) + pad2(d.getMinutes()) + pad2(d.getSeconds())
    }

    // 签名带上的时间用「YYYY-MM-DD HH:mm」，精确到分钟足够，也短一些。
    function watermarkStamp(now) {
        var d = now || new Date()
        return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
            ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes())
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

    // 从 :root 读主题变量，导出图跟随当前主题；读不到时退回浅色默认值。
    function themeColors() {
        var fallback = { bg: '#f3f7fb', text: '#0f1b2d', muted: '#60708a', accent: '#0b84ff' }
        if (!root.getComputedStyle || !document.documentElement) {
            return fallback
        }
        var style = root.getComputedStyle(document.documentElement)
        if (!style || typeof style.getPropertyValue !== 'function') {
            return fallback
        }
        var read = function (name, value) {
            var current = style.getPropertyValue(name)
            return current && current.trim() ? current.trim() : value
        }
        return {
            bg: read('--nk-bg', fallback.bg),
            text: read('--nk-text', fallback.text),
            muted: read('--nk-muted', fallback.muted),
            accent: read('--nk-accent', fallback.accent),
        }
    }

    // canvas 的 letterSpacing 支持还不普遍，有就顺手用上，没有就算了。
    function setLetterSpacing(ctx, value) {
        if ('letterSpacing' in ctx) {
            ctx.letterSpacing = value
        }
    }

    function loadImage(url) {
        return new Promise(function (resolve) {
            if (typeof root.Image !== 'function') {
                resolve(null)
                return
            }
            var image = new root.Image()
            image.onload = function () { resolve(image) }
            image.onerror = function () { resolve(null) }
            image.src = url
        })
    }

    // logo.svg 只有 viewBox、没有 width/height，部分浏览器（Firefox）不会把这种
    // SVG 画进 canvas。这里读出 SVG 文本补上尺寸再转 data URL；任何一步失败都
    // 退回原始地址。不改 logo.svg 本身，免得影响其它页面里的尺寸。
    function sizedLogoUrl() {
        if (typeof root.fetch !== 'function') {
            return Promise.resolve(LOGO_URL)
        }
        return root.fetch(LOGO_URL).then(function (response) {
            return response.text()
        }).then(function (svg) {
            var sized = String(svg).replace(/<svg\b/,
                '<svg width="' + LOGO_SIZE + '" height="' + LOGO_SIZE + '"')
            if (sized === svg) {
                return LOGO_URL
            }
            return 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(sized)
        }).catch(function () {
            return LOGO_URL
        })
    }

    // logo 只加载一次，失败就退回强调色圆点，不让导出整体失败。
    var logoPromise = null
    function loadLogo() {
        if (logoPromise) {
            return logoPromise
        }
        logoPromise = sizedLogoUrl()
            .then(loadImage)
            .then(function (image) {
                return image || loadImage(LOGO_URL)
            })
            .catch(function () { return null })
        return logoPromise
    }

    // Path2D 解析 SVG 路径的能力要试探一下，个别浏览器会抛错。
    var githubMark = null
    var githubMarkChecked = false
    function githubMarkPath() {
        if (!githubMarkChecked) {
            githubMarkChecked = true
            if (typeof root.Path2D === 'function') {
                try {
                    githubMark = new root.Path2D(GITHUB_MARK_PATH)
                } catch (error) {
                    githubMark = null
                }
            }
        }
        return githubMark
    }

    function drawGithubMark(ctx, x, y, size, color) {
        var path = githubMarkPath()
        if (!path) {
            return false
        }
        ctx.save()
        ctx.translate(x, y)
        ctx.scale(size / 16, size / 16)
        ctx.fillStyle = color
        ctx.fill(path)
        ctx.restore()
        return true
    }

    // 品牌名可能很长（站点名可配置），放不下时截断加省略号，避免压到右侧。
    function truncate(ctx, text, maxWidth) {
        if (maxWidth <= 0) {
            return ''
        }
        if (ctx.measureText(text).width <= maxWidth) {
            return text
        }
        var low = 0
        var high = text.length
        while (low < high) {
            var mid = Math.ceil((low + high) / 2)
            if (ctx.measureText(text.slice(0, mid) + '…').width <= maxWidth) {
                low = mid
            } else {
                high = mid - 1
            }
        }
        return low > 0 ? text.slice(0, low) + '…' : ''
    }

    // 在卡片下方补一条签名带：左侧 logo + 品牌名（垂直居中），
    // 右侧上下两行——上行 GitHub 仓库、下行生成时间，整块右对齐并垂直居中。
    // 早先那版是盖在卡片右下角的半透明底衬，像贴上去的贴纸；改成卡片之外的
    // 留白签名带后更像一张设计过的分享图，底衬取主题背景色，浅色 / 暗色下
    // 都自带对比，不用再靠深色胶囊压字。
    // 空间不够时先丢仓库那一行、再截断品牌名，保证两端不会撞在一起。
    function withFooter(canvas, brand, scale, logo) {
        var name = String(brand == null ? '' : brand).trim()
        var colors = themeColors()
        var band = Math.round(FOOTER_HEIGHT * scale)
        var out = document.createElement('canvas')
        out.width = canvas.width
        out.height = canvas.height + band
        var ctx = out.getContext('2d')
        if (!ctx) {
            return canvas
        }
        ctx.fillStyle = colors.bg
        ctx.fillRect(0, 0, out.width, out.height)
        ctx.drawImage(canvas, 0, 0)

        var padX = Math.round(FOOTER_PAD_X * scale)
        var fontPx = Math.max(1, Math.round(11.5 * scale))
        var gap = Math.round(7 * scale)
        var minGap = Math.round(12 * scale)
        var lineGap = Math.round(FOOTER_LINE_GAP * scale)
        var lineHeight = fontPx + lineGap
        var centerY = canvas.height + band / 2
        var available = out.width - padX * 2
        var hasMark = !!githubMarkPath()
        var stamp = watermarkStamp()
        var repoLabel = hasMark ? REPO_SLUG : REPO_FULL
        var markSize = Math.round(12 * scale)
        var markGap = Math.round(5 * scale)
        ctx.textBaseline = 'middle'

        // 先量右侧两行，取较宽的那行作为右侧整块宽度。
        setLetterSpacing(ctx, '0px')
        ctx.font = '500 ' + fontPx + 'px ' + FONT_STACK
        var stampWidth = ctx.measureText(stamp).width
        var labelWidth = ctx.measureText(repoLabel).width
        var repoLineWidth = labelWidth + (hasMark ? markSize + markGap : 0)
        var repoBlock = Math.max(repoLineWidth, stampWidth)

        // 左侧：logo（拿不到就退回强调色圆点）+ 品牌名。
        var leadWidth = logo ? Math.round(LOGO_SIZE * scale) : Math.max(1, Math.round(2.5 * scale)) * 2
        setLetterSpacing(ctx, (fontPx * 0.04).toFixed(2) + 'px')
        ctx.font = '600 ' + fontPx + 'px ' + FONT_STACK
        var brandWidth = name ? ctx.measureText(name).width : 0
        var showRepo = leadWidth + gap + brandWidth + minGap + repoBlock <= available

        var rightWidth = showRepo ? repoBlock : stampWidth
        var brandRoom = available - rightWidth - minGap - leadWidth - gap
        var brandText = name ? truncate(ctx, name, brandRoom) : ''

        var cursor = padX
        if (logo) {
            var logoSize = Math.round(LOGO_SIZE * scale)
            ctx.drawImage(logo, cursor, Math.round(centerY - logoSize / 2), logoSize, logoSize)
            cursor += logoSize
        } else {
            var dotRadius = Math.max(1, Math.round(2.5 * scale))
            ctx.beginPath()
            ctx.arc(cursor + dotRadius, centerY, dotRadius, 0, Math.PI * 2)
            ctx.fillStyle = colors.accent
            ctx.fill()
            cursor += dotRadius * 2
        }
        if (brandText) {
            ctx.font = '600 ' + fontPx + 'px ' + FONT_STACK
            ctx.fillStyle = colors.text
            ctx.textAlign = 'left'
            ctx.fillText(brandText, cursor + gap, centerY)
        }

        // 右侧：两行时整块垂直居中；只剩时间时同样居中。
        var rightBlock = showRepo ? fontPx * 2 + lineGap : fontPx
        var blockTop = canvas.height + Math.round((band - rightBlock) / 2)
        var repoY = Math.round(blockTop + fontPx / 2)
        var stampY = repoY + lineHeight
        var rightEdge = out.width - padX
        setLetterSpacing(ctx, '0px')
        ctx.font = '500 ' + fontPx + 'px ' + FONT_STACK
        ctx.fillStyle = colors.muted
        ctx.textAlign = 'right'
        if (showRepo) {
            ctx.fillText(repoLabel, rightEdge, repoY)
            if (hasMark) {
                drawGithubMark(ctx, rightEdge - labelWidth - markGap - markSize, repoY - markSize / 2, markSize, colors.muted)
            }
            ctx.fillText(stamp, rightEdge, stampY)
        } else {
            ctx.fillText(stamp, rightEdge, repoY)
        }
        return out
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

    // render 只负责把卡片渲染成 PNG，不触发任何下载/剪贴板动作。
    // options: { name, brand, scale, backgroundColor }
    function render(cardEl, options) {
        var opts = options || {}
        if (!cardEl) {
            return Promise.reject(new Error('找不到卡片元素'))
        }
        if (typeof root.html2canvas !== 'function') {
            return Promise.reject(new Error('截图组件未加载'))
        }

        var width = Math.max(MIN_WIDTH, Math.round(cardEl.getBoundingClientRect().width))
        var viewportWidth = Math.max(root.innerWidth || 0, DESKTOP_MIN_WIDTH)
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

        var cleanup = function () {
            if (host.parentNode) {
                host.parentNode.removeChild(host)
            }
        }

        return Promise.all([waitForFonts(), loadLogo()])
            .then(function (ready) {
                return root.html2canvas(clone, {
                    scale: opts.scale || SCALE,
                    backgroundColor: opts.backgroundColor === undefined ? null : opts.backgroundColor,
                    logging: false,
                    width: width,
                    windowWidth: viewportWidth,
                }).then(function (canvas) {
                    return toBlob(withFooter(canvas, opts.brand, opts.scale || SCALE, ready[1]))
                })
            })
            .then(function (blob) {
                cleanup()
                return {
                    blob: blob,
                    fileName: 'nodekeep-' + safeFileName(opts.name) + '-' + timestamp() + '.png',
                }
            }, function (error) {
                cleanup()
                throw error
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

    // capture 渲染并下载。
    function capture(cardEl, options) {
        return render(cardEl, options).then(function (result) {
            download(result.blob, result.fileName)
        })
    }

    // 复制图片到剪贴板需要安全上下文（HTTPS 或 localhost）与 ClipboardItem。
    function canCopy() {
        return !!(root.isSecureContext &&
            typeof root.ClipboardItem === 'function' &&
            root.navigator && root.navigator.clipboard &&
            typeof root.navigator.clipboard.write === 'function')
    }

    // copy 渲染并写入剪贴板。
    function copy(cardEl, options) {
        if (!canCopy()) {
            return Promise.reject(new Error('当前环境不支持复制图片，请改用下载'))
        }
        return render(cardEl, options).then(function (result) {
            var item = new root.ClipboardItem({ 'image/png': result.blob })
            return root.navigator.clipboard.write([item])
        })
    }

    root.NodeKeepCardExport = {
        render: render,
        capture: capture,
        copy: copy,
        canCopy: canCopy,
        safeFileName: safeFileName,
        watermarkStamp: watermarkStamp,
    }
})(window)
