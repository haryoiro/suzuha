// Package llm は LLM capability の契約を定義する。
//
// 現状 port 化しているのは Admin (ProviderRegistry 管理) のみ。
// agent pipeline が使う Client interface (Complete / Embed / DescribeImage 等) は
// `message.Message` が providers.ToolCall を含むため、外部 SDK 型を domain に
// 落とし込む段取りを終えてから追加予定。
package llm

import (
	"context"

	"github.com/haryoiro/suzuha/internal/domain/llm"
)

// Admin は ProviderRegistry の管理操作を公開する contract。
// admin API / control API から使う。
type Admin interface {
	ListProviders(ctx context.Context) ([]llm.ProviderEntry, error)
	GetProvider(ctx context.Context, name string) (*llm.ProviderEntry, error)
	SaveProvider(ctx context.Context, e *llm.ProviderEntry) error

	ListModels(ctx context.Context, providerName string) ([]llm.ModelInfo, error)
	GetModel(ctx context.Context, providerName, modelID string) (*llm.ModelInfo, error)
	SaveModel(ctx context.Context, m *llm.ModelInfo) error

	AssignRole(ctx context.Context, role, providerName, modelID string) error
	Assignments(ctx context.Context) ([]llm.RoleAssignment, error)
}
