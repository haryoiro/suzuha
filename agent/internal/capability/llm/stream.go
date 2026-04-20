package llm

import (
	"context"
	"fmt"
	"strings"

	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// CompleteStreamWithTools は streaming 補完を実行し、チャンクチャネルを返す。
// Content にはユーザーに見せるテキストのみが含まれる (<think> タグはフィルタ済み)。
// エラーチャネルは最大 1 つのエラーを送信してクローズされる。
func (rc *RoleClient) CompleteStreamWithTools(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.Tool,
) (<-chan portllm.StreamChunk, <-chan error) {
	out := make(chan portllm.StreamChunk)
	errs := make(chan error, 1)

	rp := rc.resolve()
	if rp.provider == nil {
		errs <- fmt.Errorf("llm: ロール %q にプロバイダが設定されていません", rc.role)
		close(out)
		close(errs)
		return out, errs
	}

	params := providers.CompletionParams{
		Model:    rp.model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}

	chunks, provErrs := rp.provider.CompletionStream(ctx, params)

	go func() {
		defer close(out)
		defer close(errs)

		acc := &streamAccumulator{}

		for chunk := range chunks {
			sc := acc.process(chunk)
			if sc == nil {
				continue
			}
			select {
			case out <- *sc:
			case <-ctx.Done():
				return
			}
		}

		// Provider error check.
		if err, ok := <-provErrs; ok && err != nil {
			errs <- fmt.Errorf("llm: streaming 補完に失敗: %w", err)
			return
		}

		// Send final chunk with accumulated state.
		final := acc.finalize()
		select {
		case out <- final:
		case <-ctx.Done():
		}
	}()

	return out, errs
}

// streamAccumulator はストリーミングチャンクの状態を蓄積する。
type streamAccumulator struct {
	// <think> タグのフィルタ状態
	inThink bool
	tagBuf  string // 部分タグ候補 (例: "<thi", "</thi")
	// 蓄積
	reasoning    strings.Builder
	content      strings.Builder
	toolCalls    []providers.ToolCall
	finishReason string
	usage        *providers.Usage
}

// process は provider チャンクを処理し、送信すべき portllm.StreamChunk を返す。
// コンテンツがない場合は nil を返す。
func (a *streamAccumulator) process(chunk providers.ChatCompletionChunk) *portllm.StreamChunk {
	if len(chunk.Choices) == 0 {
		// Usage のみのチャンク (stream_options.include_usage で最後に来る)。
		if chunk.Usage != nil {
			a.usage = chunk.Usage
		}
		return nil
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	// FinishReason の記録。
	if choice.FinishReason != "" {
		a.finishReason = choice.FinishReason
	}

	// ToolCall delta の蓄積。
	for _, tc := range delta.ToolCalls {
		a.accumulateToolCall(tc)
	}

	// Reasoning フィールド (ライブラリがサポートしている場合)。
	var reasoningDelta string
	if delta.Reasoning != nil && delta.Reasoning.Content != "" {
		reasoningDelta = delta.Reasoning.Content
		a.reasoning.WriteString(reasoningDelta)
	}

	// Content の <think> タグフィルタ。
	contentDelta := a.filterThinkTags(delta.Content)

	if contentDelta == "" && reasoningDelta == "" {
		return nil
	}

	return &portllm.StreamChunk{
		Content:   contentDelta,
		Reasoning: reasoningDelta,
	}
}

// finalize はストリーム完了チャンクを返す。
// tagBuf に残っている未確定テキストも content として出力する。
func (a *streamAccumulator) finalize() portllm.StreamChunk {
	// ストリーム終了時に未確定の部分タグが残っていれば
	// タグではなかったので content に出力する。
	if a.tagBuf != "" {
		if a.inThink {
			a.reasoning.WriteString(a.tagBuf)
		} else {
			a.content.WriteString(a.tagBuf)
		}
		a.tagBuf = ""
	}
	return portllm.StreamChunk{
		Content:      a.content.String(),
		Done:         true,
		ToolCalls:    a.toolCalls,
		FinishReason: a.finishReason,
		Usage:        a.usage,
	}
}

// filterThinkTags は Content 内の <think>...</think> タグをフィルタし、
// タグ外のテキストのみを返す。状態は呼び出し間で保持される。
// 部分タグ (例: "<thi" が 1 チャンク、"nk>" が次のチャンク) を正しく処理するため、
// タグ候補を tagBuf にバッファリングする。
func (a *streamAccumulator) filterThinkTags(content string) string {
	if content == "" {
		return ""
	}

	// 前回のバッファと結合して処理する。
	content = a.tagBuf + content
	a.tagBuf = ""

	var out strings.Builder
	for i := 0; i < len(content); {
		if a.inThink {
			closeIdx := strings.Index(content[i:], "</think>")
			if closeIdx >= 0 {
				a.reasoning.WriteString(content[i : i+closeIdx])
				a.inThink = false
				i += closeIdx + len("</think>")
				continue
			}
			// "</think>" の部分一致をチェック。
			if tail := partialTagSuffix(content[i:], "</think>"); tail != "" {
				a.reasoning.WriteString(content[i : len(content)-len(tail)])
				a.tagBuf = tail
				break
			}
			a.reasoning.WriteString(content[i:])
			break
		}

		openIdx := strings.Index(content[i:], "<think>")
		if openIdx >= 0 {
			out.WriteString(content[i : i+openIdx])
			a.content.WriteString(content[i : i+openIdx])
			a.inThink = true
			i += openIdx + len("<think>")
			continue
		}
		// "<think>" の部分一致をチェック。
		if tail := partialTagSuffix(content[i:], "<think>"); tail != "" {
			safe := content[i : len(content)-len(tail)]
			out.WriteString(safe)
			a.content.WriteString(safe)
			a.tagBuf = tail
			break
		}
		out.WriteString(content[i:])
		a.content.WriteString(content[i:])
		break
	}
	return out.String()
}

// partialTagSuffix は text の末尾が tag の先頭部分と一致する場合、
// その一致部分を返す。例: partialTagSuffix("abc<thi", "<think>") → "<thi"
func partialTagSuffix(text, tag string) string {
	// tag の先頭 1 文字から (len(tag)-1) 文字までを末尾と比較。
	maxLen := len(tag) - 1
	if maxLen > len(text) {
		maxLen = len(text)
	}
	for n := maxLen; n >= 1; n-- {
		if strings.HasSuffix(text, tag[:n]) {
			return text[len(text)-n:]
		}
	}
	return ""
}

// accumulateToolCall は増分的なツール呼び出しデルタを蓄積する。
// ツール呼び出しは複数チャンクに分割されて到着する:
// - 最初のチャンク: ID + 関数名
// - 後続のチャンク: 引数の断片
func (a *streamAccumulator) accumulateToolCall(tc providers.ToolCall) {
	// ID があれば新しいツール呼び出し。
	if tc.ID != "" {
		a.toolCalls = append(a.toolCalls, providers.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: providers.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
		return
	}

	// 既存のツール呼び出しに引数を追加。
	if len(a.toolCalls) > 0 {
		last := &a.toolCalls[len(a.toolCalls)-1]
		if tc.Function.Name != "" {
			last.Function.Name += tc.Function.Name
		}
		last.Function.Arguments += tc.Function.Arguments
	}
}
