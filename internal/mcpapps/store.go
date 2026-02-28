package mcpapps

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/haryoiro/suzuha/internal/config"
)

// App represents an installed MCP app persisted in the database.
type App struct {
	Name         string            `json:"name"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Version      string            `json:"version"`
	RegistryType string            `json:"registry_type"`
	Identifier   string            `json:"identifier"`
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	Env          map[string]string `json:"env"`
	Transport    string            `json:"transport"`
	InstalledAt  time.Time         `json:"installed_at"`
	Enabled      bool              `json:"enabled"`
}

// ToToolServer converts a stored App to a config.ToolServer.
func (a *App) ToToolServer() config.ToolServer {
	return config.ToolServer{
		Name:      a.Name,
		Type:      "mcp",
		Transport: a.Transport,
		Command:   a.Command,
		Args:      a.Args,
		Env:       a.Env,
	}
}

// AppStore provides CRUD operations for installed MCP apps.
type AppStore struct {
	db *sql.DB
}

// NewAppStore creates a new AppStore.
func NewAppStore(db *sql.DB) *AppStore {
	return &AppStore{db: db}
}

// Setup creates the mcp_apps table if it doesn't exist.
func (s *AppStore) Setup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS mcp_apps (
			name         TEXT PRIMARY KEY,
			title        TEXT,
			description  TEXT,
			version      TEXT,
			registry_type TEXT NOT NULL,
			identifier   TEXT NOT NULL,
			command      TEXT NOT NULL,
			args         TEXT NOT NULL DEFAULT '[]',
			env          TEXT NOT NULL DEFAULT '{}',
			transport    TEXT NOT NULL DEFAULT 'stdio',
			installed_at DATETIME NOT NULL DEFAULT (datetime('now')),
			enabled      INTEGER NOT NULL DEFAULT 1
		)`)
	return err
}

// Add inserts a new installed app.
func (s *AppStore) Add(ctx context.Context, app *App) error {
	argsJSON, _ := json.Marshal(app.Args)
	envJSON, _ := json.Marshal(app.Env)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mcp_apps (name, title, description, version, registry_type, identifier, command, args, env, transport)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		app.Name, app.Title, app.Description, app.Version,
		app.RegistryType, app.Identifier, app.Command,
		string(argsJSON), string(envJSON), app.Transport,
	)
	return err
}

// Remove deletes an installed app by name.
func (s *AppStore) Remove(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mcp_apps WHERE name = ?`, name)
	return err
}

// List returns all installed apps.
func (s *AppStore) List(ctx context.Context) ([]App, error) {
	return s.query(ctx, `SELECT name, title, description, version, registry_type, identifier, command, args, env, transport, installed_at, enabled FROM mcp_apps ORDER BY installed_at DESC`)
}

// ListEnabled returns all enabled apps.
func (s *AppStore) ListEnabled(ctx context.Context) ([]App, error) {
	return s.query(ctx, `SELECT name, title, description, version, registry_type, identifier, command, args, env, transport, installed_at, enabled FROM mcp_apps WHERE enabled = 1 ORDER BY installed_at DESC`)
}

func (s *AppStore) query(ctx context.Context, q string, args ...any) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []App
	for rows.Next() {
		var app App
		var argsStr, envStr string
		var enabled int

		if err := rows.Scan(
			&app.Name, &app.Title, &app.Description, &app.Version,
			&app.RegistryType, &app.Identifier, &app.Command,
			&argsStr, &envStr, &app.Transport, &app.InstalledAt, &enabled,
		); err != nil {
			return nil, err
		}

		json.Unmarshal([]byte(argsStr), &app.Args)
		if app.Args == nil {
			app.Args = []string{}
		}
		json.Unmarshal([]byte(envStr), &app.Env)
		if app.Env == nil {
			app.Env = make(map[string]string)
		}
		app.Enabled = enabled == 1

		apps = append(apps, app)
	}
	return apps, rows.Err()
}
