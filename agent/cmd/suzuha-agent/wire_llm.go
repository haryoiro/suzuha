package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/samber/do/v2"
)

func registerLLMHandlers(mux *http.ServeMux, injector do.Injector, ag *agent.Agent, logger *slog.Logger) {
	llmClient := do.MustInvoke[*llm.Client](injector)
	providerRegistry := do.MustInvoke[*llm.ProviderRegistry](injector)

	// Restore role assignments from DB on startup.
	{
		assignments, err := providerRegistry.Assignments(context.Background())
		if err == nil {
			for _, a := range assignments {
				spec, err := providerRegistry.BuildRoleSpec(context.Background(), a.ProviderName, a.ModelID)
				if err != nil {
					logger.Warn("ロール復元: RoleSpec 構築失敗", "role", a.Role, "provider", a.ProviderName, "model", a.ModelID, "error", err)
					continue
				}
				llmClient.SwapRoleSpec(a.Role, *spec)
				if a.Role == "conversation" && spec.MaxContext > 0 {
					ag.AgentContext().SetMaxTokens(spec.MaxContext)
				}
				logger.Info("LLMロールを復元", "role", a.Role, "provider", a.ProviderName, "model", a.ModelID)
			}
		}
	}

	// GET /internal/llm — ステータス概要
	mux.HandleFunc("GET /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		prov, model, apiBase, vision := llmClient.ProviderInfo()
		assignments, _ := providerRegistry.Assignments(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"provider":    prov,
			"model":       model,
			"api_base":    apiBase,
			"max_ctx":     llmClient.MaxContextTokens(),
			"vision":      vision,
			"assignments": assignments,
		})
	})

	mux.HandleFunc("GET /internal/llm/providers", func(w http.ResponseWriter, r *http.Request) {
		providers, err := providerRegistry.ListProviders(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(providers)
	})

	mux.HandleFunc("GET /internal/llm/models", func(w http.ResponseWriter, r *http.Request) {
		providerFilter := r.URL.Query().Get("provider")
		models, err := providerRegistry.ListModels(r.Context(), providerFilter)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)
	})

	mux.HandleFunc("POST /internal/llm/models", func(w http.ResponseWriter, r *http.Request) {
		var m llm.ModelInfo
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if m.ProviderName == "" || m.ModelID == "" {
			http.Error(w, `{"error":"provider_name and model_id required"}`, http.StatusBadRequest)
			return
		}
		if len(m.Capabilities) == 0 {
			m.Capabilities = []string{"text"}
		}
		if err := providerRegistry.SaveModel(r.Context(), &m); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	mux.HandleFunc("POST /internal/llm/models/refresh", func(w http.ResponseWriter, r *http.Request) {
		providers, err := providerRegistry.ListProviders(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		var total int
		for _, p := range providers {
			meta := llm.GetProviderMeta(p.Type)
			if meta == nil {
				continue
			}
			models, err := meta.ListModels(r.Context(), p.APIKey, p.APIBase)
			if err != nil {
				logger.Warn("モデルカタログ更新失敗", "provider", p.Name, "error", err)
				continue
			}
			for i := range models {
				models[i].ProviderName = p.Name
				if err := providerRegistry.SaveModel(r.Context(), &models[i]); err != nil {
					logger.Warn("モデル保存失敗", "provider", p.Name, "model", models[i].ModelID, "error", err)
				} else {
					total++
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "models_updated": total})
	})

	mux.HandleFunc("GET /internal/llm/roles", func(w http.ResponseWriter, r *http.Request) {
		assignments, err := providerRegistry.Assignments(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(assignments)
	})

	mux.HandleFunc("PUT /internal/llm/roles/{role}", func(w http.ResponseWriter, r *http.Request) {
		role := r.PathValue("role")
		var body struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" || body.Model == "" {
			http.Error(w, `{"error":"provider and model required"}`, http.StatusBadRequest)
			return
		}

		spec, err := providerRegistry.BuildRoleSpec(r.Context(), body.Provider, body.Model)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}

		if err := providerRegistry.AssignRole(r.Context(), role, body.Provider, body.Model); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		llmClient.SwapRoleSpec(role, *spec)

		if role == "conversation" && spec.MaxContext > 0 {
			ag.AgentContext().SetMaxTokens(spec.MaxContext)
			if ag.AgentContext().UsageRatio() > 0.5 {
				compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				ag.ForceCompact(compactCtx)
				compactCancel()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
}
