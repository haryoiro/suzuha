package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/port/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type toolCallResult struct {
	tc      providers.ToolCall
	tool    tool.Tool
	result  *tool.ToolResult
	err     error
	elapsed time.Duration
}

// executeToolCalls はツール呼び出しを実行する。
// ReadOnly なツールは並列実行し、副作用ありツールは直列実行する。
// ツール結果は tool_call の順序で agentCtx に追加される。
func (a *Agent) executeToolCalls(
	ctx context.Context,
	agentCtx *Context,
	sess Session,
	toolMap map[string]tool.Tool,
	calls []providers.ToolCall,
	channel string,
	iter int,
) (allStopAfter bool) {
	allStopAfter = true

	// ツール呼び出しをパーティション: ReadOnly (並列可) と副作用あり (直列)。
	// 結果は tool_call 順序で agentCtx に追加するため、結果スロットを事前確保。
	results := make([]toolCallResult, len(calls))
	for i, tc := range calls {
		results[i].tc = tc
		if t, ok := toolMap[tc.Function.Name]; ok {
			results[i].tool = t
		}
	}

	// 連続する ReadOnly ツールをバッチとして並列実行。
	// 副作用ありツールに到達したら直列実行し、その後の ReadOnly をまた並列。
	i := 0
	for i < len(results) {
		// ReadOnly バッチの終端を探す
		batchEnd := i
		for batchEnd < len(results) && results[batchEnd].tool != nil && tool.IsReadOnly(results[batchEnd].tool) {
			batchEnd++
		}

		if batchEnd > i {
			// ReadOnly バッチを並列実行
			batch := results[i:batchEnd]
			a.executeToolBatchParallel(ctx, batch, iter)
			i = batchEnd
			continue
		}

		// 副作用ありツール or 不明ツール — 直列実行
		a.executeToolSingle(ctx, &results[i], iter)
		i++
	}

	// 結果を tool_call 順序で agentCtx に追加
	for _, r := range results {
		a.applyToolResult(ctx, agentCtx, sess, r, channel)
		if r.err != nil || (r.result != nil && !r.result.StopAfter) || r.tool == nil {
			allStopAfter = false
		}
	}

	return allStopAfter
}

// executeToolSingle は単一のツールを実行する。
func (a *Agent) executeToolSingle(ctx context.Context, r *toolCallResult, iter int) {
	a.logger.Info("ツールを使う",
		"iteration", iter,
		"tool", r.tc.Function.Name,
		"call_id", r.tc.ID,
		"args", textutil.TruncateRunes(r.tc.Function.Arguments, 200))

	if r.tool == nil {
		a.logger.Warn("知らないツールを呼ばれた", "tool", r.tc.Function.Name)
		r.err = fmt.Errorf("不明なツール %q", r.tc.Function.Name)
		return
	}

	var toolSpan trace.Span
	if a.tracer != nil {
		_, toolSpan = a.tracer.Start(ctx, "tool."+r.tc.Function.Name,
			trace.WithAttributes(
				attribute.String("tool.name", r.tc.Function.Name),
				attribute.String("tool.call_id", r.tc.ID),
				attribute.String("tool.input", textutil.TruncateRunes(r.tc.Function.Arguments, 2000)),
			),
		)
	}

	start := time.Now()
	r.result, r.err = r.tool.Execute(ctx, json.RawMessage(r.tc.Function.Arguments))
	r.elapsed = time.Since(start)

	if toolSpan != nil {
		toolSpan.SetAttributes(attribute.Int64("tool.duration_ms", r.elapsed.Milliseconds()))
		if r.err != nil {
			toolSpan.RecordError(r.err)
			toolSpan.SetAttributes(attribute.String("tool.output", r.err.Error()))
		} else if r.result != nil {
			var resultText string
			for _, c := range r.result.Content {
				resultText += c.Text
			}
			toolSpan.SetAttributes(
				attribute.String("tool.output", textutil.TruncateRunes(resultText, 2000)),
				attribute.Bool("tool.is_error", r.result.IsError),
			)
		}
		toolSpan.End()
	}
}

