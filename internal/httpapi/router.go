package httpapi

import (
	"net/http"

	"github.com/zmiishe/synamcps/internal/access"
	"github.com/zmiishe/synamcps/internal/auth"
	"github.com/zmiishe/synamcps/internal/knowledge"
	"github.com/zmiishe/synamcps/internal/mcpproxy"
	"github.com/zmiishe/synamcps/internal/session"
	"github.com/zmiishe/synamcps/internal/usage"
)

func NewRouter(gateway *auth.Gateway, sessions *session.Store, service *knowledge.Service, allowPartial bool) http.Handler {
	return NewRouterWithAdmin(gateway, sessions, service, nil, nil, "", allowPartial, nil, nil, nil, nil)
}

func NewRouterWithAdmin(gateway *auth.Gateway, sessions *session.Store, service *knowledge.Service, accessService *access.Service, usageService *usage.Service, s3Bucket string, allowPartial bool, statusHandler *StatusHandler, mcpStore *mcpproxy.Store, mcpManager *mcpproxy.Manager, mcpAccess *mcpproxy.AccessService) http.Handler {
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
	mux.Handle("GET /api/knowledge", authResolver.Middleware(http.HandlerFunc(handler.List)))
	mux.Handle("POST /api/knowledge", authResolver.Middleware(http.HandlerFunc(handler.Create("api"))))
	mux.Handle("POST /api/admin/knowledge", authResolver.Middleware(http.HandlerFunc(handler.Create("admin"))))
	mux.Handle("POST /api/knowledge/ingest/file", authResolver.Middleware(http.HandlerFunc(ingestHandler.IngestFile)))
	mux.Handle("POST /api/knowledge/ingest/link", authResolver.Middleware(http.HandlerFunc(ingestHandler.IngestLink)))
	mux.Handle("POST /api/knowledge/search", authResolver.Middleware(http.HandlerFunc(handler.Search)))
	mux.Handle("GET /api/knowledge/", authResolver.Middleware(http.HandlerFunc(handler.Get)))
	mux.Handle("DELETE /api/knowledge/", authResolver.Middleware(http.HandlerFunc(handler.Delete)))

	return mux
}
