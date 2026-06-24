package mygin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/service/dao"
)

func TestAuthorizeAbortsUnauthorizedMemberRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dao.Conf = &model.Config{}
	dao.Conf.Site.CookieName = "nodekeep"

	called := false
	r := gin.New()
	r.Use(Authorize(AuthorizeOption{Member: true}))
	r.GET("/admin", func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r.ServeHTTP(w, req)

	if called {
		t.Fatal("protected handler was called for unauthorized request")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRequireSameOriginForUnsafeRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		method     string
		origin     string
		referer    string
		secFetch   string
		header     bool
		wantCalled bool
	}{
		{
			name:       "same origin post",
			method:     http.MethodPost,
			origin:     "https://example.com",
			header:     true,
			wantCalled: true,
		},
		{
			name:       "same origin post without nodekeep header",
			method:     http.MethodPost,
			origin:     "https://example.com",
			wantCalled: false,
		},
		{
			name:       "cross origin post",
			method:     http.MethodPost,
			origin:     "https://evil.example",
			header:     true,
			wantCalled: false,
		},
		{
			name:       "cross site fetch metadata",
			method:     http.MethodDelete,
			secFetch:   "cross-site",
			header:     true,
			wantCalled: false,
		},
		{
			name:       "same origin fetch metadata",
			method:     http.MethodPost,
			secFetch:   "same-origin",
			header:     true,
			wantCalled: true,
		},
		{
			name:       "safe get",
			method:     http.MethodGet,
			origin:     "https://evil.example",
			wantCalled: true,
		},
		{
			name:       "same referer post",
			method:     http.MethodPost,
			referer:    "https://example.com/server",
			header:     true,
			wantCalled: true,
		},
		{
			name:       "cross referer post",
			method:     http.MethodPost,
			referer:    "https://evil.example/server",
			header:     true,
			wantCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			r := gin.New()
			r.Use(RequireSameOriginForUnsafeRequests())
			r.Handle(tt.method, "/api", func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "https://example.com/api", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			if tt.secFetch != "" {
				req.Header.Set("Sec-Fetch-Site", tt.secFetch)
			}
			if tt.header {
				req.Header.Set(NodeKeepRequestHeader, "1")
			}
			r.ServeHTTP(w, req)

			if called != tt.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}

func TestSetSecureCookieAttributes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		SetSecureCookie(c, "nodekeep", "token", 60)
		c.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	r.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly {
		t.Fatal("auth cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if !cookie.Secure {
		t.Fatal("HTTPS auth cookie is not Secure")
	}
}
