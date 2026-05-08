package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zmiishe/synamcps/internal/config"
	"github.com/zmiishe/synamcps/internal/usage"
)

type pinger interface {
	Ping(ctx context.Context) error
}

type StatusHandler struct {
	cfg       config.Config
	usage     *usage.Service
	postgres  pinger
	redis     pinger
	s3        pinger
}

func NewStatusHandler(cfg config.Config, usageService *usage.Service, postgres pinger, redis pinger, s3 pinger) *StatusHandler {
	return &StatusHandler{cfg: cfg, usage: usageService, postgres: postgres, redis: redis, s3: s3}
}

type componentStatus struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Role        string            `json:"role,omitempty"`
	Model       string            `json:"model,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	Available   bool              `json:"available"`
	ErrorCount  int64             `json:"errorCount"`
	Color       string            `json:"color"`
	Message     string            `json:"message,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
}

type statusResponse struct {
	WindowSeconds int64             `json:"windowSeconds"`
	Components    []componentStatus `json:"components"`
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !isPlatformAdmin(p) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	window := 15 * time.Minute
	comps := make([]componentStatus, 0, 8)
	comps = append(comps, h.checkComponent(r.Context(), "postgres", "postgresql", "", h.postgres, window))
	comps = append(comps, h.checkComponent(r.Context(), "redis", "redis", "", h.redis, window))
	comps = append(comps, h.checkComponent(r.Context(), "s3", "s3", "", h.s3, window))

	// LLMs: show name (model) and role from config.
	comps = append(comps, h.checkLLM(r.Context(), "summarization", h.cfg.Summarization, window))
	comps = append(comps, h.checkLLM(r.Context(), "embedding", h.cfg.Embedding, window))

	writeJSON(w, statusResponse{
		WindowSeconds: int64(window.Seconds()),
		Components:    comps,
	}, http.StatusOK)
}

func (h *StatusHandler) checkComponent(ctx context.Context, name, kind, role string, p pinger, window time.Duration) componentStatus {
	st := componentStatus{Name: name, Kind: kind, Role: role}
	if p == nil {
		st.Available = false
		st.Message = "not configured"
	} else if err := p.Ping(ctx); err != nil {
		st.Available = false
		st.Message = err.Error()
		if h.usage != nil {
			h.usage.RecordStatusError(ctx, name)
		}
	} else {
		st.Available = true
	}
	if h.usage != nil {
		st.ErrorCount = h.usage.StatusErrorCount(ctx, name, window)
	}
	st.Color = statusColor(st.Available, st.ErrorCount)
	return st
}

func (h *StatusHandler) checkLLM(ctx context.Context, role string, cfg config.ModelConfig, window time.Duration) componentStatus {
	name := "llm_" + role
	st := componentStatus{
		Name:     name,
		Kind:     "llm",
		Role:     role,
		Model:    cfg.Model,
		Provider: cfg.Provider,
		Meta:     map[string]string{},
	}
	if cfg.API != "" {
		st.Meta["api"] = cfg.API
	}

	// Default/simple providers are always "available" (no external dependency).
	if strings.TrimSpace(strings.ToLower(cfg.Provider)) == "" || strings.TrimSpace(strings.ToLower(cfg.Provider)) == "simple" {
		st.Available = true
		st.ErrorCount = 0
		st.Color = "green"
		return st
	}

	// If API is set, do a TCP reachability check to host:port.
	ok, msg := pingURLHost(ctx, cfg.API, 2*time.Second)
	st.Available = ok
	if !ok && msg != "" {
		st.Message = msg
		if h.usage != nil {
			h.usage.RecordStatusError(ctx, name)
		}
	}
	if h.usage != nil {
		st.ErrorCount = h.usage.StatusErrorCount(ctx, name, window)
	}
	st.Color = statusColor(st.Available, st.ErrorCount)
	return st
}

func statusColor(available bool, errors int64) string {
	if !available {
		return "red"
	}
	// "few errors but available" -> yellow
	if errors > 0 && errors <= 3 {
		return "yellow"
	}
	if errors > 3 {
		return "yellow"
	}
	return "green"
}

func pingURLHost(ctx context.Context, raw string, timeout time.Duration) (bool, string) {
	if raw == "" {
		return false, "api not configured"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false, "invalid api url"
	}
	host := u.Host
	if host == "" {
		// allow "host:port" without scheme
		u2, err2 := url.Parse("https://" + raw)
		if err2 != nil {
			return false, "invalid api url"
		}
		host = u2.Host
		u = u2
	}
	if !strings.Contains(host, ":") {
		if strings.EqualFold(u.Scheme, "http") {
			host += ":80"
		} else {
			host += ":443"
		}
	}
	d := &net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return false, err.Error()
	}
	_ = c.Close()
	return true, ""
}

