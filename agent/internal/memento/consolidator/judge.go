package consolidator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
)

const judgeSystemPrompt = `あなたは記憶の管理者です。類似した記憶のグループを評価し、重複を判定してください。

判定ルール:
- keep: まったく同じ事柄を別の言い回しで記録したもの → 最も情報量が多いものを残す
- merge: 異なる時点だが同一の事柄・文脈で、統合すると情報が増える場合のみ
- skip: 以下に該当する場合は必ず skip にすること
  - 話題やキーワードが似ているだけで、具体的な内容が異なる
  - 同じ人物についてだが、別の事実や出来事を記録している
  - user_id や participants が異なる
  - 日付が大きく離れている（同じ話題でも別の機会の出来事）
  - 片方が一般的な事実、もう片方が特定のエピソード

重要: 迷ったら skip にしてください。誤って削除・統合すると情報が失われます。`

func (c *Consolidator) judgeBatch(ctx context.Context, groups []memoryGroup) ([]decision, error) {
	prompt := buildJudgePrompt(groups)

	resp, err := c.llm.CompleteRaw(ctx, []llm.RawMessage{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	return parseDecisions(resp.Text, groups)
}

func buildJudgePrompt(groups []memoryGroup) string {
	var sb strings.Builder

	sb.WriteString("以下の記憶グループを評価してください。\n\n")

	for gi, g := range groups {
		fmt.Fprintf(&sb, "=== グループ %d（型: %s, %d件）===\n", gi+1, g.memType, len(g.members))
		for mi, m := range g.members {
			content := textutil.TruncateRunes(m.content, 200)
			date := m.createdAt.Format("2006-01-02")
			meta := formatMaintainMetadata(m.metadata)
			if meta != "" {
				fmt.Fprintf(&sb, "[%d-%d] id=%s date=%s %s\n%s\n\n", gi+1, mi+1, m.id, date, meta, content)
			} else {
				fmt.Fprintf(&sb, "[%d-%d] id=%s date=%s\n%s\n\n", gi+1, mi+1, m.id, date, content)
			}
		}
	}

	sb.WriteString("JSON配列で返してください（これだけ出力して）:\n```\n")
	sb.WriteString(`[{"group":1,"action":"keep|merge|skip","keep_id":"残すID","merged_content":"統合内容(200字以内)","reason":"理由"}]`)
	sb.WriteString("\n```\n")
	sb.WriteString("- keep: keep_id に残す記憶の ID を指定\n")
	sb.WriteString("- merge: merged_content に統合後の内容を記述\n")
	sb.WriteString("- skip: 変更なし\n")

	return sb.String()
}

type llmDecision struct {
	Group         int    `json:"group"`
	Action        string `json:"action"`
	KeepID        string `json:"keep_id"`
	MergedContent string `json:"merged_content"`
	Reason        string `json:"reason"`
}

func parseDecisions(raw string, groups []memoryGroup) ([]decision, error) {
	raw = textutil.StripCodeFence(strings.TrimSpace(raw))

	var llmDecs []llmDecision
	if err := json.Unmarshal([]byte(raw), &llmDecs); err != nil {
		return nil, fmt.Errorf("parse: %w (raw: %s)", err, textutil.TruncateRunes(raw, 200))
	}

	var decisions []decision
	for _, ld := range llmDecs {
		if ld.Group < 1 || ld.Group > len(groups) {
			continue
		}
		g := groups[ld.Group-1]

		switch ld.Action {
		case "keep":
			if ld.KeepID == "" {
				continue
			}
			found := false
			var delIDs []string
			for _, m := range g.members {
				if m.id == ld.KeepID {
					found = true
				} else {
					delIDs = append(delIDs, m.id)
				}
			}
			if !found || len(delIDs) == 0 {
				continue
			}
			decisions = append(decisions, decision{
				action:    "keep",
				keepID:    ld.KeepID,
				deleteIDs: delIDs,
				groupType: g.memType,
				reason:    ld.Reason,
			})

		case "merge":
			if ld.MergedContent == "" {
				continue
			}
			var allIDs []string
			for _, m := range g.members {
				allIDs = append(allIDs, m.id)
			}
			decisions = append(decisions, decision{
				action:        "merge",
				deleteIDs:     allIDs,
				mergedContent: ld.MergedContent,
				groupType:     g.memType,
				reason:        ld.Reason,
				sourceEntries: g.members,
			})
		}
	}
	return decisions, nil
}

// formatMaintainMetadata はLLMプロンプト用にメタデータの主要フィールドを整形する。
func formatMaintainMetadata(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	var parts []string
	if uid, ok := meta["user_id"].(string); ok && uid != "" {
		parts = append(parts, "user_id="+uid)
	}
	switch v := meta["participants"].(type) {
	case []any:
		var ids []string
		for _, p := range v {
			if s, ok := p.(string); ok {
				ids = append(ids, s)
			}
		}
		if len(ids) > 0 {
			parts = append(parts, "participants="+strings.Join(ids, ","))
		}
	case []string:
		if len(v) > 0 {
			parts = append(parts, "participants="+strings.Join(v, ","))
		}
	}
	if tone, ok := meta["emotional_tone"].(string); ok && tone != "" {
		parts = append(parts, "tone="+tone)
	}
	return strings.Join(parts, " ")
}
