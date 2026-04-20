package llm

import "github.com/mozilla-ai/any-llm-go/providers"

// StreamChunk は streaming レスポンスの 1 チャンク。
// port 層で公開することで runtime/agent が capability/llm の具象を知らずに
// ストリームを受け取れる。
type StreamChunk struct {
	// Content は表示可能なテキスト差分 (<think> タグ外の内容)。
	Content string
	// Reasoning は推論内容の差分 (<think> タグ内、または Reasoning フィールド)。
	Reasoning string
	// ToolCalls はストリーム完了時に蓄積されたツール呼び出し。Done == true のときのみ有効。
	ToolCalls []providers.ToolCall
	// Done はストリーム完了を示す。
	Done bool
	// FinishReason はストリーム完了時の終了理由。
	FinishReason string
	// Usage はストリーム完了時のトークン使用量 (nil の場合あり)。
	Usage *providers.Usage
}
