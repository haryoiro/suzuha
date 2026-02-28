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
	return `Create a new custom tool. Describe what the tool should do and Claude Code will generate the Go implementation.
The generated code will be compiled and immediately available for use.
You can also provide source_code directly instead of a prompt if you prefer.`
}

func (t *CreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name":         {"type": "string", "description": "Tool name (lowercase alphanumeric + underscores, 1-64 chars). Example: prime_factorize"},
			"description":  {"type": "string", "description": "What this tool does (shown to you in future conversations)."},
			"input_schema": {"type": "object", "description": "JSON Schema for the tool's input parameters."},
			"prompt":       {"type": "string", "description": "Description of what the tool should do. Claude Code will generate the Go implementation."},
			"source_code":  {"type": "string", "description": "Optional: provide Go source code directly instead of a prompt. Must define func run(input json.RawMessage) (string, error). No package/import needed."}
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
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.Name == "" || in.Description == "" {
		return tool.ErrorResult("name and description are required"), nil
	}
	if in.Prompt == "" && in.SourceCode == "" {
		return tool.ErrorResult("either prompt or source_code is required"), nil
	}

	sourceCode := in.SourceCode
	if sourceCode == "" {
		// Use Claude Code CLI to generate the implementation.
		var err error
		schemaStr := string(in.InputSchema)
		sourceCode, err = t.manager.GenerateCode(ctx, in.Name, in.Description, schemaStr, in.Prompt)
		if err != nil {
			return tool.ErrorResult(fmt.Sprintf("code generation failed: %v", err)), nil
		}
	}

	if err := t.manager.Create(in.Name, in.Description, in.InputSchema, sourceCode); err != nil {
		return tool.ErrorResult(fmt.Sprintf("failed to create tool: %v", err)), nil
	}

	return tool.TextResult(fmt.Sprintf("Tool %q created and registered successfully. You can now use it.", in.Name)), nil
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
	return "Delete a custom tool that was previously created with create_tool."
}

func (t *DeleteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Name of the dynamic tool to delete."}
		},
		"required": ["name"]
	}`)
}

func (t *DeleteTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}

	if err := t.manager.Delete(in.Name); err != nil {
		return tool.ErrorResult(fmt.Sprintf("failed to delete tool: %v", err)), nil
	}

	return tool.TextResult(fmt.Sprintf("Tool %q deleted.", in.Name)), nil
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
	return "List all custom tools created with create_tool."
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
		return tool.ErrorResult("failed to list tools: " + err.Error()), nil
	}

	if len(manifests) == 0 {
		return tool.TextResult("No custom tools have been created yet."), nil
	}

	data, _ := json.MarshalIndent(manifests, "", "  ")
	return tool.TextResult(string(data)), nil
}

var _ tool.Tool = (*ListTool)(nil)
