package llmtoken

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

// TokenCounter はテキストのトークン数を返す関数。
type TokenCounter func(text string) int

// tiktokenCache はモデルごとの tiktoken.Tiktoken をキャッシュする。
var (
	tiktokenMu    sync.RWMutex
	tiktokenCache = map[string]*tiktoken.Tiktoken{}
)

func getTiktoken(model string) *tiktoken.Tiktoken {
	tiktokenMu.RLock()
	if enc, ok := tiktokenCache[model]; ok {
		tiktokenMu.RUnlock()
		return enc
	}
	tiktokenMu.RUnlock()

	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// モデル名が tiktoken に登録されていない場合は cl100k_base にフォールバック。
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil
		}
	}

	tiktokenMu.Lock()
	tiktokenCache[model] = enc
	tiktokenMu.Unlock()
	return enc
}

// NewTokenCounter はプロバイダタイプとモデル名からトークンカウンタを生成する。
// OpenAI 互換プロバイダは tiktoken、それ以外はヒューリスティックにフォールバック。
func NewTokenCounter(providerType, model string, logger *slog.Logger) TokenCounter {
	switch providerType {
	case "openai":
		enc := getTiktoken(model)
		if enc != nil {
			logger.Info("トークンカウンタを設定", "type", "tiktoken", "provider", providerType, "model", model)
			return func(text string) int {
				return len(enc.Encode(text, nil, nil))
			}
		}
	case "zhipu", "qwen":
		// ZhiPu/Qwen は独自トークナイザーだが Go 実装がないため
		// cl100k_base で近似（OpenAI 互換 API 形式）。
		enc := getTiktoken("gpt-4")
		if enc != nil {
			logger.Info("トークンカウンタを設定", "type", "tiktoken-approx", "provider", providerType, "model", model)
			return func(text string) int {
				return len(enc.Encode(text, nil, nil))
			}
		}
	case "gemini":
		// Gemini はローカルトークナイザーが Go にないためヒューリスティック。
		logger.Info("トークンカウンタを設定", "type", "heuristic", "provider", providerType, "model", model)
	}

	// フォールバック: Unicode ヒューリスティック
	return func(text string) int {
		return textutil.EstimateTokens(text)
	}
}

// CountMessages はメッセージ列のトークン数を合計する。
// メッセージごとに role overhead (+4) を加算する。
func CountMessages(counter TokenCounter, msgs []message.Message) int {
	total := 0
	for _, m := range msgs {
		total += counter(m.Content) + 4
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				total += counter(tc.Function.Name) + counter(tc.Function.Arguments)
			}
		}
	}
	return total
}

// ProviderTypeFromName は provider name が未知の場合にも使える簡易判定。
func ProviderTypeFromName(providerName string) string {
	lower := strings.ToLower(providerName)
	switch {
	case strings.Contains(lower, "openai"):
		return "openai"
	case strings.Contains(lower, "zhipu"), strings.Contains(lower, "glm"):
		return "zhipu"
	case strings.Contains(lower, "gemini"), strings.Contains(lower, "google"):
		return "gemini"
	case strings.Contains(lower, "qwen"):
		return "qwen"
	default:
		return ""
	}
}
