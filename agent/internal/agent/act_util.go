package agent

import (
	"sort"
	"strings"
	"time"

	"github.com/agnivade/levenshtein"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
)

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
		toolTokens += textutil.EstimateTokens(t.Name()) + textutil.EstimateTokens(t.Description()) + textutil.EstimateTokens(string(t.InputSchema())) + 20
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
		total += textutil.EstimateTokens(m.Content) + 4
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
		total -= textutil.EstimateTokens(msgs[trimStart].Content) + 4
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

