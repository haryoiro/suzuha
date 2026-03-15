package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

// PythonExec is a tool that executes Python code in a sandboxed subprocess.
type PythonExec struct {
	timeout time.Duration
}

// NewPythonExec creates a PythonExec tool.
func NewPythonExec() *PythonExec {
	return &PythonExec{timeout: 30 * time.Second}
}

func (p *PythonExec) Name() string { return "python_exec" }

func (p *PythonExec) Description() string {
	return `Execute Python code and return stdout/stderr. Use this to run calculations, solve problems, verify code output, or fetch data from the web.
Timeout: 30 seconds. Network access is available. Max output: 4000 characters.
pip is available — use subprocess to install packages if needed (e.g. subprocess.run(["pip","install","requests"])).
IMPORTANT: The output is only visible to you, NOT to the user. If you want to share results, include them in your response text.`
}

func (p *PythonExec) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"code": {"type": "string", "description": "Python code to execute."}
		},
		"required": ["code"]
	}`)
}

type pythonInput struct {
	Code string `json:"code"`
}

const maxOutputChars = 4000

func (p *PythonExec) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in pythonInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Code) == "" {
		return tool.ErrorResult("code は必須です"), nil
	}

	execCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "python3", "-c", in.Code)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	var result strings.Builder
	fmt.Fprintf(&result, "[実行時間: %s]\n", elapsed.Round(time.Millisecond))

	if stdout.Len() > 0 {
		result.WriteString("--- stdout ---\n")
		result.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if stdout.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("--- stderr ---\n")
		result.WriteString(stderr.String())
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.WriteString("\n⏱ タイムアウト（30秒）")
		} else if stdout.Len() == 0 && stderr.Len() == 0 {
			fmt.Fprintf(&result, "実行エラー: %s", err)
		}
	}

	out := result.String()
	if len([]rune(out)) > maxOutputChars {
		out = string([]rune(out)[:maxOutputChars]) + "\n...(省略)"
	}

	if err != nil {
		return tool.ErrorResult(out), nil
	}
	return tool.TextResult(out), nil
}

var _ tool.Tool = (*PythonExec)(nil)
