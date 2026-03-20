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
	return `Pythonコードを実行して結果を返す。計算、問題解決、コードの検証、Webからのデータ取得などに使える。
タイムアウト: 30秒。ネットワークアクセス可能。出力上限: 4000文字。
pipも使える。必要ならsubprocessでパッケージをインストールできる（例: subprocess.run(["pip","install","requests"])）。
重要: 出力は自分にしか見えない。ユーザーに結果を伝えたい場合は、返答テキストに含めること。`
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
