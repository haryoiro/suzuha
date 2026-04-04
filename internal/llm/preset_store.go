package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/lib/crypto"
)

// Preset は LLM プロバイダのプリセット定義。
type Preset struct {
	Name         string   `json:"name"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	APIKey       string   `json:"api_key,omitempty"` // メモリ上は平文、DB は暗号化
	APIBase      string   `json:"api_base"`
	MaxTokens    int      `json:"max_tokens"`
	Capabilities []string `json:"capabilities"`
	Source       string   `json:"source"` // "seed" or "user"
}

// HasCapability はプリセットが指定の capability を持つか返す。
func (p *Preset) HasCapability(cap string) bool {
	return slices.Contains(p.Capabilities, cap)
}

// ResolveResult はモダリティ解決の結果。
type ResolveResult struct {
	Preset Preset
	Inline bool // true=ネイティブ対応 (会話モデルに含む), false=パイプライン (別モデル)
}

// PresetStore はプリセットの CRUD とロール割り当てを管理する。
type PresetStore struct {
	db     *sql.DB
	cipher *crypto.AESGCMCipher
	logger *slog.Logger
}

// NewPresetStore は PresetStore を作成する。
func NewPresetStore(db *sql.DB, cipher *crypto.AESGCMCipher, logger *slog.Logger) *PresetStore {
	return &PresetStore{db: db, cipher: cipher, logger: logger}
}

// List は全プリセットを返す。
func (s *PresetStore) List(ctx context.Context) ([]Preset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, provider, model, api_key, api_base, max_tokens, capabilities, source FROM llm_presets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("preset: list: %w", err)
	}
	defer rows.Close()

	var presets []Preset
	for rows.Next() {
		p, err := s.scanPreset(rows)
		if err != nil {
			return nil, err
		}
		presets = append(presets, p)
	}
	return presets, rows.Err()
}

// Get は名前でプリセットを取得する。
func (s *PresetStore) Get(ctx context.Context, name string) (*Preset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, provider, model, api_key, api_base, max_tokens, capabilities, source FROM llm_presets WHERE name = ?`, name)
	p, err := s.scanPresetRow(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("preset: %q が見つかりません", name)
	}
	if err != nil {
		return nil, fmt.Errorf("preset: get: %w", err)
	}
	return &p, nil
}

// Save はプリセットを作成・更新する (upsert)。
func (s *PresetStore) Save(ctx context.Context, p *Preset) error {
	capsJSON, err := json.Marshal(p.Capabilities)
	if err != nil {
		return fmt.Errorf("preset: capabilities の JSON 変換に失敗: %w", err)
	}

	// api_key が空なら既存値を保持する。
	encKey := ""
	if p.APIKey != "" {
		var err error
		encKey, err = s.cipher.Encrypt(p.APIKey)
		if err != nil {
			return fmt.Errorf("preset: API キーの暗号化に失敗: %w", err)
		}
	}

	source := p.Source
	if source == "" {
		source = "user"
	}

	// api_key が空の場合は既存値を維持する (COALESCE + NULLIF)。
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO llm_presets (name, provider, model, api_key, api_base, max_tokens, capabilities, source, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(name) DO UPDATE SET
		   provider = excluded.provider,
		   model = excluded.model,
		   api_key = CASE WHEN excluded.api_key = '' THEN llm_presets.api_key ELSE excluded.api_key END,
		   api_base = excluded.api_base,
		   max_tokens = excluded.max_tokens,
		   capabilities = excluded.capabilities,
		   source = excluded.source,
		   updated_at = datetime('now')`,
		p.Name, p.Provider, p.Model, encKey, p.APIBase, p.MaxTokens, string(capsJSON), source)
	if err != nil {
		return fmt.Errorf("preset: save: %w", err)
	}
	return nil
}

// Delete はプリセットを削除する。
func (s *PresetStore) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM llm_presets WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("preset: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("preset: %q が見つかりません", name)
	}
	return nil
}

// Assign はロールにプリセットを割り当てる。
func (s *PresetStore) Assign(ctx context.Context, role, presetName string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO llm_role_assignments (role, preset) VALUES (?, ?)
		 ON CONFLICT(role) DO UPDATE SET preset = excluded.preset`,
		role, presetName)
	if err != nil {
		return fmt.Errorf("preset: assign: %w", err)
	}
	return nil
}

