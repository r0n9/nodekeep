package mygin

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/r0n9/nodekeep/model"
)

const NodeKeepRequestHeader = "X-NodeKeep-Request"

func SetSecureCookie(c *gin.Context, name, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   IsHTTPS(c),
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearSecureCookie(c *gin.Context, name string) {
	SetSecureCookie(c, name, "", -1)
}

func RequireSameOriginForUnsafeRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) {
			return
		}
		if c.GetHeader(NodeKeepRequestHeader) != "1" {
			rejectCrossSiteRequest(c)
			return
		}
		switch strings.ToLower(c.GetHeader("Sec-Fetch-Site")) {
		case "same-origin", "same-site", "none":
			return
		case "cross-site":
			rejectCrossSiteRequest(c)
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" {
			if !sameRequestHost(c, origin) {
				rejectCrossSiteRequest(c)
			}
			return
		}
		if referer := c.GetHeader("Referer"); referer != "" && !sameRequestHost(c, referer) {
			rejectCrossSiteRequest(c)
		}
	}
}

func IsHTTPS(c *gin.Context) bool {
	return RequestScheme(c) == "https"
}

func RequestScheme(c *gin.Context) string {
	if c.Request.TLS != nil {
		return "https"
	}
	if strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") ||
		strings.EqualFold(c.GetHeader("X-Forwarded-Ssl"), "on") {
		return "https"
	}
	for _, part := range strings.Split(c.GetHeader("Forwarded"), ";") {
		if strings.EqualFold(strings.TrimSpace(part), "proto=https") {
			return "https"
		}
	}
	return "http"
}

func NormalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err == nil {
			host = u.Host
		}
	}
	if hostname, port, err := net.SplitHostPort(host); err == nil {
		hostname = strings.Trim(hostname, "[]")
		if port == "80" || port == "443" {
			return hostname
		}
		return hostname + ":" + port
	}
	return strings.Trim(host, "[]")
}

func sameRequestHost(c *gin.Context, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return NormalizeHost(u.Host) == NormalizeHost(c.Request.Host)
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func rejectCrossSiteRequest(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{
		Code:    http.StatusForbidden,
		Message: "拒绝跨站管理请求",
	})
	c.Abort()
}
