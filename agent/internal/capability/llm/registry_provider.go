package llm

import (
	"context"
	"database/sql"
	"fmt"
)

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
		apiKey, err := r.cipher.Decrypt(encKey)
		if err != nil {
			return nil, fmt.Errorf("provider: decrypt api key for %q: %w", e.Name, err)
		}
		e.APIKey = apiKey
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
	apiKey, err := r.cipher.Decrypt(encKey)
	if err != nil {
		return nil, fmt.Errorf("provider: decrypt api key for %q: %w", name, err)
	}
	e.APIKey = apiKey
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

	r.mu.Lock()
	delete(r.cache, e.Name)
	r.mu.Unlock()
	return nil
}
