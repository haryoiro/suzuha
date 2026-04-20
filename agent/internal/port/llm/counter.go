package llm

import "github.com/haryoiro/suzuha/internal/domain/message"

// TokenCounterFactory は provider type と model ID からトークンカウンタを生成する関数。
//
// capability/llm が provider 固有の tokenizer (OpenAI tiktoken、ヒューリスティック等) を
// 束ねて返す。runtime/agent は role swap 時にここから新 counter を取得する。
type TokenCounterFactory func(providerType, model string) message.TokenCounter
