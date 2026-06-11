package httpapi

import (
	"net/http"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/knowledge"
	"github.com/synamcps/synamcps-server/internal/mcpproxy"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/usage"
)

func NewRouter(gateway *auth.Gateway, sessions *session.Store, service *knowledge.Service, allowPartial bool) http.Handler {
	return NewRouterWithAdmin(gateway, sessions, service, nil, nil, "", allowPartial, nil, nil, nil, nil, 0)
}

func NewRouterWithAdmin(gateway *auth.Gateway, sessions *session.Store, service *knowledge.Service, accessService *access.Service, usageService *usage.Service, s3Bucket string, allowPartial bool, statusHandler *StatusHandler, mcpStore *mcpproxy.Store, mcpManager *mcpproxy.Manager, mcpAccess *mcpproxy.AccessService, maxBodyBytes int64) http.Handler {
	mux := http.NewServeMux()
	authResolver := NewAuthResolver(gateway, sessions)
	handler := NewKnowledgeHandler(service, allowPartial)

	if accessService != nil {
		admin := NewAdminHandler(accessService, usageService, s3Bucket)
		admin.AttachMCP(mcpStore, mcpManager, mcpAccess)
		mux.Handle("/api/admin/", authResolver.Middleware(admin))
		if statusHandler != nil {
			mux.Handle("GET /api/admin/status", authResolver.Middleware(statusHandler))
		}
	}
	ingestHandler := NewIngestHandler(service)
	guard := func(h http.HandlerFunc) http.Handler {
		return maxBodyMiddleware(maxBodyBytes, authResolver.Middleware(rateLimitMiddleware(usageService, h)))
	}
	mux.Handle("GET /api/knowledge", guard(handler.List))
	mux.Handle("POST /api/knowledge", guard(handler.Create("api")))
	mux.Handle("POST /api/admin/knowledge", guard(handler.Create("admin")))
	mux.Handle("POST /api/knowledge/ingest/file", guard(ingestHandler.IngestFile))
	mux.Handle("POST /api/knowledge/ingest/link", guard(ingestHandler.IngestLink))
	mux.Handle("POST /api/knowledge/search", guard(handler.Search))
	mux.Handle("GET /api/knowledge/", guard(handler.Get))
	mux.Handle("DELETE /api/knowledge/", guard(handler.Delete))

	return mux
}
