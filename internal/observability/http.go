package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const maxLoggedResponseBytes = 8 << 10 // 8 KiB

type Pinger interface {
	Ping(ctx context.Context) error
}

type HTTPMetrics struct {
	requestsTotal uint64
	errorsTotal   uint64
	usageExporter interface{ Prometheus() string }
}

func NewHTTPMetrics() *HTTPMetrics { return &HTTPMetrics{} }

func (m *HTTPMetrics) AttachUsageExporter(exporter interface{ Prometheus() string }) {
	m.usageExporter = exporter
}

func (m *HTTPMetrics) Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		atomic.AddUint64(&m.requestsTotal, 1)
		reqID := uuid.NewString()
		w.Header().Set("X-Request-Id", reqID)

		capture := shouldCaptureResponse(r.URL.Path)
		rec := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
			capture:        capture,
			maxBody:        maxLoggedResponseBytes,
		}
		if capture {
			rec.body = &bytes.Buffer{}
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				atomic.AddUint64(&m.errorsTotal, 1)
				logger.Error("panic in request", "request_id", reqID, "panic", recovered, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
			if rec.status >= 500 {
				atomic.AddUint64(&m.errorsTotal, 1)
			}
			attrs := []any{
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
			}
			if mcpMethod := rec.Header().Get("X-MCP-Method"); mcpMethod != "" {
				attrs = append(attrs, "mcp_method", mcpMethod)
			}
			if sid := rec.Header().Get("Mcp-Session-Id"); sid != "" {
				attrs = append(attrs, "mcp_session_id", sid)
			}
			if pv := rec.Header().Get("MCP-Protocol-Version"); pv != "" {
				attrs = append(attrs, "mcp_protocol_version", pv)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				attrs = append(attrs, "location", redactLocation(loc))
			}
			if www := rec.Header().Get("WWW-Authenticate"); www != "" {
				attrs = append(attrs, "www_authenticate", www)
			}
			if capture && rec.body != nil && rec.body.Len() > 0 {
				attrs = append(attrs, "response", redactResponseForLog(rec.body.String()))
				if rec.truncated {
					attrs = append(attrs, "response_truncated", true)
				}
			}
			logger.Info("http_request", attrs...)
		}()

		next.ServeHTTP(rec, r)
	})
}

func (m *HTTPMetrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w, "http_requests_total %d\n", atomic.LoadUint64(&m.requestsTotal))
		_, _ = fmt.Fprintf(w, "http_errors_total %d\n", atomic.LoadUint64(&m.errorsTotal))
		if m.usageExporter != nil {
			_, _ = w.Write([]byte(m.usageExporter.Prometheus()))
		}
	}
}

func ReadyzHandler(logger *slog.Logger, checks map[string]Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		type result struct {
			OK    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		}
		out := map[string]result{}
		allOK := true
		for name, checker := range checks {
			if checker == nil {
				out[name] = result{OK: true}
				continue
			}
			if err := checker.Ping(ctx); err != nil {
				allOK = false
				out[name] = result{OK: false, Error: err.Error()}
				logger.Error("readiness check failed", "name", name, "error", err)
			} else {
				out[name] = result{OK: true}
			}
		}
		status := http.StatusOK
		if !allOK {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      allOK,
			"checks":  out,
			"version": "0.1.0",
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status    int
	capture   bool
	maxBody   int
	body      *bytes.Buffer
	truncated bool
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.capture && r.body != nil && r.maxBody > 0 {
		remain := r.maxBody - r.body.Len()
		if remain > 0 {
			if len(p) > remain {
				r.body.Write(p[:remain])
				r.truncated = true
			} else {
				r.body.Write(p)
			}
		} else {
			r.truncated = true
		}
	}
	return r.ResponseWriter.Write(p)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func shouldCaptureResponse(path string) bool {
	switch {
	case path == "/mcp":
		return true
	case strings.HasPrefix(path, "/oauth/"):
		return true
	case strings.HasPrefix(path, "/.well-known/"):
		return true
	case path == "/register", path == "/authorize", path == "/token":
		return true
	default:
		return false
	}
}

func redactResponseForLog(body string) string {
	ctHint := strings.TrimSpace(body)
	lower := strings.ToLower(ctHint)
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") {
		return "[html omitted]"
	}
	return redactJSONSecrets(body)
}

func redactLocation(loc string) string {
	u, err := url.Parse(loc)
	if err != nil || u == nil {
		return loc
	}
	q := u.Query()
	changed := false
	for _, key := range []string{"code", "access_token", "refresh_token", "id_token"} {
		if q.Has(key) {
			q.Set(key, "[redacted]")
			changed = true
		}
	}
	if !changed {
		return loc
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func redactJSONSecrets(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return raw
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return raw
	}
	redactSecretValues(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(out)
}

func redactSecretValues(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			switch strings.ToLower(k) {
			case "access_token", "refresh_token", "id_token", "client_secret", "rawtoken", "password", "code_verifier":
				t[k] = "[redacted]"
			default:
				redactSecretValues(child)
			}
		}
	case []any:
		for _, child := range t {
			redactSecretValues(child)
		}
	}
}
