package httpx

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Body(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return apperr.Invalid("Invalid JSON request: " + err.Error())
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return apperr.Invalid("JSON body must be an object.")
	}
	typed := json.NewDecoder(strings.NewReader(string(raw)))
	typed.DisallowUnknownFields()
	if err := typed.Decode(v); err != nil {
		return apperr.Invalid("Invalid JSON request: " + err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return apperr.Invalid("Expected one JSON object.")
	}
	return nil
}
func ErrorBody(err error, debug bool) (int, map[string]any) {
	var e *apperr.Error
	if !errors.As(err, &e) {
		e = &apperr.Error{Status: 500, Code: "INTERNAL_ERROR", Message: "Internal server error.", Cause: err}
	}
	message := e.Message
	if e.Status >= 500 && e.Status != 503 {
		message = "The request could not be completed."
	}
	b := map[string]any{"status": "error", "code": e.Code, "message": message}
	if e.Details != nil {
		b["details"] = e.Details
	}
	if debug && e.Status >= 500 {
		b["debug"] = map[string]string{"error": err.Error()}
	}
	return e.Status, b
}
func Error(w http.ResponseWriter, err error, debug bool) {
	status, b := ErrorBody(err, debug)
	if status >= 500 {
		slog.Error("Request failed", "error", err)
	}
	if status == 503 {
		w.Header().Set("Retry-After", "10")
	}
	JSON(w, status, b)
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func Middleware(c config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				slog.Error("Request panic", "error", v)
				if rw.status == 0 {
					Error(rw, apperr.New(500, "INTERNAL_ERROR", "Internal server error."), false)
				}
			}
			status := rw.status
			if status == 0 {
				status = 200
			}
			slog.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "status", status, "duration", time.Since(start))
		}()
		r.Body = http.MaxBytesReader(rw, r.Body, 1<<20)
		if r.URL.Path != "/healthz" {
			expected := sha256.Sum256([]byte("Bearer " + c.App.APIKey))
			actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
			if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
				Error(rw, apperr.New(401, "UNAUTHORIZED", "A valid bearer token is required."), c.App.Debug)
				return
			}
		}
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
			if !allowed(r.Host, c.MCP.AllowedHosts) {
				Error(rw, apperr.New(403, "FORBIDDEN_ORIGIN", "MCP host is not allowed."), false)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || !allowed(u.Host, c.MCP.AllowedOrigins) {
					Error(rw, apperr.New(403, "FORBIDDEN_ORIGIN", "MCP origin is not allowed."), false)
					return
				}
			}
		}
		next.ServeHTTP(rw, r)
	})
}
func allowed(host string, list []string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	for _, h := range list {
		if strings.EqualFold(host, strings.Trim(h, "[]")) {
			return true
		}
	}
	return false
}
