package llm

import (
	"database/sql"
	"log/slog"
	"sync"

	"github.com/mozilla-ai/any-llm-go/providers"

	domain "github.com/haryoiro/suzuha/internal/domain/llm"
	"github.com/haryoiro/suzuha/internal/lib/crypto"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// domain/llm への型エイリアス。既存の `llm.ProviderEntry` 等の参照を温存する。
type (
	ProviderEntry  = domain.ProviderEntry
	ModelInfo      = domain.ModelInfo
	RoleAssignment = domain.RoleAssignment
)

// ProviderRegistry はプロバイダ・モデル・ロール割り当てを管理する。
//
// 責務はファイル分割:
//   - provider_registry.go — 型定義 / 構築
//   - registry_provider.go — Provider CRUD (List / Get / Save)
//   - registry_model.go    — Model CRUD (List / Get / Save) + scan / parse helpers
//   - registry_role.go     — Role 割り当て + RoleSpec 解決 + Provider instance cache
//   - registry_seed.go     — Seed (SeedProviders / SeedStaticModels / SeedModels) + Migrate
type ProviderRegistry struct {
	db     *sql.DB
	cipher *crypto.AESGCMCipher
	logger *slog.Logger

	// metas はプロバイダタイプ名 ("openai", "zhipu", "gemini", "qwen") →
	// モデルカタログ取得実装。DI で注入される。
	metas map[string]portllm.ProviderMeta

	mu    sync.RWMutex
	cache map[string]cachedProvider // provider name → cached instance
}

type cachedProvider struct {
	entry    ProviderEntry
	provider providers.Provider
}

// NewProviderRegistry は ProviderRegistry を作成する。
func NewProviderRegistry(db *sql.DB, cipher *crypto.AESGCMCipher, metas map[string]portllm.ProviderMeta, logger *slog.Logger) *ProviderRegistry {
	return &ProviderRegistry{
		db:     db,
		cipher: cipher,
		logger: logger,
		metas:  metas,
		cache:  make(map[string]cachedProvider),
	}
}

// ProviderMeta は指定プロバイダタイプに対応する ProviderMeta を返す。
func (r *ProviderRegistry) ProviderMeta(providerType string) portllm.ProviderMeta {
	return r.metas[providerType]
}
