package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
)

// memorySaver はメモリの保存機能を提供する (consumer-side interface)。
type memorySaver interface {
	Save(ctx context.Context, mem *memory.Memory) error
}

// memoSearcher はタイプ指定のメモリ検索機能を提供する (consumer-side interface)。
type memoSearcher interface {
	SearchByType(ctx context.Context, query string, memType memory.MemoryType, limit int) ([]memory.Memory, error)
}

// memoUpdater はメモの取得・保存機能を提供する (consumer-side interface)。
type memoUpdater interface {
	Get(ctx context.Context, id string) (*memory.Memory, error)
	Save(ctx context.Context, mem *memory.Memory) error
}

// ── memo_create ──

type MemoCreate struct {
	store memorySaver
}

func NewMemoCreate(store memorySaver) *MemoCreate {
	return &MemoCreate{store: store}
}

func (t *MemoCreate) Name() string    { return "memo_create" }
func (t *MemoCreate) ReadOnly() bool { return false }
func (t *MemoCreate) Description() string {
	return "メモを作成する。思いついたこと、気づき、TODOなどを自由に記録できる。#tag で自動タグ付け。"
}

func (t *MemoCreate) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"content": {"type": "string", "description": "メモの内容。#tag を含めるとタグが自動抽出される。"}
		},
		"required": ["content"]
	}`)
}

type memoCreateInput struct {
	Content string `json:"content"`
}

func (t *MemoCreate) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in memoCreateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Content) == "" {
		return tool.ErrorResult("content が空です"), nil
	}

	tags := memory.ExtractTags(in.Content)
	meta := map[string]any{}
	if len(tags) > 0 {
		meta["tags"] = tags
	}

	mem := memory.Memory{
		Type:     memory.MemoryTypeMemo,
		Content:  in.Content,
		Metadata: meta,
	}
	if err := t.store.Save(ctx, &mem); err != nil {
		return tool.ErrorResult("保存に失敗: " + err.Error()), nil
	}

	result := fmt.Sprintf("メモを保存した (id=%s)", mem.ID)
	if len(tags) > 0 {
		result += fmt.Sprintf(" tags: %s", strings.Join(tags, ", "))
	}
	return tool.TextResult(result), nil
}

// ── memo_search ──

type MemoSearch struct {
	store memoSearcher
}

func NewMemoSearch(store memoSearcher) *MemoSearch {
	return &MemoSearch{store: store}
}

func (t *MemoSearch) Name() string    { return "memo_search" }
func (t *MemoSearch) ReadOnly() bool { return true }
func (t *MemoSearch) Description() string {
	return "メモを検索する。意味的な類似検索ができる。タグでの絞り込みも可能。"
}

func (t *MemoSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "検索クエリ"},
			"tag": {"type": "string", "description": "タグで絞り込む（任意）"}
		},
		"required": ["query"]
	}`)
}

type memoSearchInput struct {
	Query string `json:"query"`
	Tag   string `json:"tag"`
}

func (t *MemoSearch) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in memoSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return tool.ErrorResult("query が空です"), nil
	}

	mems, err := t.store.SearchByType(ctx, in.Query, memory.MemoryTypeMemo, 10)
	if err != nil {
		return tool.ErrorResult("検索に失敗: " + err.Error()), nil
	}

	// Tag filter.
	if in.Tag != "" {
		var filtered []memory.Memory
		for _, m := range mems {
			if hasMemoTag(m, in.Tag) {
				filtered = append(filtered, m)
			}
		}
		mems = filtered
	}

	if len(mems) == 0 {
		return tool.TextResult("メモが見つからなかった"), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d件のメモが見つかった:\n", len(mems))
	for _, m := range mems {
		tags := extractMetaTags(m)
		tagStr := ""
		if len(tags) > 0 {
			tagStr = " [" + strings.Join(tags, ", ") + "]"
		}
		fmt.Fprintf(&sb, "\n- id=%s (%s)%s\n  %s\n",
			m.ID, m.CreatedAt.Format("2006-01-02 15:04"), tagStr, m.Content)
	}
	return tool.TextResult(sb.String()), nil
}

// ── memo_update ──

type MemoUpdate struct {
	store memoUpdater
}

func NewMemoUpdate(store memoUpdater) *MemoUpdate {
	return &MemoUpdate{store: store}
}

func (t *MemoUpdate) Name() string    { return "memo_update" }
func (t *MemoUpdate) ReadOnly() bool { return false }
func (t *MemoUpdate) Description() string {
	return "既存のメモを更新する。内容を書き換えるとタグも再抽出される。"
}

func (t *MemoUpdate) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "更新するメモのID"},
			"content": {"type": "string", "description": "新しいメモの内容"}
		},
		"required": ["id", "content"]
	}`)
}

type memoUpdateInput struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

func (t *MemoUpdate) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in memoUpdateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}

	existing, err := t.store.Get(ctx, in.ID)
	if err != nil {
		return tool.ErrorResult("メモが見つからない: " + err.Error()), nil
	}
	if existing.Type != memory.MemoryTypeMemo {
		return tool.ErrorResult("指定されたIDはメモではない"), nil
	}

	existing.Content = in.Content
	tags := memory.ExtractTags(in.Content)
	if existing.Metadata == nil {
		existing.Metadata = map[string]any{}
	}
	if len(tags) > 0 {
		existing.Metadata["tags"] = tags
	} else {
		delete(existing.Metadata, "tags")
	}
	// Clear embedding so it gets recomputed on save.
	existing.Embedding = nil

	if err := t.store.Save(ctx, existing); err != nil {
		return tool.ErrorResult("更新に失敗: " + err.Error()), nil
	}

	result := fmt.Sprintf("メモを更新した (id=%s)", in.ID)
	if len(tags) > 0 {
		result += fmt.Sprintf(" tags: %s", strings.Join(tags, ", "))
	}
	return tool.TextResult(result), nil
}

// ── helpers ──

func hasMemoTag(m memory.Memory, tag string) bool {
	for _, t := range extractMetaTags(m) {
		if t == tag {
			return true
		}
	}
	return false
}

func extractMetaTags(m memory.Memory) []string {
	if m.Metadata == nil {
		return nil
	}
	raw, ok := m.Metadata["tags"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		tags := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return v
	default:
		return nil
	}
}
