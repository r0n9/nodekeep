package controller

import (
	"compress/gzip"
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"code.cloudfoundry.org/bytefmt"
	"github.com/gin-gonic/gin"

	"github.com/r0n9/nodekeep/pkg/geoip"
	"github.com/r0n9/nodekeep/pkg/mygin"
	"github.com/r0n9/nodekeep/service/dao"
)

func ServeWeb() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	if dao.Conf.Debug {
		gin.SetMode(gin.DebugMode)
	}
	r := gin.Default()
	r.Use(mygin.RecordPath)
	r.Use(gzipJSON())
	r.SetFuncMap(template.FuncMap{
		"tf": func(t time.Time) string {
			return t.Format("2006年 1月2日 15:04:05")
		},
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
		"tag": func(s string) template.HTML {
			return template.HTML(`<` + s + `>`)
		},
		"stf": func(s uint64) string {
			return time.Unix(int64(s), 0).Format("2006年1月2日 15:04")
		},
		"sf": func(duration uint64) string {
			return time.Duration(time.Duration(duration) * time.Second).String()
		},
		"bf": func(b uint64) string {
			return bytefmt.ByteSize(b)
		},
		"ts": func(s string) string {
			return strings.TrimSpace(s)
		},
		"ipv4": func(s string) string {
			ipv4, _ := geoip.ExtractIPs(s)
			return ipv4
		},
		"ipv6": func(s string) string {
			_, ipv6 := geoip.ExtractIPs(s)
			return ipv6
		},
		"shortIPv6": func(s string) string {
			return geoip.ShortIPv6(s)
		},
		"float32f": func(f float32) string {
			return fmt.Sprintf("%.2f", f)
		},
		"divU64": func(a, b uint64) float32 {
			if b == 0 {
				if a > 0 {
					return 100
				}
				return 0
			}
			if a == 0 {
				// 这是从未在线的情况
				return 1 / float32(b) * 100
			}
			return float32(a) / float32(b) * 100
		},
		"div": func(a, b int) float32 {
			if b == 0 {
				if a > 0 {
					return 100
				}
				return 0
			}
			if a == 0 {
				// 这是从未在线的情况
				return 1 / float32(b) * 100
			}
			return float32(a) / float32(b) * 100
		},
		"addU64": func(a, b uint64) uint64 {
			return a + b
		},
		"add": func(a, b int) int {
			return a + b
		},
		"dayBefore": func(i int) string {
			year, month, day := time.Now().Date()
			today := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
			return today.AddDate(0, 0, i-29).Format("1月2日")
		},
		"className": func(percent float32) string {
			if percent == 0 {
				return ""
			}
			if percent > 95 {
				return "good"
			}
			if percent > 80 {
				return "warning"
			}
			return "danger"
		},
		"statusName": func(percent float32) string {
			if percent == 0 {
				return "无数据"
			}
			if percent > 95 {
				return "良好"
			}
			if percent > 80 {
				return "低可用"
			}
			return "故障"
		},
	})
	r.Static("/static", "resource/static")
	r.LoadHTMLGlob("resource/template/**/*")
	routers(r)
	return r
}

func routers(r *gin.Engine) {
	// 通用页面
	cp := commonPage{r}
	cp.serve()
	// 游客页面
	gp := guestPage{r}
	gp.serve()
	// 会员页面
	mp := &memberPage{r}
	mp.serve()
	// API
	api := r.Group("api")
	{
		ma := &memberAPI{api}
		ma.serve()
	}
}

// gzipJSON compresses JSON API responses when the client accepts gzip.
func gzipJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}
		// Only wrap once we know the response is JSON-ish; decide after handlers via writer proxy
		// that activates on first Write when content-type is json.
		writer := &lazyGzipWriter{ResponseWriter: c.Writer, gz: gzip.NewWriter(io.Discard)}
		c.Writer = writer
		defer writer.Close()
		c.Next()
	}
}

type lazyGzipWriter struct {
	gin.ResponseWriter
	gz       *gzip.Writer
	enabled  bool
	disabled bool
}

func (w *lazyGzipWriter) Write(data []byte) (int, error) {
	if !w.enabled && !w.disabled {
		contentType := w.Header().Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			w.enabled = true
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Add("Vary", "Accept-Encoding")
			w.Header().Del("Content-Length")
			w.gz.Reset(w.ResponseWriter)
		} else {
			w.disabled = true
		}
	}
	if w.enabled {
		return w.gz.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *lazyGzipWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *lazyGzipWriter) Close() {
	if w.enabled {
		_ = w.gz.Close()
	}
}
