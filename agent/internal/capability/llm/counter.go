package llm

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

// tiktokenCache はモデルごとの tiktoken.Tiktoken をキャッシュする。
// capability 内部に閉じた状態で、外部に露出しない。
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

// NewTokenCounterFactory は provider type / model に応じた TokenCounter を返す factory を生成する。
//
// OpenAI 互換は tiktoken、ZhiPu / Qwen は cl100k_base 近似、Gemini と未知の
// provider はヒューリスティック。
func NewTokenCounterFactory(logger *slog.Logger) portllm.TokenCounterFactory {
	return func(providerType, model string) message.TokenCounter {
		switch providerType {
		case "openai":
			if enc := getTiktoken(model); enc != nil {
				logger.Info("トークンカウンタを設定", "type", "tiktoken", "provider", providerType, "model", model)
				return func(text string) int {
					return len(enc.Encode(text, nil, nil))
				}
			}
		case "zhipu", "qwen":
			if enc := getTiktoken("gpt-4"); enc != nil {
				logger.Info("トークンカウンタを設定", "type", "tiktoken-approx", "provider", providerType, "model", model)
				return func(text string) int {
					return len(enc.Encode(text, nil, nil))
				}
			}
		case "gemini":
			logger.Info("トークンカウンタを設定", "type", "heuristic", "provider", providerType, "model", model)
		}
		return func(text string) int {
			return textutil.EstimateTokens(text)
		}
	}
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
