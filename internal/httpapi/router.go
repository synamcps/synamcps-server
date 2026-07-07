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

type RouterDeps struct {
	Gateway               *auth.Gateway
	Sessions              *session.Store
	Knowledge             *knowledge.Service
	Access                *access.Service
	Usage                 *usage.Service
	S3Bucket              string
	AllowPartialSourceURL bool
	Status                *StatusHandler
	MCPStore              *mcpproxy.Store
	MCPManager            *mcpproxy.Manager
	MCPAccess             *mcpproxy.AccessService
	MaxBodyBytes          int64
}

func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()
	authResolver := NewAuthResolver(deps.Gateway, deps.Sessions)
	handler := NewKnowledgeHandler(deps.Knowledge, deps.AllowPartialSourceURL)

	if deps.Access != nil {
		admin := NewAdminHandler(deps.Access, deps.Usage, deps.S3Bucket)
		admin.AttachMCP(deps.MCPStore, deps.MCPManager, deps.MCPAccess)
		mux.Handle("/api/admin/", authResolver.Middleware(admin))
		if deps.Status != nil {
			mux.Handle("GET /api/admin/status", authResolver.Middleware(deps.Status))
		}
	}
	ingestHandler := NewIngestHandler(deps.Knowledge)
	guard := func(h http.HandlerFunc) http.Handler {
		return maxBodyMiddleware(deps.MaxBodyBytes, authResolver.Middleware(rateLimitMiddleware(deps.Usage, h)))
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
