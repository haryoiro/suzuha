// Package llmtext は LLM 出力テキストの純粋な整形・判定ヘルパを提供する。
//
// LLM client 実装 (capability/llm) を触らず呼べるプリミティブに限定する。
// runtime/agent や behavior/ が port/llm を経由せずに直接使える。
package llmtext

import "strings"

// directiveTags は agent 内部で制御に使う tag。LLM 出力に残してはいけない。
var directiveTags = []string{"[RESPOND]", "[LISTEN]", "[SKIP]"}

// StripDirectiveTags は LLM 出力から agent 制御タグを除去する。
// チャットに送るテキストは必ず本関数で洗っておくこと。
func StripDirectiveTags(text string) string {
	for _, tag := range directiveTags {
		text = strings.ReplaceAll(text, tag, "")
	}
	return strings.TrimSpace(text)
}

// IsSilentResponse は LLM が応答しないことを選んだ (空または [SKIP] を含む) 場合 true。
func IsSilentResponse(text string) bool {
	return text == "" || strings.Contains(strings.ToUpper(text), "[SKIP]")
}