// Unassign はロールの割り当てを削除する。
func (s *PresetStore) Unassign(ctx context.Context, role string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM llm_role_assignments WHERE role = ?`, role)
	if err != nil {
		return fmt.Errorf("preset: unassign: %w", err)
	}
	return nil
}

// Assignments は全ロール割り当てを返す。
func (s *PresetStore) Assignments(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role, preset FROM llm_role_assignments`)
	if err != nil {
		return nil, fmt.Errorf("preset: assignments: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var role, preset string
		if err := rows.Scan(&role, &preset); err != nil {
			return nil, err
		}
		result[role] = preset
	}
	return result, rows.Err()
}

// Resolve はロールと必要な capability からプリセットを解決する。
//
// フォールバック順:
//  1. role のプリセットが capability を持つ → Inline=true
//  2. capability 名のロール (e.g. "vision") のプリセット → Inline=false
//  3. role → "background" → "conversation" のフォールバック (capability 空の場合)
func (s *PresetStore) Resolve(ctx context.Context, role, capability string) (*ResolveResult, error) {
	assignments, err := s.Assignments(ctx)
	if err != nil {
		return nil, err
	}

	// ロールのプリセットを取得
	preset, err := s.resolveRole(ctx, role, assignments)
	if err != nil {
		return nil, err
	}

	// capability が空ならそのまま返す
	if capability == "" {
		return &ResolveResult{Preset: *preset, Inline: true}, nil
	}

	// ロールのプリセットが capability を持つ → inline
	if preset.HasCapability(capability) {
		return &ResolveResult{Preset: *preset, Inline: true}, nil
	}

	// capability 名のロールにフォールバック (e.g. "vision" ロール)
	if capPresetName, ok := assignments[capability]; ok {
		capPreset, err := s.Get(ctx, capPresetName)
		if err == nil && capPreset.HasCapability(capability) {
			return &ResolveResult{Preset: *capPreset, Inline: false}, nil
		}
	}

	return nil, fmt.Errorf("preset: capability %q を満たすプリセットが role=%q に見つかりません", capability, role)
}

// resolveRole はフォールバック付きでロールのプリセットを取得する。
// フォールバック: role → "background" → "conversation"
func (s *PresetStore) resolveRole(ctx context.Context, role string, assignments map[string]string) (*Preset, error) {
	fallback := []string{role}
	if role != "background" && role != "conversation" {
		fallback = append(fallback, "background")
	}
	if role != "conversation" {
		fallback = append(fallback, "conversation")
	}

	for _, r := range fallback {
		if presetName, ok := assignments[r]; ok {
			p, err := s.Get(ctx, presetName)
			if err == nil {
				return p, nil
			}
		}
	}
	return nil, fmt.Errorf("preset: role %q に割り当てられたプリセットがありません", role)
}

// SeedDefaults は Seed 時のデフォルトロール割り当て。
type SeedDefaults struct {
	Conversation string // conversation + background のデフォルトプリセット名
	Vision       string // vision ロールのプリセット名 (空ならスキップ)
	Embedding    string // embedding ロールのプリセット名 (空ならスキップ)
}

// Seed は config.yaml のプリセットを DB にシードする。
// source='seed' のプリセットは上書き、source='user' は保持。
// 既存の app_settings.llm_provider があれば移行する。
func (s *PresetStore) Seed(ctx context.Context, cfgPresets []config.LLMPreset, defaults SeedDefaults) error {
	// config プリセットをシード
	for _, cp := range cfgPresets {
		caps := []string{"text"}
		if cp.Vision {
			caps = append(caps, "vision")
		}
		p := &Preset{
			Name:         cp.Name,
			Provider:     cp.Provider,
			Model:        cp.Model,
			APIKey:       cp.APIKey,
			APIBase:      cp.APIBase,
			MaxTokens:    cp.MaxTokens,
			Capabilities: caps,
			Source:       "seed",
		}

		// source='user' のプリセットは上書きしない
		existing, err := s.Get(ctx, cp.Name)
		if err == nil && existing.Source == "user" {
			s.logger.Debug("preset: ユーザープリセットをスキップ", "name", cp.Name)
			continue
		}

		if err := s.Save(ctx, p); err != nil {
			return fmt.Errorf("preset: seed %q に失敗: %w", cp.Name, err)
		}
	}

	// デフォルトロール割り当て (未割当の場合のみ)
	assignments, err := s.Assignments(ctx)
	if err != nil {
		return err
	}

	seedAssign := func(role, preset string) {
		if preset == "" {
			return
		}
		if _, ok := assignments[role]; ok {
			return // 既に割り当て済み
		}
		if err := s.Assign(ctx, role, preset); err != nil {
			s.logger.Warn("preset: デフォルト割り当てに失敗", "role", role, "preset", preset, "error", err)
		}
	}

	seedAssign("conversation", defaults.Conversation)
	seedAssign("background", defaults.Conversation) // background も同じデフォルト
	seedAssign("vision", defaults.Vision)
	seedAssign("embedding", defaults.Embedding)

	// 旧 app_settings.llm_provider からの移行
	s.migrateAppSettings(ctx, assignments)

	return nil
}

