package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/haryoiro/suzuha/internal/user"
)

// UsersHandler provides HTTP handlers for browsing user data.
type UsersHandler struct {
	users  user.AdminStore
	db     *sql.DB // for Memories endpoint (json_extract query)
	logger *slog.Logger
}

// NewUsersHandler creates a new UsersHandler.
func NewUsersHandler(users user.AdminStore, db *sql.DB, logger *slog.Logger) *UsersHandler {
	return &UsersHandler{users: users, db: db, logger: logger}
}

type userJSON struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Role        string         `json:"role"`
	IsBot       bool           `json:"is_bot"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	// Joined from platform_links.
	Platforms []platformLinkJSON `json:"platforms,omitempty"`
}

type platformLinkJSON struct {
	Platform       string `json:"platform"`
	PlatformUserID string `json:"platform_user_id"`
	PlatformName   string `json:"platform_name"`
}

func userToJSON(u user.User) userJSON {
	return userJSON{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		Role:        string(u.Role),
		IsBot:       u.IsBot,
		Metadata:    u.Metadata,
		CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:   u.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}

// List handles GET /api/users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	users, total, err := h.users.List(r.Context(), offset, limit)
	if err != nil {
		h.logger.Error("ユーザー一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	result := make([]userJSON, 0, len(users))
	for _, u := range users {
		uj := userToJSON(u)
		links, _ := h.users.ListPlatformLinks(r.Context(), u.ID)
		for _, l := range links {
			uj.Platforms = append(uj.Platforms, platformLinkJSON{
				Platform:       l.Platform,
				PlatformUserID: l.PlatformUserID,
				PlatformName:   l.PlatformName,
			})
		}
		if uj.Platforms == nil {
			uj.Platforms = []platformLinkJSON{}
		}
		result = append(result, uj)
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": result, "total": total})
}

// Get handles GET /api/users/{id}.
func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	u, err := h.users.Get(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	uj := userToJSON(*u)
	links, _ := h.users.ListPlatformLinks(r.Context(), u.ID)
	for _, l := range links {
		uj.Platforms = append(uj.Platforms, platformLinkJSON{
			Platform:       l.Platform,
			PlatformUserID: l.PlatformUserID,
			PlatformName:   l.PlatformName,
		})
	}
	if uj.Platforms == nil {
		uj.Platforms = []platformLinkJSON{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": uj})
}

// Update handles PUT /api/users/{id}.
func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		IsBot       *bool   `json:"is_bot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	fields := user.UpdateFields{
		DisplayName: body.DisplayName,
		IsBot:       body.IsBot,
	}
	if body.Role != nil {
		switch *body.Role {
		case "owner", "member", "guest":
			role := user.Role(*body.Role)
			fields.Role = &role
		default:
			http.Error(w, `{"error":"invalid role"}`, http.StatusBadRequest)
			return
		}
	}
	if fields.DisplayName == nil && fields.Role == nil && fields.IsBot == nil {
		http.Error(w, `{"error":"no fields to update"}`, http.StatusBadRequest)
		return
	}

	if err := h.users.Update(r.Context(), id, fields); err != nil {
		h.logger.Error("ユーザーの更新に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Guilds handles GET /api/users/{id}/guilds.
func (h *UsersHandler) Guilds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	guilds, err := h.users.GetUserGuilds(r.Context(), id)
	if err != nil {
		h.logger.Error("ユーザーのギルド取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	type entry struct {
		GuildID     string `json:"guild_id"`
		GuildName   string `json:"guild_name"`
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		LastSeenAt  string `json:"last_seen_at"`
	}
	result := make([]entry, 0, len(guilds))
	for _, g := range guilds {
		result = append(result, entry{
			GuildID:     g.GuildID,
			GuildName:   g.GuildName,
			ChannelID:   g.ChannelID,
			ChannelName: g.ChannelName,
			LastSeenAt:  g.LastSeenAt.Format("2006-01-02 15:04:05"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

// Memories handles GET /api/users/{id}/memories.
// Uses raw DB for json_extract query.
func (h *UsersHandler) Memories(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, content, created_at, updated_at FROM memories
		 WHERE type = 'user' AND json_extract(metadata, '$.user_id') = ?
		 ORDER BY updated_at DESC LIMIT ?`, id, limit)
	if err != nil {
		h.logger.Error("ユーザーのメモリ取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type memEntry struct {
		ID        string `json:"id"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	var entries []memEntry
	for rows.Next() {
		var e memEntry
		if err := rows.Scan(&e.ID, &e.Content, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []memEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}
