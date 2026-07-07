package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/auth"
	"github.com/synamcps/synamcps-server/internal/config"
	"github.com/synamcps/synamcps-server/internal/httpapi"
	"github.com/synamcps/synamcps-server/internal/knowledge"
	"github.com/synamcps/synamcps-server/internal/knowledge/ingest"
	"github.com/synamcps/synamcps-server/internal/llm"
	"github.com/synamcps/synamcps-server/internal/mcp"
	"github.com/synamcps/synamcps-server/internal/mcpproxy"
	"github.com/synamcps/synamcps-server/internal/observability"
	"github.com/synamcps/synamcps-server/internal/secrets"
	"github.com/synamcps/synamcps-server/internal/session"
	"github.com/synamcps/synamcps-server/internal/storage/blob"
	metapg "github.com/synamcps/synamcps-server/internal/storage/meta/postgres"
	"github.com/synamcps/synamcps-server/internal/storage/migrate"
	"github.com/synamcps/synamcps-server/internal/storage/pgconn"
	"github.com/synamcps/synamcps-server/internal/storage/vector"
	"github.com/synamcps/synamcps-server/internal/storage/vector/pgvector"
	"github.com/synamcps/synamcps-server/internal/storage/vector/qdrant"
	"github.com/synamcps/synamcps-server/internal/transport/legacysse"
	"github.com/synamcps/synamcps-server/internal/transport/streamablehttp"
	"github.com/synamcps/synamcps-server/internal/usage"
	"github.com/synamcps/synamcps-server/internal/web"
)

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.example.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	warnInsecureJWKS(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessions := session.NewStore(cfg.Redis)
	gateway := auth.NewGateway(cfg)

	var pgPool *pgxpool.Pool
	if cfg.Metadata.DSN != "" {
		pgPool, err = pgconn.NewPool(ctx, cfg.Metadata)
		if err != nil {
			log.Fatalf("init postgres pool: %v", err)
		}
		if err := migrate.Up(pgPool, ""); err != nil {
			log.Fatalf("apply migrations: %v", err)
		}
	}

	accessStore, err := initAccessStore(ctx, pgPool, cfg.Metadata.DSN)
	if err != nil {
		log.Fatalf("init access store: %v", err)
	}

	var mcpStore *mcpproxy.Store
	var mcpManager *mcpproxy.Manager
	var mcpAccess *mcpproxy.AccessService
	accessOpts := []access.ServiceOption{}
	if cfg.MCPProxy.Enabled {
		cipher, err := secrets.NewCipher(cfg.MCPProxySecretsKey())
		if err != nil {
			log.Printf("mcp proxy secrets disabled: %v", err)
		} else {
			mcpStore, err = initMCPStore(ctx, pgPool, cfg.Metadata.DSN, cipher)
			if err != nil {
				log.Fatalf("init mcp proxy store: %v", err)
			}
			mcpManager = mcpproxy.NewManager(cfg.MCPProxy, mcpStore)
			mcpAccess = mcpproxy.NewAccessService(mcpStore)
			accessOpts = append(accessOpts, access.WithMCPScopeLoader(mcpStore))
		}
	}
	accessService := access.NewService(accessStore, accessOpts...)
	gateway.SetOpaqueTokenResolver(accessService)

	usageService := usage.NewService(cfg.Redis, cfg.Usage)
	if cfg.Usage.Exporters.VictoriaMetrics.Enabled {
		usageService.StartVictoriaMetricsExporter(ctx, cfg.Usage.Exporters.VictoriaMetrics.RemoteWriteURL, time.Duration(cfg.Usage.Exporters.VictoriaMetrics.IntervalSeconds)*time.Second)
	}

	catalog, err := initCatalog(ctx, pgPool, cfg.Metadata.DSN)
	if err != nil {
		log.Fatalf("init metadata catalog: %v", err)
	}
	blobStore, err := blob.NewStore(cfg)
	if err != nil {
		log.Fatalf("init s3 store: %v", err)
	}
	var vec vector.Store
	if cfg.VectorBackend.Active == "qdrant" {
		vec, err = qdrant.New(cfg.VectorBackend.QdrantURL, cfg.VectorBackend.QdrantCollection)
		if err != nil {
			log.Fatalf("init qdrant store: %v", err)
		}
	} else {
		vecStore, err := initVectorStore(ctx, pgPool, cfg.Metadata.DSN)
		if err != nil {
			log.Fatalf("init pgvector store: %v", err)
		}
		vec = vecStore
	}

	summarizer := llm.NewSimpleSummarizer(cfg.Summarization)
	embedder := llm.NewSimpleEmbeddingProvider(cfg.Embedding)
	jobStore := ingest.NewJobStore(ctx, pgPool)
	pipeline := ingest.NewPipeline(cfg, summarizer, embedder, vec, catalog, blobStore, jobStore)
	ingestWorker := ingest.NewWorker(pipeline, jobStore, ingest.WorkerConfig{})
	ingestWorker.Start(ctx)
	knowledgeService, err := knowledge.NewService(catalog, vec, pipeline, accessService, cfg.S3.Bucket, ingestWorker)
	if err != nil {
		log.Fatalf("init knowledge service: %v", err)
	}

	rootMux := http.NewServeMux()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	metrics := observability.NewHTTPMetrics()
	metrics.AttachUsageExporter(usageService)
	rootMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	rootMux.Handle("/metrics", metrics.Handler())
	rootMux.Handle("/readyz", observability.ReadyzHandler(logger, map[string]observability.Pinger{
		"redis":    sessions,
		"metadata": catalog,
		"blob":     blobStore,
	}))
	statusHandler := httpapi.NewStatusHandler(cfg, usageService, catalog, sessions, blobStore)
	apiRouter := httpapi.NewRouterWithAdmin(gateway, sessions, knowledgeService, accessService, usageService, cfg.S3.Bucket, cfg.Search.Filters.SourceURL.AllowPartialMatch, statusHandler, mcpStore, mcpManager, mcpAccess, cfg.Limits.MaxUploadBytes)
	rootMux.Handle("/api/", apiRouter)
	rootMux.HandleFunc("/api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"transports": map[string]bool{
				"streamable_http": cfg.Transport.StreamableHTTP,
				"legacy_sse":      cfg.Transport.LegacySSE,
			},
			"auth_methods": buildAuthMethods(cfg),
		})
	})
	rootMux.Handle("/.well-known/oauth-protected-resource", gateway.ProtectedResourceMetadataHandler())
	rootMux.Handle("/.well-known/oauth-authorization-server", gateway.AuthorizationServerMetadataHandler())

	if cfg.Web.EnableUI {
		rootMux.Handle("/", web.NewHandler(cfg, sessions, accessService))
	}

	mcpServer := mcp.NewServer(mcp.ServerDeps{
		Sessions:  sessions,
		Knowledge: knowledgeService,
		Access:    accessService,
		Usage:     usageService,
		Proxy:     mcpManager,
		MCPAccess: mcpAccess,
	})
	if cfg.Transport.StreamableHTTP {
		streamablehttp.NewHandler(mcpServer, gateway, sessions).Register(rootMux)
	}
	if cfg.Transport.LegacySSE {
		legacysse.NewHandler().Register(rootMux)
	}

	server := &http.Server{
		Addr:              cfg.Server.ListenAddr,
		Handler:           metrics.Middleware(logger, withMiddlewares(rootMux, cfg)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("server listening on %s", cfg.Server.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	ingestWorker.Shutdown()
	sessions.EvictExpired()
	_ = sessions.Close()
	_ = server.Shutdown(shutdownCtx)
	if pgPool != nil {
		pgPool.Close()
	}
}

func initAccessStore(ctx context.Context, pool *pgxpool.Pool, dsn string) (*access.Store, error) {
	if pool != nil {
		return access.NewStoreWithPool(ctx, pool)
	}
	return access.NewStore(ctx, dsn)
}

func initCatalog(ctx context.Context, pool *pgxpool.Pool, dsn string) (*metapg.Store, error) {
	if pool != nil {
		return metapg.NewWithPool(ctx, pool)
	}
	return metapg.New(ctx, dsn)
}

func initVectorStore(ctx context.Context, pool *pgxpool.Pool, dsn string) (*pgvector.Store, error) {
	if pool != nil {
		return pgvector.NewWithPool(ctx, pool)
	}
	return pgvector.New(ctx, dsn)
}

func initMCPStore(ctx context.Context, pool *pgxpool.Pool, dsn string, cipher *secrets.Cipher) (*mcpproxy.Store, error) {
	if pool != nil {
		return mcpproxy.NewStoreWithPool(ctx, pool, cipher)
	}
	return mcpproxy.NewStore(ctx, dsn, cipher)
}

func warnInsecureJWKS(cfg config.Config) {
	if !cfg.Server.DevMode {
		return
	}
	for _, p := range cfg.OAuth.Providers {
		if p.JWKSURL == "insecure" {
			log.Printf("WARNING: dev_mode enabled with jwks_url=insecure for provider %q — JWT signatures are NOT verified", p.Name)
		}
	}
}

func buildAuthMethods(cfg config.Config) []string {
	out := make([]string, 0, len(cfg.OAuth.Providers)+1)
	for _, p := range cfg.OAuth.Providers {
		out = append(out, p.Name)
	}
	if cfg.Teleport.Enabled {
		out = append(out, "teleport_proxy")
	}
	return out
}

func withMiddlewares(next http.Handler, cfg config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !isWebRoute(r.URL.Path) && !isAllowedOrigin(origin, r.Host, cfg.API.AllowedOrigins) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isWebRoute(path string) bool {
	return path == "/" || path == "/login" || path == "/logout" || strings.HasPrefix(path, "/app")
}

func isAllowedOrigin(origin, host string, allowlist []string) bool {
	if origin == "" {
		return true
	}
	if origin == "http://"+host || origin == "https://"+host {
		return true
	}
	originURL, err := url.Parse(origin)
	if err == nil {
		if sameHost(originURL.Host, host) || isLoopbackHost(originURL.Hostname()) {
			return true
		}
	}
	for _, allowed := range allowlist {
		if allowed == "*" {
			return true
		}
		if allowed == origin {
			return true
		}
	}
	return false
}

func sameHost(a, b string) bool {
	aHost, aPort := splitHostPort(a)
	bHost, bPort := splitHostPort(b)
	return aHost != "" && aHost == bHost && aPort == bPort
}

func splitHostPort(value string) (string, string) {
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		return host, port
	}
	return value, ""
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
