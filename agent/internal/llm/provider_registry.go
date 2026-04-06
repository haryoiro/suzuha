package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/lib/crypto"
	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
)

// ProviderEntry はプロバイダ接続情報。
type ProviderEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`              // "openai", "zhipu", "gemini", "qwen"
	APIKey  string `json:"api_key,omitempty"` // メモリ上は平文
	APIBase string `json:"api_base"`
	Source  string `json:"source"` // "seed" or "user"
}

// ModelInfo はモデルカタログのエントリ。
type ModelInfo struct {
	ProviderName string   `json:"provider_name"`
	ModelID      string   `json:"model_id"`
	Capabilities []string `json:"capabilities"` // ["text"], ["text","vision"]
	MaxContext   int      `json:"max_context"`
	Source       string   `json:"source"` // "static", "api", "user"
}

// HasCapability はモデルが指定の capability を持つか返す。
func (m *ModelInfo) HasCapability(cap string) bool {
	return slices.Contains(m.Capabilities, cap)
}

// RoleAssignment はロールへのプロバイダ/モデル割り当て。
type RoleAssignment struct {
	Role         string `json:"role"`
	ProviderName string `json:"provider_name"`
	ModelID      string `json:"model_id"`
}

// RoleSpec は Client.SwapRole に渡す解決済みのロール仕様。
type RoleSpec struct {
	ProviderInst providers.Provider
	ProviderName string
	ModelID      string
	APIBase      string
	MaxContext   int
	Capabilities []string
}

// ProviderRegistry はプロバイダ・モデル・ロール割り当てを管理する。
type ProviderRegistry struct {
	db     *sql.DB
	cipher *crypto.AESGCMCipher
	logger *slog.Logger

	mu    sync.RWMutex
	cache map[string]cachedProvider // provider name → cached instance
}

type cachedProvider struct {
	entry    ProviderEntry
	provider providers.Provider
}

// NewProviderRegistry は ProviderRegistry を作成する。
func NewProviderRegistry(db *sql.DB, cipher *crypto.AESGCMCipher, logger *slog.Logger) *ProviderRegistry {
	return &ProviderRegistry{
		db:     db,
		cipher: cipher,
		logger: logger,
		cache:  make(map[string]cachedProvider),
	}
}

// --- Provider CRUD ---

// ListProviders は全プロバイダを返す。
func (r *ProviderRegistry) ListProviders(ctx context.Context) ([]ProviderEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, type, api_key, api_base, source FROM llm_providers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("provider: list: %w", err)
	}
	defer rows.Close()

	var out []ProviderEntry
	for rows.Next() {
		var e ProviderEntry
		var encKey string
		if err := rows.Scan(&e.Name, &e.Type, &encKey, &e.APIBase, &e.Source); err != nil {
			return nil, fmt.Errorf("provider: scan: %w", err)
		}
		e.APIKey, _ = r.cipher.Decrypt(encKey)
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetProvider は名前でプロバイダを取得する。
func (r *ProviderRegistry) GetProvider(ctx context.Context, name string) (*ProviderEntry, error) {
	var e ProviderEntry
	var encKey string
	err := r.db.QueryRowContext(ctx,
		`SELECT name, type, api_key, api_base, source FROM llm_providers WHERE name = $1`, name).
		Scan(&e.Name, &e.Type, &encKey, &e.APIBase, &e.Source)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("provider: %q が見つかりません", name)
	}
	if err != nil {
		return nil, fmt.Errorf("provider: get: %w", err)
	}
	e.APIKey, _ = r.cipher.Decrypt(encKey)
	return &e, nil
}

// SaveProvider はプロバイダを作成・更新する (upsert)。
func (r *ProviderRegistry) SaveProvider(ctx context.Context, e *ProviderEntry) error {
	encKey := ""
	if e.APIKey != "" {
		var err error
		encKey, err = r.cipher.Encrypt(e.APIKey)
		if err != nil {
			return fmt.Errorf("provider: API キーの暗号化に失敗: %w", err)
		}
	}
	source := e.Source
	if source == "" {
		source = "user"
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO llm_providers (name, type, api_key, api_base, source, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT(name) DO UPDATE SET
		   type = excluded.type,
		   api_key = CASE WHEN excluded.api_key = '' THEN llm_providers.api_key ELSE excluded.api_key END,
		   api_base = excluded.api_base,
		   source = excluded.source,
		   updated_at = now()`,
		e.Name, e.Type, encKey, e.APIBase, source)
	if err != nil {
		return fmt.Errorf("provider: save: %w", err)
	}

	// Invalidate cache.
	r.mu.Lock()
	delete(r.cache, e.Name)
	r.mu.Unlock()
	return nil
}

