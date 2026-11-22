package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Twinkle661/NamiDiary/internal/config"
)

func TestAuthHostOriginAndBody(t *testing.T) {
	c := config.Defaults()
	c.App.APIKey = "secret"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var b map[string]any
			if err := Body(r, &b); err != nil {
				Error(w, err, false)
				return
			}
		}
		JSON(w, 200, map[string]bool{"ok": true})
	})
	handler := Middleware(c, next)
	for _, tc := range []struct {
		path, host, origin, token string
		want                      int
	}{{"/healthz", "example.test", "", "", 200}, {"/memory/topics", "localhost", "", "", 401}, {"/mcp", "evil.test", "", "Bearer secret", 403}, {"/mcp", "localhost:8188", "https://evil.test", "Bearer secret", 403}, {"/mcp", "localhost:8188", "http://localhost:3000", "Bearer secret", 200}, {"/mcp", "[::1]:8188", "", "Bearer secret", 200}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%+v: %d", tc, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/memory/entries", strings.NewReader(`{"content":"`+strings.Repeat("x", 1<<20)+`"}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("oversized request accepted")
	}
}
