package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"

	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/samber/do/v2"
)

func registerToolHandlers(mux *http.ServeMux, injector do.Injector, logger *slog.Logger) {
	registry := do.MustInvoke[*tool.Registry](injector)
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")

	// Restore disabled tools on startup.
	{
		var disabledJSON string
		err := llmDB.QueryRow(`SELECT value FROM app_settings WHERE key = 'disabled_tools'`).Scan(&disabledJSON)
		if err == nil && disabledJSON != "" {
			var names []string
			if json.Unmarshal([]byte(disabledJSON), &names) == nil && len(names) > 0 {
				registry.SetDisabled(names)
				logger.Info("restored disabled tools", "count", len(names))
			}
		}
	}

	saveDisabledTools := func() {
		names := registry.DisabledNames()
		data, err := json.Marshal(names)
		if err != nil {
			logger.Error("disabled tools の marshal に失敗", "error", err)
			return
		}
		if _, err := llmDB.Exec(
			`INSERT INTO app_settings (key, value) VALUES ('disabled_tools', $1) ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`,
			string(data),
		); err != nil {
			logger.Error("disabled tools の保存に失敗", "error", err)
		}
	}

	mux.HandleFunc("GET /internal/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := registry.All()
		type toolInfo struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Enabled     bool            `json:"enabled"`
		}
		out := make([]toolInfo, 0, len(tools))
		for _, t := range tools {
			out = append(out, toolInfo{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
				Enabled:     !registry.IsDisabled(t.Name()),
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": out})
	})
	mux.HandleFunc("PUT /internal/tools/{name}/enabled", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		// Update the disabled set.
		current := registry.DisabledNames()
		var updated []string
		if body.Enabled {
			for _, n := range current {
				if n != name {
					updated = append(updated, n)
				}
			}
		} else {
			found := false
			for _, n := range current {
				updated = append(updated, n)
				if n == name {
					found = true
				}
			}
			if !found {
				updated = append(updated, name)
			}
		}
		registry.SetDisabled(updated)
		saveDisabledTools()
		logger.Info("tool toggled", "tool", name, "enabled", body.Enabled)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// Tool execution API.
	mux.HandleFunc("POST /internal/tools/{name}/execute", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		t, ok := registry.Get(name)
		if !ok {
			http.Error(w, `{"error":"tool not found"}`, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if len(body) == 0 {
			body = []byte("{}")
		}
		logger.Info("tool: 手動実行", "tool", name)
		result, err := t.Execute(r.Context(), json.RawMessage(body))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		var text string
		for _, c := range result.Content {
			text += c.Text
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":       !result.IsError,
			"output":   text,
			"is_error": result.IsError,
		})
	})
}
