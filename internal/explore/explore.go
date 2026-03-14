package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// evaluate asks the LLM to assess content and decide whether to continue exploring.
// Shared between Task (cron) and ExploreTool (agent tool).
func evaluate(
	ctx context.Context,
	llmClient *llm.Client,
	systemPrompt string,
	title, content string,
	path []hop,
) (*evaluation, error) {
	var sb strings.Builder

	sb.WriteString("今読んでるもの:\n")
	fmt.Fprintf(&sb, "タイトル: %s\n", title)
	fmt.Fprintf(&sb, "内容: %s\n\n", truncateRunes(content, contentMaxRunes))

	if len(path) > 0 {
		sb.WriteString("ここまでの探索:\n")
		for i, h := range path {
			fmt.Fprintf(&sb, "%d. 「%s」→ %s\n", i+1, h.Title, h.Impression)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("気になることがあったら何が気になるか教えて。\n")
	sb.WriteString("もう十分なら next_query を null にして。\n\n")
	sb.WriteString("JSON で返して（これだけ出力して）:\n")
	sb.WriteString(`{"impression": "感想1-2文", "remember": true/false, "next_query": "キーワード" or null}`)
	sb.WriteString("\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := llmClient.CompleteRawDefault(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	text = stripCodeFence(text)

	var eval evaluation
	if err := json.Unmarshal([]byte(text), &eval); err != nil {
		return &evaluation{
			Impression: text,
			Remember:   false,
			NextQuery:  nil,
		}, nil
	}
	return &eval, nil
}

// pickResult asks the LLM to choose which search result to read next.
func pickResult(
	ctx context.Context,
	llmClient *llm.Client,
	systemPrompt string,
	query string,
	results []SearchResult,
	path []hop,
) (*SearchResult, error) {
	var sb strings.Builder

	fmt.Fprintf(&sb, "「%s」で検索した結果:\n\n", query)
	sb.WriteString(truncateResults(results, snippetMaxRunes))
	sb.WriteString("\n")

	if len(path) > 0 {
		sb.WriteString("ここまでの探索:\n")
		for i, h := range path {
			fmt.Fprintf(&sb, "%d. 「%s」→ %s\n", i+1, h.Title, h.Impression)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("どれが一番気になる？番号で答えて（どれも気にならなければ 0）。\n")
	sb.WriteString("数字だけ出力して。\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := llmClient.CompleteRawDefault(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm pick: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	var idx int
	for _, r := range text {
		if r >= '0' && r <= '9' {
			idx = idx*10 + int(r-'0')
		} else if idx > 0 {
			break
		}
	}

	if idx <= 0 || idx > len(results) {
		return nil, nil
	}
	return &results[idx-1], nil
}

// reflectOnExploration summarises the exploration as factual material.
// No character voice — the calling agent LLM adds personality when it
// turns this tool result into a Discord message.
func reflectOnExploration(
	ctx context.Context,
	llmClient *llm.Client,
	_ string, // systemPrompt intentionally ignored
	path []hop,
	remembered []string,
) (string, error) {
	var sb strings.Builder

	sb.WriteString("以下の探索結果を要約して。\n\n")
	sb.WriteString("たどった道:\n")
	for i, h := range path {
		fmt.Fprintf(&sb, "%d. 「%s」— %s\n", i+1, h.Title, h.Impression)
	}

	if len(remembered) > 0 {
		sb.WriteString("\n特に注目した点:\n")
		for _, r := range remembered {
			fmt.Fprintf(&sb, "- %s\n", r)
		}
	}

	sb.WriteString("\nルール:\n")
	sb.WriteString("- 客観的な事実・発見だけまとめる\n")
	sb.WriteString("- キャラクターの口調や感想は入れない\n")
	sb.WriteString("- 2-4文で簡潔に\n")

	resp, err := llmClient.CompleteRawDefault(ctx, []providers.Message{
		{Role: "user", Content: sb.String()},
	})
	if err != nil {
		return "", fmt.Errorf("reflect: %w", err)
	}
	return strings.TrimSpace(resp.Text), nil
}

// buildSummary creates a fallback summary when LLM reflection fails.
func buildSummary(path []hop, remembered []string) string {
	var sb strings.Builder

	for _, h := range path {
		fmt.Fprintf(&sb, "- %s: %s\n", h.Title, h.Impression)
	}

	if len(remembered) > 0 {
		sb.WriteString("\n注目点:\n")
		for _, r := range remembered {
			fmt.Fprintf(&sb, "- %s\n", r)
		}
	}

	return sb.String()
}
