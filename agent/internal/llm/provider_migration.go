package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/config"
)

// SeedProviders は config.yaml のプロバイダ定義を DB にシードする。
func (r *ProviderRegistry) SeedProviders(ctx context.Context, cfgProviders []config.LLMProvider) error {
	for _, cp := range cfgProviders {
		existing, err := r.GetProvider(ctx, cp.Name)
		if err == nil && existing.Source == "user" {
			r.logger.Debug("provider: ユーザー定義をスキップ", "name", cp.Name)
			continue
		}
		e := &ProviderEntry{
			Name:    cp.Name,
			Type:    cp.Type,
			APIKey:  cp.APIKey,
			APIBase: cp.APIBase,
			Source:  "seed",
		}
		if err := r.SaveProvider(ctx, e); err != nil {
			return fmt.Errorf("provider: seed %q に失敗: %w", cp.Name, err)
		}
	}
	return nil
}

// SeedModels はモデルカタログにエントリをシードする (source="static" のみ上書き)。
func (r *ProviderRegistry) SeedModels(ctx context.Context, models []ModelInfo) error {
	for _, m := range models {
		existing, err := r.GetModel(ctx, m.ProviderName, m.ModelID)
		if err != nil {
			return err
		}
		if existing != nil && existing.Source == "user" {
			continue
		}
		m.Source = "static"
		if err := r.SaveModel(ctx, &m); err != nil {
			return fmt.Errorf("model: seed %q/%q に失敗: %w", m.ProviderName, m.ModelID, err)
		}
	}
	return nil
}

// MigrateFromPresets は旧 llm_presets テーブルから新テーブルにデータを移行する。
// ロール割り当てが既に新形式 (provider_name が空でない) なら完了済みとみなしスキップ。
func (r *ProviderRegistry) MigrateFromPresets(ctx context.Context) error {
	// 新形式のロール割り当てが既にあるならスキップ
	var migratedCount int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_role_assignments WHERE provider_name != ''`).Scan(&migratedCount)
	if migratedCount > 0 {
		return nil
	}

	// 旧テーブルが存在するか確認
	var tableExists int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_name='llm_presets'`).Scan(&tableExists)
	if tableExists == 0 {
		return nil
	}

	// 旧プリセットを読み込み
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, provider, model, api_key, api_base, max_tokens, capabilities, source FROM llm_presets`)
	if err != nil {
		return fmt.Errorf("migrate: 旧プリセットの読み込みに失敗: %w", err)
	}
	defer rows.Close()

	type oldPreset struct {
		name, provider, model, encKey, apiBase, capsJSON, source string
		maxTokens                                                int
	}
	var presets []oldPreset
	for rows.Next() {
		var p oldPreset
		if err := rows.Scan(&p.name, &p.provider, &p.model, &p.encKey, &p.apiBase, &p.maxTokens, &p.capsJSON, &p.source); err != nil {
			return fmt.Errorf("migrate: スキャン失敗: %w", err)
		}
		presets = append(presets, p)
	}
	if len(presets) == 0 {
		return nil
	}

	r.logger.Info("migrate: 旧プリセットから移行開始", "count", len(presets))

	// 1. 固有プロバイダを抽出 (provider+api_base の組み合わせでユニーク化)
	type provKey struct{ provider, apiBase string }
	seenProviders := make(map[provKey]bool)
	for _, p := range presets {
		key := provKey{p.provider, p.apiBase}
		if seenProviders[key] {
			continue
		}
		seenProviders[key] = true

		// プロバイダ名: provider type をそのまま使うか、api_base で区別
		provName := p.provider
		if p.apiBase != "" && p.apiBase != "https://api.openai.com/v1" && p.apiBase != "https://open.bigmodel.cn/api/coding/paas/v4" {
			provName = p.name // local-qwen のような名前をプロバイダ名にする
		}

		// API キーは旧テーブルから暗号化済みのままコピー
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO llm_providers (name, type, api_key, api_base, source)
			 VALUES ($1, $2, $3, $4, 'seed')
			 ON CONFLICT(name) DO NOTHING`,
			provName, p.provider, p.encKey, p.apiBase)
		if err != nil {
			r.logger.Warn("migrate: プロバイダ挿入失敗", "name", provName, "error", err)
		}
	}

	// 2. モデルカタログに登録
	for _, p := range presets {
		var caps []string
		if p.capsJSON != "" {
			json.Unmarshal([]byte(p.capsJSON), &caps)
		}
		if len(caps) == 0 {
			caps = []string{"text"}
		}
		capsBytes, _ := json.Marshal(caps)

		// プロバイダ名を解決
		provName := p.provider
		var exists int
		r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_providers WHERE name = $1`, provName).Scan(&exists)
		if exists == 0 {
			provName = p.name
		}

		_, err := r.db.ExecContext(ctx,
			`INSERT INTO llm_model_catalog (provider_name, model_id, capabilities, max_context, source)
			 VALUES ($1, $2, $3, $4, 'static')
			 ON CONFLICT(provider_name, model_id) DO NOTHING`,
			provName, p.model, string(capsBytes), p.maxTokens)
		if err != nil {
			r.logger.Warn("migrate: モデル挿入失敗", "model", p.model, "error", err)
		}
	}

	// 3. ロール割り当てを移行
	roleRows, err := r.db.QueryContext(ctx, `SELECT role, preset FROM llm_role_assignments WHERE provider_name = ''`)
	if err == nil {
		defer roleRows.Close()
		for roleRows.Next() {
			var role, presetName string
			if err := roleRows.Scan(&role, &presetName); err != nil {
				continue
			}
			// 旧プリセット名からプロバイダとモデルを解決
			for _, p := range presets {
				if p.name == presetName {
					provName := p.provider
					var exists int
					r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_providers WHERE name = $1`, provName).Scan(&exists)
					if exists == 0 {
						provName = p.name
					}
					r.db.ExecContext(ctx,
						`UPDATE llm_role_assignments SET provider_name = $1, model_id = $2 WHERE role = $3`,
						provName, p.model, role)
					r.logger.Info("migrate: ロール移行", "role", role, "provider", provName, "model", p.model)
					break
				}
			}
		}
	}

	r.logger.Info("migrate: 旧プリセットからの移行完了")
	return nil
}
