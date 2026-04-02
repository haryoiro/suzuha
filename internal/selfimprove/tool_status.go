package selfimprove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/haryoiro/suzuha/internal/tool"
)

// StatusTool lists pending self-improvement branches.
type StatusTool struct {
	repoRoot string
}

// NewStatusTool creates a StatusTool.
func NewStatusTool(repoRoot string) *StatusTool {
	return &StatusTool{repoRoot: repoRoot}
}

func (t *StatusTool) Name() string    { return "self_improve_status" }
func (t *StatusTool) ReadOnly() bool { return true }

func (t *StatusTool) Description() string {
	return "マージされていない self-improve/* ブランチの一覧を表示する。各ブランチのコミットサマリーを含む。"
}

func (t *StatusTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *StatusTool) Execute(ctx context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "branch", "--list", "self-improve/*",
		"--format=%(refname:short)\t%(objectname:short)\t%(subject)")
	cmd.Dir = t.repoRoot
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return tool.ErrorResult("ブランチ一覧取得失敗: " + err.Error()), nil
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return tool.TextResult("self-improve ブランチはありません。"), nil
	}

	var result strings.Builder
	result.WriteString("## self-improve ブランチ一覧\n\n")
	result.WriteString("| ブランチ | コミット | 内容 |\n")
	result.WriteString("|----------|----------|------|\n")

	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		result.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", parts[0], parts[1], parts[2]))
	}

	return tool.TextResult(result.String()), nil
}

var _ tool.Tool = (*StatusTool)(nil)
