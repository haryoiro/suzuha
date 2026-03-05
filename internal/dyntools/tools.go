package dyntools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/tool"
)

// ── create_tool ──

// CreateTool lets the LLM create a new dynamic tool at runtime.
// It uses Claude Code CLI to generate the Go implementation from a prompt.
type CreateTool struct {
	manager *Manager
}

func NewCreateTool(mgr *Manager) *CreateTool { return &CreateTool{manager: mgr} }

func (t *CreateTool) Name() string { return "create_tool" }
func (t *CreateTool) Description() string {
	return `新しいカスタムツールを作成します。ツールの動作を記述すると、Claude Codeがgoの実装を生成します。
生成されたコードはコンパイルされ、すぐに使用可能になります。
プロンプトの代わりにsource_codeを直接指定することもできます。`
}

func (t *CreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":         {"type": "string", "description": "ツール名（小文字英数字+アンダースコア、1-64文字）。例: prime_factorize"},
			"description":  {"type": "string", "description": "ツールの説明（今後の会話で表示されます）。"},
			"input_schema": {"type": "object", "description": "ツールの入力パラメータのJSONスキーマ。"},
			"prompt":       {"type": "string", "description": "ツールの動作の説明。Claude CodeがGoの実装を生成します。"},
			"source_code":  {"type": "string", "description": "任意: プロンプトの代わりにGoソースコードを直接指定。func run(input json.RawMessage) (string, error) を定義する必要があります。package/importは不要。"}
		},
		"required": ["name", "description", "input_schema"]
	}`)
}

func (t *CreateTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
		Prompt      string          `json:"prompt"`
		SourceCode  string          `json:"source_code"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("入力が不正です: " + err.Error()), nil
	}
	if in.Name == "" || in.Description == "" {
		return tool.ErrorResult("name と description は必須です"), nil
	}
	if in.Prompt == "" && in.SourceCode == "" {
		return tool.ErrorResult("prompt または source_code のいずれかが必要です"), nil
	}

	sourceCode := in.SourceCode
	if sourceCode == "" {
		// Use Claude Code CLI to generate the implementation.
		var err error
		schemaStr := string(in.InputSchema)
		sourceCode, err = t.manager.GenerateCode(ctx, in.Name, in.Description, schemaStr, in.Prompt)
		if err != nil {
			return tool.ErrorResult(fmt.Sprintf("コード生成に失敗しました: %v", err)), nil
		}
	}

	if err := t.manager.Create(in.Name, in.Description, in.InputSchema, sourceCode); err != nil {
		return tool.ErrorResult(fmt.Sprintf("ツールの作成に失敗しました: %v", err)), nil
	}

	return tool.TextResult(fmt.Sprintf("ツール %q を作成・登録しました。使用可能です。", in.Name)), nil
}

var _ tool.Tool = (*CreateTool)(nil)

// ── delete_tool ──

// DeleteTool lets the LLM remove a dynamic tool.
type DeleteTool struct {
	manager *Manager
}

func NewDeleteTool(mgr *Manager) *DeleteTool { return &DeleteTool{manager: mgr} }

func (t *DeleteTool) Name() string { return "delete_tool" }
func (t *DeleteTool) Description() string {
	return "create_tool で作成したカスタムツールを削除します。"
}

func (t *DeleteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "削除するダイナミックツールの名前。"}
		},
		"required": ["name"]
	}`)
}

func (t *DeleteTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("入力が不正です: " + err.Error()), nil
	}

	if err := t.manager.Delete(in.Name); err != nil {
		return tool.ErrorResult(fmt.Sprintf("ツールの削除に失敗しました: %v", err)), nil
	}

	return tool.TextResult(fmt.Sprintf("ツール %q を削除しました。", in.Name)), nil
}

var _ tool.Tool = (*DeleteTool)(nil)

// ── list_dynamic_tools ──

// ListTool lets the LLM see what custom tools exist.
type ListTool struct {
	manager *Manager
}

func NewListTool(mgr *Manager) *ListTool { return &ListTool{manager: mgr} }

func (t *ListTool) Name() string { return "list_dynamic_tools" }
func (t *ListTool) Description() string {
	return "create_tool で作成した全カスタムツールを一覧表示します。"
}

func (t *ListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {}
	}`)
}

func (t *ListTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	manifests, err := t.manager.List()
	if err != nil {
		return tool.ErrorResult("ツール一覧の取得に失敗しました: " + err.Error()), nil
	}

	if len(manifests) == 0 {
		return tool.TextResult("カスタムツールはまだ作成されていません。"), nil
	}

	data, _ := json.MarshalIndent(manifests, "", "  ")
	return tool.TextResult(string(data)), nil
}

var _ tool.Tool = (*ListTool)(nil)