// --- Model Catalog ---

// ListModels はモデルカタログを返す。providerName が空なら全プロバイダ。
func (r *ProviderRegistry) ListModels(ctx context.Context, providerName string) ([]ModelInfo, error) {
	var rows *sql.Rows
	var err error
	if providerName != "" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT provider_name, model_id, capabilities, max_context, source
			 FROM llm_model_catalog WHERE provider_name = $1 ORDER BY model_id`, providerName)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT provider_name, model_id, capabilities, max_context, source
			 FROM llm_model_catalog ORDER BY provider_name, model_id`)
	}
	if err != nil {
		return nil, fmt.Errorf("model: list: %w", err)
	}
	defer rows.Close()

	var out []ModelInfo
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetModel はプロバイダ/モデルIDでカタログエントリを取得する。
func (r *ProviderRegistry) GetModel(ctx context.Context, providerName, modelID string) (*ModelInfo, error) {
	var m ModelInfo
	var capsJSON string
	err := r.db.QueryRowContext(ctx,
		`SELECT provider_name, model_id, capabilities, max_context, source
		 FROM llm_model_catalog WHERE provider_name = $1 AND model_id = $2`,
		providerName, modelID).Scan(&m.ProviderName, &m.ModelID, &capsJSON, &m.MaxContext, &m.Source)
	if err == sql.ErrNoRows {
		return nil, nil // カタログにないモデルは nil を返す (エラーではない)
	}
	if err != nil {
		return nil, fmt.Errorf("model: get: %w", err)
	}
	parseCaps(capsJSON, &m)
	return &m, nil
}

// SaveModel はモデルカタログにエントリを追加・更新する。
func (r *ProviderRegistry) SaveModel(ctx context.Context, m *ModelInfo) error {
	capsJSON, _ := json.Marshal(m.Capabilities)
	source := m.Source
	if source == "" {
		source = "user"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO llm_model_catalog (provider_name, model_id, capabilities, max_context, source)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT(provider_name, model_id) DO UPDATE SET
		   capabilities = excluded.capabilities,
		   max_context = excluded.max_context,
		   source = excluded.source`,
		m.ProviderName, m.ModelID, string(capsJSON), m.MaxContext, source)
	if err != nil {
		return fmt.Errorf("model: save: %w", err)
	}
	return nil
}

// --- Role Assignments ---

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

// --- Resolution ---

// ResolveRole はロール割り当てから RoleSpec を組み立てる。
// モデルがカタログにない場合はデフォルト capabilities=["text"], maxContext=0 で返す。
func (r *ProviderRegistry) ResolveRole(ctx context.Context, role string) (*RoleSpec, error) {
	assignments, err := r.Assignments(ctx)
	if err != nil {
		return nil, err
	}

	// フォールバック: role → "background" → "conversation"
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

	// モデルカタログから capabilities と max_context を取得
	model, err := r.GetModel(ctx, providerName, modelID)
	if err != nil {
		return nil, err
	}

	spec := &RoleSpec{
		ProviderInst: inst,
		ProviderName: providerName,
		ModelID:      modelID,
		APIBase:      entry.APIBase,
	}
	if model != nil {
		spec.Capabilities = model.Capabilities
		spec.MaxContext = model.MaxContext
	} else {
		// カタログにないモデル → デフォルト
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

// --- Seeding ---

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

// --- Helpers ---

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

func scanModel(rows *sql.Rows) (ModelInfo, error) {
	var m ModelInfo
	var capsJSON string
	if err := rows.Scan(&m.ProviderName, &m.ModelID, &capsJSON, &m.MaxContext, &m.Source); err != nil {
		return m, fmt.Errorf("model: scan: %w", err)
	}
	parseCaps(capsJSON, &m)
	return m, nil
}

func parseCaps(capsJSON string, m *ModelInfo) {
	if capsJSON != "" {
		if err := json.Unmarshal([]byte(capsJSON), &m.Capabilities); err != nil {
			m.Capabilities = []string{"text"}
		}
	} else {
		m.Capabilities = []string{"text"}
	}
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