// executeToolBatchParallel は ReadOnly ツールのバッチを並列実行する。
// 1 つがエラーになったら他の実行中ツールを中断する (side-channel abort)。
func (a *Agent) executeToolBatchParallel(ctx context.Context, batch []toolCallResult, iter int) {
	if len(batch) == 1 {
		a.executeToolSingle(ctx, &batch[0], iter)
		return
	}

	a.logger.Info("ツールを並列実行",
		"iteration", iter,
		"count", len(batch),
		"tools", toolNames(batch))

	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for i := range batch {
		wg.Add(1)
		go func(r *toolCallResult) {
			defer wg.Done()
			a.executeToolSingle(batchCtx, r, iter)
			// エラーが発生したら他の兄弟ツールを中断
			if r.err != nil {
				a.logger.Debug("並列ツールでエラー、兄弟を中断", "tool", r.tc.Function.Name)
				cancel()
			}
		}(&batch[i])
	}
	wg.Wait()
}

// applyToolResult はツール実行結果を agentCtx に追加する。
func (a *Agent) applyToolResult(ctx context.Context, agentCtx *Context, sess Session, r toolCallResult, channel string) {
	if r.tool == nil {
		agentCtx.Add(toolResultMessage(fmt.Sprintf("error: %v", r.err), channel, r.tc.ID))
		return
	}

	if r.err != nil {
		a.logger.Error("ツールが失敗した",
			"tool", r.tc.Function.Name, "error", r.err, "elapsed_ms", r.elapsed.Milliseconds())
		agentCtx.Add(toolResultMessage(fmt.Sprintf("error: %v", r.err), channel, r.tc.ID))
		return
	}

	content := ""
	for _, c := range r.result.Content {
		content += c.Text
	}

	// Tool Result Budget: 8KB を超えるツール結果は truncate して
	// context window の爆発を防ぐ。
	const maxToolResultBytes = 8192
	if len(content) > maxToolResultBytes {
		a.logger.Warn("ツール結果が大きすぎるため切り詰め",
			"tool", r.tc.Function.Name, "original_bytes", len(content), "max", maxToolResultBytes)
		content = content[:maxToolResultBytes] + "\n\n... (結果が大きいため省略)"
	}

	a.logger.Info("ツールの結果",
		"tool", r.tc.Function.Name,
		"elapsed_ms", r.elapsed.Milliseconds(),
		"is_error", r.result.IsError,
		"result", textutil.TruncateRunes(content, 200))

	agentCtx.Add(toolResultMessage(content, channel, r.tc.ID))

	// Share python_exec output to Discord as a code block.
	if channel != "" && r.tc.Function.Name == "python_exec" && content != "" {
		if err := sess.Respond(ctx, "```\n"+content+"\n```"); err != nil {
			a.logger.Warn("ツール結果の共有に失敗", "error", err)
		}
	}

	// If the tool returned images, inject them as a user message.
	if len(r.result.ImageURLs) > 0 {
		agentCtx.Add(llm.Message{
			Role:      "user",
			Content:   content,
			ImageURLs: r.result.ImageURLs,
			Channel:   channel,
			Timestamp: jtime.Now(),
		})
	}
}

// sourceKeyFromChannel は channel が属する SourceKey を逆引きする。
func sourceKeyFromChannel(channel string, contexts map[SourceKey]*Context) SourceKey {
	for key, ctx := range contexts {
		for _, m := range ctx.Messages() {
			if m.Channel == channel {
				return key
			}
		}
	}
	return SourceKeyDiscord
}

// toolNames は toolCallResult のスライスからツール名を抽出する。
func toolNames(results []toolCallResult) []string {
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.tc.Function.Name
	}
	return names
}
