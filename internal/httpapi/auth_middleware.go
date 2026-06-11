package httpapi

import (
	"context"
	"net/http"

	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/usage"
)

type AuthResolver struct {
	gateway  *auth.Gateway
	sessions *session.Store
}

func NewAuthResolver(gateway *auth.Gateway, sessions *session.Store) *AuthResolver {
	return &AuthResolver{gateway: gateway, sessions: sessions}
}

func (a *AuthResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session_id"); err == nil {
			if ws, ok := a.sessions.GetWebSession(c.Value); ok {
				if isMutatingMethod(r.Method) && r.Header.Get("X-CSRF-Token") != ws.CSRFToken {
					http.Error(w, "invalid csrf token", http.StatusForbidden)
					return
				}
				ctx := context.WithValue(r.Context(), auth.PrincipalContextKey, ws.Principal)
				ctx = context.WithValue(ctx, auth.AccessContextKey, models.APIAccessContext{
					Principal:     ws.Principal,
					AuthMode:      "web_session",
					GrantedScopes: ws.Principal.Scopes,
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		a.gateway.Middleware(next).ServeHTTP(w, r)
	})
}

func principalFromRequest(r *http.Request) (models.Principal, bool) {
	return auth.PrincipalFromContext(r.Context())
}

// rateLimitMiddleware enforces per-token rate limits on the REST API for
// requests authenticated with an access token. It must run after the auth
// middleware so the access context is populated. Requests authenticated via web
// session / JWT (no access token) are not throttled here. Fails closed if the
// rate limiter backend errors.
func rateLimitMiddleware(usageService *usage.Service, next http.Handler) http.Handler {
	if usageService == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ac, ok := auth.AccessContextFromContext(r.Context()); ok && ac.AccessToken != nil {
			allowed, err := usageService.Allow(r.Context(), *ac.AccessToken, "")
			if err != nil {
				http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
				return
			}
			if !allowed {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isMutatingMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

// defaultMaxBodyBytes bounds request bodies when no explicit limit is
// configured (covers 32 MiB uploads plus multipart overhead).
const defaultMaxBodyBytes int64 = 40 << 20

// maxBodyMiddleware caps the size of request bodies to avoid unbounded memory
// use from large JSON payloads or uploads.
func maxBodyMiddleware(limit int64, next http.Handler) http.Handler {
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
