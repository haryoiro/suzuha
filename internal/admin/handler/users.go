package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// UsersHandler provides HTTP handlers for browsing user data.
type UsersHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewUsersHandler creates a new UsersHandler.
func NewUsersHandler(db *sql.DB, logger *slog.Logger) *UsersHandler {
	return &UsersHandler{db: db, logger: logger}
}

type userJSON struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Role        string         `json:"role"`
	IsBot       bool           `json:"is_bot"`
	Affinity    float64        `json:"affinity"`
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

type affinityEventJSON struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	Delta     float64 `json:"delta"`
	Reason    string  `json:"reason"`
	CreatedAt string  `json:"created_at"`
}

// List handles GET /api/users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	// Count total.
	var total int
	if err := h.db.QueryRowContext(r.Context(), `SELECT count(*) FROM users`).Scan(&total); err != nil {
		h.logger.Error("count users", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Fetch users.
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, display_name, role, is_bot, affinity, metadata, created_at, updated_at
		 FROM users ORDER BY updated_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		h.logger.Error("list users", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []userJSON
	for rows.Next() {
		var u userJSON
		var metaJSON sql.NullString
		if err := rows.Scan(&u.ID, &u.DisplayName, &u.Role, &u.IsBot, &u.Affinity, &metaJSON, &u.CreatedAt, &u.UpdatedAt); err != nil {
			h.logger.Error("scan user", "error", err)
			continue
		}
		if metaJSON.Valid {
			_ = json.Unmarshal([]byte(metaJSON.String), &u.Metadata)
		}
		users = append(users, u)
	}

	// Fetch platform links for each user.
	for i := range users {
		links, _ := h.getPlatformLinks(r, users[i].ID)
		users[i].Platforms = links
	}

	if users == nil {
		users = []userJSON{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": users, "total": total})
}

// Get handles GET /api/users/{id}.
func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var u userJSON
	var metaJSON sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, display_name, role, is_bot, affinity, metadata, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.DisplayName, &u.Role, &u.IsBot, &u.Affinity, &metaJSON, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if metaJSON.Valid {
		_ = json.Unmarshal([]byte(metaJSON.String), &u.Metadata)
	}

	links, _ := h.getPlatformLinks(r, u.ID)
	u.Platforms = links

	writeJSON(w, http.StatusOK, map[string]any{"data": u})
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

	// Build dynamic UPDATE query.
	var sets []string
	var args []any
	if body.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *body.DisplayName)
	}
	if body.Role != nil {
		switch *body.Role {
		case "owner", "member", "guest":
			sets = append(sets, "role = ?")
			args = append(args, *body.Role)
		default:
			http.Error(w, `{"error":"invalid role"}`, http.StatusBadRequest)
			return
		}
	}
	if body.IsBot != nil {
		sets = append(sets, "is_bot = ?")
		args = append(args, *body.IsBot)
	}
	if len(sets) == 0 {
		http.Error(w, `{"error":"no fields to update"}`, http.StatusBadRequest)
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now())
	args = append(args, id)

	query := "UPDATE users SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += " WHERE id = ?"

	res, err := h.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		h.logger.Error("update user", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AffinityEvents handles GET /api/users/{id}/affinity.
func (h *UsersHandler) AffinityEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, user_id, delta, reason, created_at
		 FROM affinity_events WHERE user_id = ?
		 ORDER BY created_at DESC LIMIT ?`, id, limit)
	if err != nil {
		h.logger.Error("affinity events", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var events []affinityEventJSON
	for rows.Next() {
		var e affinityEventJSON
		if err := rows.Scan(&e.ID, &e.UserID, &e.Delta, &e.Reason, &e.CreatedAt); err != nil {
			continue
		}
		events = append(events, e)
	}
	if events == nil {
		events = []affinityEventJSON{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

// Guilds handles GET /api/users/{id}/guilds.
func (h *UsersHandler) Guilds(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT ugc.guild_id, g.name, ugc.channel_id, ugc.channel_name, ugc.last_seen_at
		 FROM user_guild_channels ugc
		 JOIN guilds g ON g.id = ugc.guild_id
		 WHERE ugc.user_id = ?
		 ORDER BY ugc.last_seen_at DESC`, id)
	if err != nil {
		h.logger.Error("user guilds", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type entry struct {
		GuildID     string `json:"guild_id"`
		GuildName   string `json:"guild_name"`
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		LastSeenAt  string `json:"last_seen_at"`
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.GuildID, &e.GuildName, &e.ChannelID, &e.ChannelName, &e.LastSeenAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

// Memories handles GET /api/users/{id}/memories.
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
		h.logger.Error("user memories", "error", err)
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

func (h *UsersHandler) getPlatformLinks(r *http.Request, userID string) ([]platformLinkJSON, error) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT platform, platform_user_id, platform_name
		 FROM platform_links WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []platformLinkJSON
	for rows.Next() {
		var l platformLinkJSON
		if err := rows.Scan(&l.Platform, &l.PlatformUserID, &l.PlatformName); err != nil {
			continue
		}
		links = append(links, l)
	}
	return links, nil
}
