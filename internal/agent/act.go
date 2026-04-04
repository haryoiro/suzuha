package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agnivade/levenshtein"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// skipResponseTool is a virtual tool that signals the LLM wants to skip responding.
// It is NOT registered in the global tool.Registry — it is injected per-call
// into completeWithTools only when the directive allows skipping.
type skipResponseTool struct{}

func (skipResponseTool) Name() string    { return "skip_response" }
func (skipResponseTool) ReadOnly() bool { return true }
func (skipResponseTool) Description() string {
	return "この会話に返答しないときに呼ぶ。重要: テキストを返すなら絶対にこのツールを呼ばないこと（テキストとskip_responseの同時使用は禁止）。discord_react と一緒に呼んでもよい。"
}
func (skipResponseTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"スキップ理由（ログ用）"}},"required":[]}`)
}
func (skipResponseTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	return tool.StopResult("skipped"), nil
}

var _ tool.Tool = skipResponseTool{}

// Act is the backward-compatible wrapper that calls ActWith with the discord context
// and session, then routes the response through the discord session.
func (a *Agent) Act(ctx context.Context, p *Perception, t *Thought) error {
	sess := a.sessions[SourceKeyDiscord]
	sess.BeginTurn(p)
	text, err := a.ActWith(ctx, a.contexts[SourceKeyDiscord], sess, p, t)
	if err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	return sess.Respond(ctx, text)
}

// ActWith runs the LLM completion with tool loop, filters the response,
// and returns the response text. It does NOT route the response to any output;
// the caller is responsible for sending the response via Session.Respond().
func (a *Agent) ActWith(ctx context.Context, agentCtx *Context, sess Session, p *Perception, t *Thought) (string, error) {
	resp, intermediateText, err := a.completeWithToolsUsing(ctx, agentCtx, sess, t, p.Channel)
	if err != nil {
		return "", fmt.Errorf("agent: 補完に失敗: %w", err)
	}

	// Add assistant response to context.
	// When the response has tool calls, the assistant message was already added
	// inside completeWithToolsUsing (before executing the tools).
	if !resp.HasToolCalls() {
		agentCtx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   p.Channel,
			Timestamp: jtime.Now(),
		})
	}

	// Send response (strip directive tags and silent markers).
	// Think tags are already parsed in llm.Complete().
	text := strings.TrimSpace(llm.StripDirectiveTags(resp.Text))
	switch {
	case text == "":
		a.logger.Debug("何も思いつかなかった")
		return "", nil
	case containsSkipTool(resp.ToolCalls):
		a.logger.Info("黙った (skip_response)",
			"had_text", text != "")
		return "", nil
	case intermediateText != "" && isSimilarText(intermediateText, text):
		a.logger.Info("同じこと言いそうなので黙った",
			"intermediate_length", len(intermediateText),
			"final_length", len(text))
		return "", nil
	case llm.IsSilentResponse(text):
		a.logger.Info("黙った (サイレント)",
			"raw_text", truncate(resp.Text, 100))
		return "", nil
	default:
		return text, nil
	}
}

// completeWithToolsUsing runs the LLM and executes tool calls in a loop,
// using the given agent context and session for typing/intermediate responses.
func (a *Agent) completeWithToolsUsing(ctx context.Context, agentCtx *Context, sess Session, t *Thought, channel string) (*llm.Response, string, error) {
	directive := t.Directive
	allTools := a.tools.AllEnabled()

	if !strings.HasPrefix(directive, "[RESPOND]") {
		allTools = append(allTools, skipResponseTool{})
	}

	// Build a local tool map for this call (includes injected skip_response).
	toolMap := make(map[string]tool.Tool, len(allTools))
	for _, t := range allTools {
		toolMap[t.Name()] = t
	}

	maxIter := 10
	var intermediateText string
	var lowProgressStreak int // 連続して出力が少ない回数 (diminishing returns 検知用)

	for iter := range maxIter {
		// Send typing indicator only on subsequent iterations (tool loops),
		// not on the first call where we don't yet know if we'll respond.
		if iter > 0 && channel != "" {
			if ds, ok := sess.(*DiscordSession); ok {
				ds.Typing(ctx)
			}
		}

		var msgs []llm.Message
		if iter == 0 {
			msgs = t.BuildMessages(agentCtx.SystemPrompt(), agentCtx.Messages())
		} else {
			msgs = agentCtx.MessagesWithSystem()
		}
		// Trim messages to fit within max context, reserving space for tools.
		maxCtx := a.llm.MaxContextTokens()
		msgs = trimMessagesToFit(msgs, allTools, maxCtx)

		// Reactive Compact: ツールループ中にコンテキストが 90% を超えたら
		// 緊急圧縮してコンテキストを解放する。
		if iter > 0 && maxCtx > 0 {
			estimated := agentCtx.EstimatedTokens()
			if float64(estimated)/float64(maxCtx) > 0.9 {
				a.logger.Warn("ツールループ中にコンテキスト逼迫、緊急圧縮",
					"usage_ratio", fmt.Sprintf("%.2f", float64(estimated)/float64(maxCtx)),
					"estimated", estimated, "max", maxCtx)
				a.compactAsyncFor(ctx, agentCtx, sourceKeyFromChannel(channel, a.contexts))
				// 圧縮後のメッセージで再構築
				msgs = agentCtx.MessagesWithSystem()
				msgs = trimMessagesToFit(msgs, allTools, maxCtx)
			}
		}

		// チャンネルごとにグルーピングし、現チャンネルを末尾に寄せる。
		// LLM の recency bias を活かして現チャンネルの会話にフォーカスさせる。
		if channel != "" {
			msgs = groupByChannel(msgs, channel)
		}

		resp, err := a.llm.Complete(ctx, msgs, allTools)
		if err != nil {
			return nil, intermediateText, err
		}

		a.logger.Info("考えた",
			"iteration", iter,
			"finish_reason", resp.FinishReason,
			"text_length", len(resp.Text),
			"tool_calls", len(resp.ToolCalls),
			"tokens_in", resp.Usage.PromptTokens,
			"tokens_out", resp.Usage.CompletionTokens,
			"content", truncate(resp.Text, 200))

		// Calibrate token estimator using actual prompt tokens from the provider.
		if iter == 0 && resp.Usage.PromptTokens > 0 {
			agentCtx.CalibrateTokens(resp.Usage.PromptTokens)
			a.logger.Debug("トークン計算を補正",
				"actual", resp.Usage.PromptTokens,
				"estimated", agentCtx.EstimatedTokens(),
				"ratio", fmt.Sprintf("%.2f", agentCtx.TokenCalibration()))
		}

		if !resp.HasToolCalls() {
			return resp, intermediateText, nil
		}

		// Diminishing returns detection: 出力トークンが少ない iteration が
		// 3 回以上続いたらツールループを打ち切る (空回り防止)。
		if resp.Usage.CompletionTokens < 500 {
			lowProgressStreak++
		} else {
			lowProgressStreak = 0
		}
		if lowProgressStreak >= 3 {
			a.logger.Warn("ツールループの進捗が停滞、打ち切り",
				"iteration", iter, "low_progress_streak", lowProgressStreak)
			return resp, intermediateText, nil
		}

		agentCtx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   channel,
			Timestamp: jtime.Now(),
			ToolCalls: resp.ToolCalls,
		})

		// Send intermediate text if the LLM returned text alongside tool calls.
		if stripped := llm.StripDirectiveTags(resp.Text); stripped != "" && channel != "" && !containsSkipTool(resp.ToolCalls) {
			a.logger.Info("途中で話した",
				"channel", channel, "length", len(stripped))
			if err := sess.Respond(ctx, stripped); err != nil {
				a.logger.Warn("途中の発言に失敗", "error", err)
			}
			intermediateText = stripped
		}

		allStopAfter := a.executeToolCalls(ctx, agentCtx, sess, toolMap, resp.ToolCalls, channel, iter)

		if allStopAfter {
			a.logger.Info("ツールが完了した", "iteration", iter)
			return resp, intermediateText, nil
		}
	}

	return nil, intermediateText, fmt.Errorf("agent: ツールループが %d 回の反復を超過しました", maxIter)
}

// isSimilarText returns true if two texts are similar enough to be considered duplicates.
// Used for intra-turn dedup (intermediate text vs final response). Threshold: 95%.
func isSimilarText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	dist := levenshtein.ComputeDistance(a, b)
	maxLen := max(len([]rune(a)), len([]rune(b)))
	return 1.0-float64(dist)/float64(maxLen) >= 0.95
}

// containsSkipTool returns true if the tool calls include skip_response.
func containsSkipTool(calls []providers.ToolCall) bool {
	for _, tc := range calls {
		if tc.Function.Name == "skip_response" {
			return true
		}
	}
	return false
}

// trimMessagesToFit drops the oldest non-system messages (from the front,
// after the first system message) so the total estimated tokens fit within
// maxTokens, leaving room for tool definitions and a generation budget.
func trimMessagesToFit(msgs []llm.Message, tools []tool.Tool, maxTokens int) []llm.Message {
	if maxTokens <= 0 {
		return msgs
	}

	// Estimate tool definition tokens from actual schema sizes.
	toolTokens := 0
	for _, t := range tools {
		// name + description + JSON schema + overhead
		toolTokens += estimateStringTokens(t.Name()) + estimateStringTokens(t.Description()) + estimateStringTokens(string(t.InputSchema())) + 20
	}
	// Reserve tokens for generation output.
	generationBudget := 512
	budget := maxTokens - toolTokens - generationBudget
	if budget < 500 {
		budget = 500
	}

	// Calculate total tokens.
	total := 0
	for _, m := range msgs {
		total += estimateStringTokens(m.Content) + 4
	}

	if total <= budget {
		return msgs
	}

	// Find the first non-system message index (skip leading system prompt).
	trimStart := 0
	for trimStart < len(msgs) && msgs[trimStart].Role == "system" {
		trimStart++
	}

	// Drop oldest conversation messages until we fit.
	for total > budget && trimStart < len(msgs)-1 {
		total -= estimateStringTokens(msgs[trimStart].Content) + 4
		trimStart++
	}

	// Rebuild: leading system messages + remaining messages.
	var leading int
	for leading < len(msgs) && msgs[leading].Role == "system" {
		leading++
	}
	result := make([]llm.Message, 0, leading+(len(msgs)-trimStart))
	result = append(result, msgs[:leading]...)
	result = append(result, msgs[trimStart:]...)
	return result
}

// groupByChannel はメッセージをチャンネルごとにグルーピングし、
// activeChannel を末尾に配置する。各チャンネル内の順序は維持される。
// 他チャンネルは最終メッセージ時刻の古い順に並ぶ。
// system/tool ロールのメッセージは先頭にそのまま残す。
func groupByChannel(msgs []llm.Message, activeChannel string) []llm.Message {
	if activeChannel == "" || len(msgs) == 0 {
		return msgs
	}

	// system/tool メッセージ (先頭部分) とチャンネル付きメッセージを分離する。
	var head []llm.Message
	var channelMsgs []llm.Message
	inHead := true
	for _, m := range msgs {
		// 先頭の system メッセージ群はそのまま維持。
		// Channel が空の assistant/tool メッセージ (ツールループ中) も
		// 直前のチャンネルに属するので channelMsgs に含める。
		if inHead && (m.Role == "system") {
			head = append(head, m)
			continue
		}
		inHead = false
		channelMsgs = append(channelMsgs, m)
	}

	if len(channelMsgs) == 0 {
		return msgs
	}

	// チャンネルごとにグルーピング (出現順を維持)。
	type channelGroup struct {
		channel string
		msgs    []llm.Message
		lastTS  time.Time
	}
	groupMap := make(map[string]*channelGroup)
	var groupOrder []string

	for _, m := range channelMsgs {
		ch := m.Channel
		// Channel が空のメッセージ (assistant 応答, tool 結果) は
		// 直前のチャンネルに帰属させる。
		if ch == "" && len(groupOrder) > 0 {
			ch = groupOrder[len(groupOrder)-1]
		}
		if ch == "" {
			ch = activeChannel
		}

		g, ok := groupMap[ch]
		if !ok {
			g = &channelGroup{channel: ch}
			groupMap[ch] = g
			groupOrder = append(groupOrder, ch)
		}
		g.msgs = append(g.msgs, m)
		if !m.Timestamp.IsZero() && m.Timestamp.After(g.lastTS) {
			g.lastTS = m.Timestamp
		}
	}

	// activeChannel 以外を最終メッセージ時刻でソート。
	var others []*channelGroup
	var active *channelGroup
	for _, ch := range groupOrder {
		g := groupMap[ch]
		if ch == activeChannel {
			active = g
		} else {
			others = append(others, g)
		}
	}
	sort.Slice(others, func(i, j int) bool {
		return others[i].lastTS.Before(others[j].lastTS)
	})

	// 結合: head → 他チャンネル (古い順) → 現チャンネル
	result := make([]llm.Message, 0, len(msgs))
	result = append(result, head...)
	for _, g := range others {
		result = append(result, g.msgs...)
	}
	if active != nil {
		result = append(result, active.msgs...)
	}

	return result
}

// toolCallResult はツール実行の結果を保持する。
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
		"args", truncate(r.tc.Function.Arguments, 200))

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
				attribute.String("tool.input", truncate(r.tc.Function.Arguments, 2000)),
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
				attribute.String("tool.output", truncate(resultText, 2000)),
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
		agentCtx.Add(llm.Message{
			Role:       "tool",
			Content:    fmt.Sprintf("error: %v", r.err),
			Channel:    channel,
			ToolCallID: r.tc.ID,
			Timestamp:  jtime.Now(),
		})
		return
	}

	if r.err != nil {
		a.logger.Error("ツールが失敗した",
			"tool", r.tc.Function.Name, "error", r.err, "elapsed_ms", r.elapsed.Milliseconds())
		agentCtx.Add(llm.Message{
			Role:       "tool",
			Content:    fmt.Sprintf("error: %v", r.err),
			Channel:    channel,
			ToolCallID: r.tc.ID,
			Timestamp:  jtime.Now(),
		})
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
		"result", truncate(content, 200))

	agentCtx.Add(llm.Message{
		Role:       "tool",
		Content:    content,
		Channel:    channel,
		ToolCallID: r.tc.ID,
		Timestamp:  jtime.Now(),
	})

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
