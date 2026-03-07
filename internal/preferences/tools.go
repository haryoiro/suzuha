package preferences

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/tool"
)

// RegisterPreferenceTool lets the agent register a new preference.
type RegisterPreferenceTool struct {
	store *Store
}

func NewRegisterPreferenceTool(s *Store) *RegisterPreferenceTool {
	return &RegisterPreferenceTool{store: s}
}

func (t *RegisterPreferenceTool) Name() string { return "register_preference" }
func (t *RegisterPreferenceTool) Description() string {
	return "新しい好み・価値観・興味を登録する。会話やexploreで気になったものを記録。初回はconfidence低めで仮登録される。"
}
func (t *RegisterPreferenceTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"topic":    {"type": "string", "description": "対象（例: 'ジャズ', 'Rust言語', '深夜の散歩'）"},
			"category": {"type": "string", "description": "カテゴリ（例: '音楽', '技術', '生活'）"},
			"stance":   {"type": "string", "enum": ["liked", "disliked", "curious", "undecided"], "description": "態度。初見ならcuriousが無難。"},
			"reasoning":{"type": "string", "description": "なぜそう思ったか（短く）"}
		},
		"required": ["topic", "category", "stance", "reasoning"]
	}`)
}

func (t *RegisterPreferenceTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Topic     string `json:"topic"`
		Category  string `json:"category"`
		Stance    string `json:"stance"`
		Reasoning string `json:"reasoning"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}

	confidence := 0.1
	switch Stance(in.Stance) {
	case StanceLiked, StanceDisliked:
		confidence = 0.3 // 好き/嫌いでも初回は控えめ
	case StanceCurious:
		confidence = 0.1
	case StanceUndecided:
		confidence = 0.2
	}

	p := &Preference{
		Topic:      in.Topic,
		Category:   in.Category,
		Stance:     Stance(in.Stance),
		Confidence: confidence,
		Reasoning:  in.Reasoning,
	}
	if err := t.store.Upsert(context.Background(), p); err != nil {
		return tool.ErrorResult("登録失敗: " + err.Error()), nil
	}
	return tool.TextResult(fmt.Sprintf("「%s」を%sとして登録しました (confidence=%.1f)", in.Topic, in.Stance, confidence)), nil
}

var _ tool.Tool = (*RegisterPreferenceTool)(nil)

// ListPreferencesTool lets the agent see its current preferences.
type ListPreferencesTool struct {
	store *Store
}

func NewListPreferencesTool(s *Store) *ListPreferencesTool {
	return &ListPreferencesTool{store: s}
}

func (t *ListPreferencesTool) Name() string { return "list_preferences" }
func (t *ListPreferencesTool) Description() string {
	return "自分の好み・価値観の一覧を見る。stance でフィルタ可能。"
}
func (t *ListPreferencesTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"stance": {"type": "string", "enum": ["liked", "disliked", "curious", "undecided", "all"], "description": "フィルタ。allで全件。"},
			"limit":  {"type": "integer", "description": "最大件数 (デフォルト20)"}
		}
	}`)
}

func (t *ListPreferencesTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Stance string `json:"stance"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}

	ctx := context.Background()
	var prefs []Preference
	var err error
	if in.Stance == "" || in.Stance == "all" {
		prefs, err = t.store.ListAll(ctx, in.Limit)
	} else {
		prefs, err = t.store.ListByStance(ctx, Stance(in.Stance), in.Limit)
	}
	if err != nil {
		return tool.ErrorResult("取得失敗: " + err.Error()), nil
	}

	b, _ := json.Marshal(prefs)
	return tool.TextResult(string(b)), nil
}

var _ tool.Tool = (*ListPreferencesTool)(nil)
