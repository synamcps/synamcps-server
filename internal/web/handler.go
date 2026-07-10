package web

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/session"
)

// UI assets are kept in separate .html/.js files (see templates/) and embedded
// into the binary, instead of being inlined as Go string literals.
//
//go:embed templates/login.html
var loginHTML string

//go:embed templates/app.html
var adminHTML string

//go:embed templates/user-app.html
var userHTML string

//go:embed templates/mcp-connect.html
var mcpConnectHTML string

//go:embed templates/app.js
var adminJS []byte

//go:embed templates/user-app.js
var userJS []byte

//go:embed templates/mcp-connect.js
var mcpConnectJS []byte

type Capabilities struct {
	Transports []string `json:"transports"`
	Auth       []string `json:"auth"`
}

func NewHandler(cfg config.Config, sessions *session.Store, accessService *access.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		caps := Capabilities{
			Transports: []string{"streamable_http"},
			Auth:       []string{},
		}
		if cfg.Transport.LegacySSE {
			caps.Transports = append(caps.Transports, "legacy_sse")
		}
		for _, p := range cfg.OAuth.Providers {
			caps.Auth = append(caps.Auth, p.Name)
		}
		if cfg.Teleport.Enabled {
			caps.Auth = append(caps.Auth, "teleport_proxy")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(caps)
	})

	// Embedded JS bundles (registered before the generic /assets/ file server;
	// exact-path patterns take precedence over the subtree pattern).
	mux.HandleFunc("/assets/app.js", serveAsset(adminJS, "application/javascript; charset=utf-8"))
	mux.HandleFunc("/assets/admin.js", serveAsset(adminJS, "application/javascript; charset=utf-8"))
	mux.HandleFunc("/assets/user-app.js", serveAsset(userJS, "application/javascript; charset=utf-8"))
	mux.HandleFunc("/assets/mcp-connect.js", serveAsset(mcpConnectJS, "application/javascript; charset=utf-8"))
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("web/assets"))))
	mux.HandleFunc("/", indexHandler(cfg, sessions))
	mux.HandleFunc("/login", loginHandler(cfg, sessions, accessService))
	mux.HandleFunc("/logout", logoutHandler())
	mux.HandleFunc("/admin", adminHandler(sessions))
	mux.HandleFunc("/admin/mcp-connect", adminMCPConnectHandler(sessions))
	mux.HandleFunc("/app", userAppHandler(cfg, sessions))
	mux.HandleFunc("/app/mcp-connect", redirectHandler("/app"))
	return mux
}

func serveAsset(content []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(content)
	}
}

func writeHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func loginHandler(cfg config.Config, sessions *session.Store, accessService *access.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeHTML(w, loginHTML)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")
		expectedPassword := cfg.DefaultAdminPassword()
		if cfg.Web.Admin.Enabled && expectedPassword != "" && username == cfg.Web.Admin.Username && password == expectedPassword {
			principal := models.Principal{
				UserID:     "default-admin",
				Email:      cfg.Web.Admin.Username,
				SubjectKey: "user:internal:default-admin",
				Scopes:     []string{"platform_admin", "admin"},
				AuthSource: "internal",
			}
			createWebLogin(w, r, sessions, principal, cfg.Web.Admin.SessionTTLHours)
			return
		}

		if accessService != nil {
			user, ok, err := accessService.Store().AuthenticateUser(r.Context(), username, password)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if ok {
				principal := models.Principal{
					UserID:     user.ExternalSubject,
					Email:      user.Email,
					SubjectKey: user.SubjectKey,
					AuthSource: user.Source,
				}
				if user.SubjectKey == "user:internal:default-admin" {
					principal.Scopes = []string{"platform_admin", "admin"}
				}
				createWebLogin(w, r, sessions, principal, cfg.Web.Admin.SessionTTLHours)
				return
			}
		}
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	}
}

func createWebLogin(w http.ResponseWriter, r *http.Request, sessions *session.Store, principal models.Principal, ttlHours int) {
	if ttlHours <= 0 {
		ttlHours = 12
	}
	ws := sessions.CreateWebSession(principal, time.Duration(ttlHours)*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    ws.SessionID,
		Path:     "/",
		Expires:  ws.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	if isAdminPrincipal(principal) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func logoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func indexHandler(cfg config.Config, sessions *session.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		ws, ok := webSessionFromRequest(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if isAdminPrincipal(ws.Principal) {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		if cfg.Web.UserUI.Enabled {
			http.Redirect(w, r, "/app", http.StatusSeeOther)
			return
		}
		http.Error(w, "user UI disabled", http.StatusNotFound)
	}
}

func adminHandler(sessions *session.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin" {
			http.NotFound(w, r)
			return
		}
		ws, ok := webSessionFromRequest(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !isAdminPrincipal(ws.Principal) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeHTML(w, strings.ReplaceAll(adminHTML, "__CSRF_TOKEN__", ws.CSRFToken))
	}
}

func adminMCPConnectHandler(sessions *session.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, ok := webSessionFromRequest(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !isAdminPrincipal(ws.Principal) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeHTML(w, mcpConnectHTML)
	}
}

func userAppHandler(cfg config.Config, sessions *session.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app" {
			http.NotFound(w, r)
			return
		}
		if !cfg.Web.UserUI.Enabled {
			http.Error(w, "user UI disabled", http.StatusNotFound)
			return
		}
		ws, ok := webSessionFromRequest(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		writeHTML(w, strings.ReplaceAll(userHTML, "__CSRF_TOKEN__", ws.CSRFToken))
	}
}

func redirectHandler(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, path, http.StatusSeeOther)
	}
}

func webSessionFromRequest(r *http.Request, sessions *session.Store) (models.WebSession, bool) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		return models.WebSession{}, false
	}
	return sessions.GetWebSession(cookie.Value)
}

func isAdminPrincipal(p models.Principal) bool {
	for _, scope := range p.Scopes {
		if scope == "platform_admin" || scope == "admin" {
			return true
		}
	}
	return false
}