// migrateAppSettings は旧 app_settings の llm_provider を移行する。
func (s *PresetStore) migrateAppSettings(ctx context.Context, currentAssignments map[string]string) {
	var savedJSON string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'llm_provider'`).Scan(&savedJSON)
	if err != nil || savedJSON == "" {
		return
	}

	var saved struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
		APIBase  string `json:"api_base"`
		MaxCtx   int    `json:"max_ctx"`
		Vision   bool   `json:"vision"`
	}
	if json.Unmarshal([]byte(savedJSON), &saved) != nil || saved.Provider == "" {
		return
	}

	// 既存プリセットから一致するものを探す
	presets, _ := s.List(ctx)
	var matchName string
	for _, p := range presets {
		if p.Provider == saved.Provider && p.Model == saved.Model && p.APIBase == saved.APIBase {
			matchName = p.Name
			break
		}
	}

	// 見つからなければ新しいプリセットとして保存
	if matchName == "" {
		matchName = fmt.Sprintf("%s-%s", saved.Provider, sanitizeName(saved.Model))
		caps := []string{"text"}
		if saved.Vision {
			caps = append(caps, "vision")
		}
		p := &Preset{
			Name:         matchName,
			Provider:     saved.Provider,
			Model:        saved.Model,
			APIKey:       saved.APIKey,
			APIBase:      saved.APIBase,
			MaxTokens:    saved.MaxCtx,
			Capabilities: caps,
			Source:       "seed",
		}
		if err := s.Save(ctx, p); err != nil {
			s.logger.Warn("preset: app_settings 移行に失敗", "error", err)
			return
		}
	}

	// conversation ロールに割り当て (未割当の場合のみ)
	if _, ok := currentAssignments["conversation"]; !ok {
		s.Assign(ctx, "conversation", matchName)
	}

	// 旧エントリを削除
	s.db.ExecContext(ctx, `DELETE FROM app_settings WHERE key = 'llm_provider'`)
	s.logger.Info("preset: app_settings.llm_provider を移行完了", "preset", matchName)
}

// sanitizeName はモデル名をプリセット名に使えるように正規化する。
func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// --- internal helpers ---

type scanner interface {
	Scan(dest ...any) error
}

func (s *PresetStore) scanPreset(rows *sql.Rows) (Preset, error) {
	var p Preset
	var encKey, capsJSON string
	if err := rows.Scan(&p.Name, &p.Provider, &p.Model, &encKey, &p.APIBase, &p.MaxTokens, &capsJSON, &p.Source); err != nil {
		return p, fmt.Errorf("preset: scan: %w", err)
	}
	return s.finishScan(p, encKey, capsJSON)
}

func (s *PresetStore) scanPresetRow(row *sql.Row) (Preset, error) {
	var p Preset
	var encKey, capsJSON string
	if err := row.Scan(&p.Name, &p.Provider, &p.Model, &encKey, &p.APIBase, &p.MaxTokens, &capsJSON, &p.Source); err != nil {
		return p, err
	}
	return s.finishScan(p, encKey, capsJSON)
}

func (s *PresetStore) finishScan(p Preset, encKey, capsJSON string) (Preset, error) {
	key, err := s.cipher.Decrypt(encKey)
	if err != nil {
		s.logger.Warn("preset: API キーの復号に失敗、空文字として扱う", "name", p.Name, "error", err)
		key = ""
	}
	p.APIKey = key

	if capsJSON != "" {
		if err := json.Unmarshal([]byte(capsJSON), &p.Capabilities); err != nil {
			p.Capabilities = []string{"text"}
		}
	} else {
		p.Capabilities = []string{"text"}
	}
	return p, nil
}
