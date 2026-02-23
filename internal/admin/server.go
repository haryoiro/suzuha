package admin

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/haryoiro/suzuha/internal/admin/handler"
	"github.com/haryoiro/suzuha/internal/admin/middleware"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/memory"
)

// Server is the admin dashboard HTTP server.
type Server struct {
	handler http.Handler
	cfg     config.Admin
	logger  *slog.Logger
}

// NewServer creates a new admin Server with all routes configured.
func NewServer(cfg config.Admin, store memory.AdminStore, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	// Health check.
	mux.HandleFunc("GET /api/health", handler.Health)

	// Memories CRUD.
	memH := handler.NewMemoryHandler(store, logger)
	mux.HandleFunc("GET /api/memories", memH.List)
	mux.HandleFunc("POST /api/memories", memH.Create)
	mux.HandleFunc("GET /api/memories/{id}", memH.Get)
	mux.HandleFunc("PUT /api/memories/{id}", memH.Update)
	mux.HandleFunc("DELETE /api/memories/{id}", memH.Delete)
	mux.HandleFunc("GET /api/memories/vec-stats", memH.VecStats)
	mux.HandleFunc("GET /api/memories/with-vec", memH.ListWithVec)

	// Metrics proxy.
	metH := handler.NewMetricsHandler(cfg.AgentMetrics, logger)
	mux.HandleFunc("GET /api/metrics", metH.Proxy)
	mux.HandleFunc("GET /api/metrics/json", metH.ProxyJSON)

	// Users.
	userH := handler.NewUsersHandler(store.DB(), logger)
	mux.HandleFunc("GET /api/users", userH.List)
	mux.HandleFunc("GET /api/users/{id}", userH.Get)
	mux.HandleFunc("GET /api/users/{id}/affinity", userH.AffinityEvents)

	// Agent operations (compact, etc.).
	agentBase := strings.TrimSuffix(cfg.AgentMetrics, "/metrics")
	agentH := handler.NewAgentHandler(agentBase, logger)
	mux.HandleFunc("POST /api/agent/compact", agentH.Compact)

	// Agent context proxy.
	ctxH := handler.NewContextHandler(cfg.AgentContext, logger)
	mux.HandleFunc("GET /api/context", ctxH.Proxy)

	// Log streaming.
	logH := handler.NewLogHandler(cfg.AgentLogs, cfg.ConsolLogs, logger)
	mux.HandleFunc("GET /api/logs/stream", logH.Stream)

	// SPA static files.
	staticDir := cfg.StaticDir
	if staticDir == "" {
		// Try default path relative to working directory.
		staticDir = "web/admin/dist"
	}
	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		mux.Handle("/", spaHandler(staticDir))
		logger.Info("serving SPA", "dir", staticDir)
	} else {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<!doctype html><html><body style="font-family:sans-serif;padding:2em;background:#111;color:#eee">` +
				`<h1>suzuha admin API</h1>` +
				`<p>The frontend is served by Vite dev server at <a href="http://localhost:5173" style="color:#7c3aed">http://localhost:5173</a></p>` +
				`<p>Or build the frontend (<code>cd web/admin &amp;&amp; pnpm run build</code>) to serve it from here.</p>` +
				`<h3>API endpoints</h3><ul>` +
				`<li><a href="/api/health" style="color:#7c3aed">GET /api/health</a></li>` +
				`<li><a href="/api/memories" style="color:#7c3aed">GET /api/memories</a></li>` +
				`<li><a href="/api/users" style="color:#7c3aed">GET /api/users</a></li>` +
				`<li><a href="/api/metrics/json" style="color:#7c3aed">GET /api/metrics/json</a></li>` +
				`<li>GET /api/logs/stream (SSE)</li>` +
				`</ul></body></html>`))
		})
	}

	// Wrap with middleware.
	h := middleware.Logging(logger, mux)
	h = middleware.CORS(h)

	return &Server{handler: h, cfg: cfg, logger: logger}
}

// ListenAndServe starts the admin HTTP server.
func (s *Server) ListenAndServe() error {
	s.logger.Info("suzuha-admin starting", "addr", s.cfg.Addr)
	return http.ListenAndServe(s.cfg.Addr, s.handler)
}

// spaHandler serves a built SPA, falling back to index.html for client-side routing.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, r.URL.Path)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}
