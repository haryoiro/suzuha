package dyntools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

const defaultTimeout = 30 * time.Second

// ScriptTool executes a compiled Go binary as a subprocess.
// Input JSON is written to stdin; output JSON is read from stdout.
type ScriptTool struct {
	name        string
	description string
	inputSchema json.RawMessage
	binaryPath  string
	timeout     time.Duration
}

// NewScriptTool creates a new ScriptTool.
func NewScriptTool(name, description string, schema json.RawMessage, binaryPath string) *ScriptTool {
	return &ScriptTool{
		name:        name,
		description: description,
		inputSchema: schema,
		binaryPath:  binaryPath,
		timeout:     defaultTimeout,
	}
}

func (s *ScriptTool) Name() string                { return s.name }
func (s *ScriptTool) Description() string          { return s.description }
func (s *ScriptTool) InputSchema() json.RawMessage { return s.inputSchema }

// scriptOutput is the expected stdout format from a dynamic tool binary.
type scriptOutput struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

func (s *ScriptTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.binaryPath)
	cmd.Stdin = bytes.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return tool.ErrorResult(fmt.Sprintf("ツールの実行がタイムアウトしました (%s)", s.timeout)), nil
		}
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return tool.ErrorResult("ツールの実行に失敗しました: " + msg), nil
	}

	var out scriptOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return tool.ErrorResult("ツール出力が不正です: " + stdout.String()), nil
	}

	if out.IsError {
		return tool.ErrorResult(out.Content), nil
	}
	return tool.TextResult(out.Content), nil
}

var _ tool.Tool = (*ScriptTool)(nil)
