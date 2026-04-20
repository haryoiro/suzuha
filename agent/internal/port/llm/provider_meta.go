package llm

import (
	"context"

	domainllm "github.com/haryoiro/suzuha/internal/domain/llm"
)

// ProviderMeta はプロバイダごとのモデルカタログ取得を抽象化する契約。
// adapter/llm/{gemini,openai,zhipu} 等の vendor パッケージが実装する。
type ProviderMeta interface {
	ListModels(ctx context.Context, apiKey, apiBase string) ([]domainllm.ModelInfo, error)
}
