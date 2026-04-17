package llm

import (
	"context"
	"fmt"

	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
)

// AssignRole はロールにプロバイダ/モデルを割り当てる。
func (r *ProviderRegistry) AssignRole(ctx context.Context, role, providerName, modelID string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO llm_role_assignments (role, preset, provider_name, model_id)
		 VALUES ($1, '', $2, $3)
		 ON CONFLICT(role) DO UPDATE SET
		   provider_name = excluded.provider_name,
		   model_id = excluded.model_id`,
		role, providerName, modelID)
	if err != nil {
		return fmt.Errorf("role: assign: %w", err)
	}
	return nil
}

// Assignments は全ロール割り当てを返す。
func (r *ProviderRegistry) Assignments(ctx context.Context) ([]RoleAssignment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT role, provider_name, model_id FROM llm_role_assignments WHERE provider_name != ''`)
	if err != nil {
		return nil, fmt.Errorf("role: assignments: %w", err)
	}
	defer rows.Close()

	var out []RoleAssignment
	for rows.Next() {
		var a RoleAssignment
		if err := rows.Scan(&a.Role, &a.ProviderName, &a.ModelID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveRole はロール割り当てから RoleSpec を組み立てる。
// モデルがカタログにない場合はデフォルト capabilities=["text"], maxContext=0 で返す。
func (r *ProviderRegistry) ResolveRole(ctx context.Context, role string) (*RoleSpec, error) {
	assignments, err := r.Assignments(ctx)
	if err != nil {
		return nil, err
	}

	var assignment *RoleAssignment
	for _, fallback := range roleFallback(role) {
		for i := range assignments {
			if assignments[i].Role == fallback {
				assignment = &assignments[i]
				break
			}
		}
		if assignment != nil {
			break
		}
	}
	if assignment == nil {
		return nil, fmt.Errorf("role: %q に割り当てがありません", role)
	}

	return r.buildRoleSpec(ctx, assignment.ProviderName, assignment.ModelID)
}

// BuildRoleSpec はプロバイダ名+モデルIDから RoleSpec を組み立てる。
func (r *ProviderRegistry) BuildRoleSpec(ctx context.Context, providerName, modelID string) (*RoleSpec, error) {
	return r.buildRoleSpec(ctx, providerName, modelID)
}

func (r *ProviderRegistry) buildRoleSpec(ctx context.Context, providerName, modelID string) (*RoleSpec, error) {
	entry, err := r.GetProvider(ctx, providerName)
	if err != nil {
		return nil, err
	}

	inst, err := r.resolveProviderInstance(entry)
	if err != nil {
		return nil, err
	}

	model, err := r.GetModel(ctx, providerName, modelID)
	if err != nil {
		return nil, err
	}

	spec := &RoleSpec{
		ProviderInst: inst,
		ProviderName: providerName,
		ProviderType: entry.Type,
		ModelID:      modelID,
		APIBase:      entry.APIBase,
	}
	if model != nil {
		spec.Capabilities = model.Capabilities
		spec.MaxContext = model.MaxContext
	} else {
		spec.Capabilities = []string{"text"}
		r.logger.Warn("model: カタログにないモデル、デフォルト capabilities を使用",
			"provider", providerName, "model", modelID)
	}
	return spec, nil
}

// resolveProviderInstance はキャッシュ済みの Provider インスタンスを返す。
func (r *ProviderRegistry) resolveProviderInstance(entry *ProviderEntry) (providers.Provider, error) {
	r.mu.RLock()
	if cached, ok := r.cache[entry.Name]; ok {
		r.mu.RUnlock()
		return cached.provider, nil
	}
	r.mu.RUnlock()

	inst, err := newProviderInstance(entry.Type, entry.APIKey, entry.APIBase)
	if err != nil {
		return nil, fmt.Errorf("provider: %q のインスタンス作成に失敗: %w", entry.Name, err)
	}

	r.mu.Lock()
	r.cache[entry.Name] = cachedProvider{entry: *entry, provider: inst}
	r.mu.Unlock()
	return inst, nil
}

// newProviderInstance は any-llm-go の Provider を作成する。
func newProviderInstance(providerType, apiKey, apiBase string) (providers.Provider, error) {
	opts := []anyllm.Option{anyllm.WithAPIKey(apiKey)}
	if apiBase != "" {
		opts = append(opts, anyllm.WithBaseURL(apiBase))
	}
	switch providerType {
	case "openai", "zhipu", "qwen":
		return openai.New(opts...)
	case "gemini":
		return gemini.New([]anyllm.Option{anyllm.WithAPIKey(apiKey)}...)
	default:
		return nil, fmt.Errorf("未対応のプロバイダタイプ %q", providerType)
	}
}

func roleFallback(role string) []string {
	chain := []string{role}
	if role != "background" && role != "conversation" {
		chain = append(chain, "background")
	}
	if role != "conversation" {
		chain = append(chain, "conversation")
	}
	return chain
}
