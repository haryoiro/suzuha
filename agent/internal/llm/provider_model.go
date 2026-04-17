package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

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
		return nil, nil
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
