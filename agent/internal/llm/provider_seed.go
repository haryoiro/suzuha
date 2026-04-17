package llm

import (
	"context"
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

// SeedStaticModels は登録済みプロバイダの静的モデルカタログを DB に反映する。
// 各プロバイダタイプの ProviderMeta から静的リストを取得し、SeedModels で upsert する。
func (r *ProviderRegistry) SeedStaticModels(ctx context.Context) error {
	providers, err := r.ListProviders(ctx)
	if err != nil {
		return fmt.Errorf("model: seed: プロバイダ一覧取得に失敗: %w", err)
	}
	seenTypes := make(map[string]bool)
	for _, p := range providers {
		if seenTypes[p.Type] {
			continue
		}
		seenTypes[p.Type] = true

		meta := GetProviderMeta(p.Type)
		if meta == nil {
			continue
		}
		models, err := meta.ListModels(ctx, p.APIKey, p.APIBase)
		if err != nil {
			r.logger.Warn("model: 静的モデル取得に失敗", "type", p.Type, "error", err)
			continue
		}
		for i := range models {
			models[i].ProviderName = p.Name
		}
		if err := r.SeedModels(ctx, models); err != nil {
			return err
		}
		r.logger.Info("model: 静的カタログをシード", "type", p.Type, "count", len(models))
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
