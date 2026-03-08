package admin

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/admin/handler"
	"github.com/haryoiro/suzuha/internal/admin/middleware"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/schedule"
	"github.com/haryoiro/suzuha/internal/user"
)

// Server is the admin dashboard HTTP server.
type Server struct {
	handler http.Handler
	cfg     config.Admin
	logger  *slog.Logger
}

// NewServer creates a new admin Server with all routes configured.
func NewServer(cfg config.Admin, store memory.AdminStore, userStore user.AdminStore, schedStore *schedule.Store, logger *slog.Logger) *Server {
	agentBase := strings.TrimSuffix(cfg.AgentMetrics, "/metrics")

	// Create the ogen handler implementation.
	adminHandler := NewAdminHandler(store, userStore, schedStore, agentBase, cfg.PromptDir, logger)

	// Create the ogen server (handles all typed API routes).
	ogenServer, err := api.NewServer(adminHandler)
	if err != nil {
		logger.Error("ogen サーバーの作成に失敗", "error", err.Error())
		panic(err)
	}

	mux := http.NewServeMux()

	// Mount ogen server for /api/ routes.
	mux.Handle("/api/", ogenServer)

	// SSE log streaming (not in OpenAPI spec, handled separately).
	logH := handler.NewLogHandler(cfg.AgentLogs, "", logger)
	mux.HandleFunc("GET /api/logs/stream", logH.Stream)

	// SPA static files.
	staticDir := cfg.StaticDir
	if staticDir == "" {
		staticDir = "web/admin/dist"
	}
	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		mux.Handle("/", spaHandler(staticDir))
		logger.Info("SPAを配信中", "dir", staticDir)
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
	s.logger.Info("suzuha-admin を起動します", "addr", s.cfg.Addr)
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
