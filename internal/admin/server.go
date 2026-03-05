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
	mux.HandleFunc("GET /api/memories/duplicates", memH.Duplicates)

	// Metrics (direct SQLite query).
	metH := handler.NewMetricsHandler(store.DB(), logger)
	mux.HandleFunc("GET /api/metrics/json", metH.ServeJSON)

	// Users.
	userH := handler.NewUsersHandler(userStore, store.DB(), logger)
	mux.HandleFunc("GET /api/users", userH.List)
	mux.HandleFunc("GET /api/users/{id}", userH.Get)
	mux.HandleFunc("PUT /api/users/{id}", userH.Update)
	mux.HandleFunc("GET /api/users/{id}/affinity", userH.AffinityEvents)
	mux.HandleFunc("GET /api/users/{id}/guilds", userH.Guilds)
	mux.HandleFunc("GET /api/users/{id}/memories", userH.Memories)

	// Guilds & channels.
	guildH := handler.NewGuildsHandler(userStore, logger)
	mux.HandleFunc("GET /api/guilds", guildH.List)
	mux.HandleFunc("GET /api/channels", guildH.AllChannels)
	mux.HandleFunc("GET /api/guilds/{id}/channels", guildH.Channels)

	// Channel settings.
	agentBase := strings.TrimSuffix(cfg.AgentMetrics, "/metrics")
	chSettingsH := handler.NewChannelSettingsHandler(store.DB(), agentBase, logger)
	mux.HandleFunc("GET /api/channel-settings", chSettingsH.List)
	mux.HandleFunc("PUT /api/channel-settings/{channelId}", chSettingsH.Upsert)
	mux.HandleFunc("DELETE /api/channel-settings/{channelId}", chSettingsH.Delete)

	// Scheduled actions.
	actionsH := handler.NewActionsHandler(schedStore, logger)
	mux.HandleFunc("GET /api/scheduled-actions", actionsH.List)
	mux.HandleFunc("POST /api/scheduled-actions", actionsH.Create)
	mux.HandleFunc("PUT /api/scheduled-actions/{id}", actionsH.Update)
	mux.HandleFunc("DELETE /api/scheduled-actions/{id}", actionsH.Delete)

	// Conversation logs (fine-tuning data).
	convH := handler.NewConversationLogsHandler(store.DB(), logger)
	mux.HandleFunc("GET /api/conversation-logs", convH.List)
	mux.HandleFunc("GET /api/conversation-logs/export", convH.Export)

	// Agent operations (compact, etc.).
	agentH := handler.NewAgentHandler(agentBase, logger)
	mux.HandleFunc("POST /api/agent/compact", agentH.Compact)

	// Boredom status.
	boredomH := handler.NewBoredomHandler(store.DB(), logger)
	mux.HandleFunc("GET /api/boredom", boredomH.Get)

	// Memory deduplication (forget).
	// Trigger API is now served by the agent process (merged from consolidator).
	forgetH := handler.NewForgetHandler(store, agentBase, logger)
	mux.HandleFunc("GET /api/forget/groups", forgetH.Groups)
	mux.HandleFunc("GET /api/forget/status", forgetH.Status)
	mux.HandleFunc("POST /api/forget/delete", forgetH.Delete)
	mux.HandleFunc("POST /api/forget/merge", forgetH.Merge)
	mux.HandleFunc("POST /api/forget/run", forgetH.Run)

	// RSS feeds CRUD.
	rssH := handler.NewRSSHandler(store.DB(), logger)
	mux.HandleFunc("GET /api/feeds", rssH.List)
	mux.HandleFunc("POST /api/feeds", rssH.Create)
	mux.HandleFunc("GET /api/feeds/stats", rssH.Stats)
	mux.HandleFunc("GET /api/feeds/{id}", rssH.Get)
	mux.HandleFunc("PUT /api/feeds/{id}", rssH.Update)
	mux.HandleFunc("DELETE /api/feeds/{id}", rssH.Delete)
	mux.HandleFunc("GET /api/feeds/{id}/items", rssH.ListItems)

	// Location devices & places.
	locH := handler.NewLocationHandler(store.DB(), agentBase, logger)
	mux.HandleFunc("GET /api/location/{userId}", locH.GetLocation)
	mux.HandleFunc("GET /api/location/devices", locH.ListDevices)
	mux.HandleFunc("PUT /api/location/devices/{id}", locH.UpsertDevice)
	mux.HandleFunc("DELETE /api/location/devices/{id}", locH.DeleteDevice)
	mux.HandleFunc("GET /api/location/places", locH.ListPlaces)
	mux.HandleFunc("POST /api/location/places", locH.CreatePlace)
	mux.HandleFunc("PUT /api/location/places/{id}", locH.UpdatePlace)
	mux.HandleFunc("DELETE /api/location/places/{id}", locH.DeletePlace)

	// Tools (proxy to agent).
	toolsH := handler.NewToolsHandler(agentBase, logger)
	mux.HandleFunc("GET /api/tools", toolsH.List)
	mux.HandleFunc("PUT /api/tools/{name}/enabled", toolsH.ToggleTool)

	// LLM provider (proxy to agent).
	llmH := handler.NewLLMHandler(agentBase, logger)
	mux.HandleFunc("GET /api/llm", llmH.Get)
	mux.HandleFunc("PUT /api/llm", llmH.Put)

	// Prompt files.
	promptH := handler.NewPromptHandler(cfg.PromptDir, agentBase, logger)
	mux.HandleFunc("GET /api/prompts", promptH.List)
	mux.HandleFunc("GET /api/prompts/{name}", promptH.Get)
	mux.HandleFunc("PUT /api/prompts/{name}", promptH.Update)

	// Agent context proxy.
	ctxH := handler.NewContextHandler(cfg.AgentContext, logger)
	mux.HandleFunc("GET /api/context", ctxH.Proxy)

	// Playground proxy (chat with LLM using agent context snapshot).
	playH := handler.NewPlaygroundHandler(agentBase, logger)
	mux.HandleFunc("POST /api/playground", playH.Send)

	// Identity proxy (bot's own identity).
	identityH := handler.NewIdentityHandler(agentBase, logger)
	mux.HandleFunc("GET /api/identity", identityH.Get)

	// Log streaming (consolidator logs are now part of agent).
	logH := handler.NewLogHandler(cfg.AgentLogs, "", logger)
	mux.HandleFunc("GET /api/logs/stream", logH.Stream)

	// SPA static files.
	staticDir := cfg.StaticDir
	if staticDir == "" {
		// Try default path relative to working directory.
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
