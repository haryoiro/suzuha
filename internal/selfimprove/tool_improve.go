package selfimprove

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/tool"
)

// ImproveTool posts a structured improvement request to the self-improve Discord channel.
// Claude Code (listening via Discord plugin) picks it up and makes code changes.
type ImproveTool struct {
	session   *discordgo.Session
	channelID string

	mu         sync.Mutex
	timestamps []time.Time
}

// NewImproveTool creates an ImproveTool.
func NewImproveTool(s *discordgo.Session, channelID string) *ImproveTool {
	return &ImproveTool{session: s, channelID: channelID}
}

func (t *ImproveTool) Name() string { return "self_improve" }

func (t *ImproveTool) Description() string {
	return `自分自身のコードを改善するためのリクエストを、開発チャンネルに投稿する。
Claude Code が Discord 経由でリクエストを受け取り、git worktree 上でコードを変更してブランチにコミットする。
変更はレビュー後にオーナーがマージするまで反映されない。

使用条件:
- 明確で具体的な改善内容がある場合のみ使用すること
- 漠然とした「改善したい」では使わない
- レートリミット: 1時間に3回まで`
}

func (t *ImproveTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"instruction": {
				"type": "string",
				"description": "改善内容の詳細な指示。何を、なぜ、どう変えたいかを具体的に書く。"
			},
			"target_files": {
				"type": "array",
				"items": {"type": "string"},
				"description": "変更対象のファイルパス (optional)。例: [\"internal/agent/act.go\"]"
			},
			"channel_name": {
				"type": "string",
				"description": "このリクエストの発端となったDiscordチャンネル名 (optional)"
			},
			"guild_name": {
				"type": "string",
				"description": "このリクエストの発端となったDiscordサーバー名 (optional)"
			}
		},
		"required": ["instruction"]
	}`)
}

type improveInput struct {
	Instruction string   `json:"instruction"`
	TargetFiles []string `json:"target_files"`
	ChannelName string   `json:"channel_name"`
	GuildName   string   `json:"guild_name"`
}

const maxPerHour = 3

func (t *ImproveTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in improveInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Instruction) == "" {
		return tool.ErrorResult("instruction は必須です"), nil
	}

	// Rate limit check.
	if !t.allowRequest() {
		return tool.ErrorResult("レートリミット超過: 1時間に最大3回まで。しばらく待ってから再試行してください"), nil
	}

	// Build the message.
	var msg strings.Builder
	msg.WriteString("## 🔧 自己改善リクエスト\n\n")
	msg.WriteString("### 指示\n")
	msg.WriteString(in.Instruction)
	msg.WriteString("\n")

	if len(in.TargetFiles) > 0 {
		msg.WriteString("\n### 対象ファイル\n")
		for _, f := range in.TargetFiles {
			msg.WriteString("- `" + f + "`\n")
		}
	}

	if in.ChannelName != "" || in.GuildName != "" {
		msg.WriteString("\n### コンテキスト\n")
		if in.GuildName != "" {
			msg.WriteString("- サーバー: " + in.GuildName + "\n")
		}
		if in.ChannelName != "" {
			msg.WriteString("- チャンネル: #" + in.ChannelName + "\n")
		}
	}

	msg.WriteString("\n---\n")
	msg.WriteString("*git worktree で変更して、ブランチにコミットしてください。マージはしないでください。オーナーがレビューします。*")

	// Send to the self-improve channel.
	_, err := t.session.ChannelMessageSend(t.channelID, msg.String())
	if err != nil {
		return tool.ErrorResult("チャンネルへの投稿失敗: " + err.Error()), nil
	}

	return tool.TextResult(fmt.Sprintf(
		"改善リクエストを <#%s> に投稿しました。Claude Code が対応します。",
		t.channelID,
	)), nil
}

func (t *ImproveTool) allowRequest() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Hour)

	// Remove expired entries.
	valid := t.timestamps[:0]
	for _, ts := range t.timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	t.timestamps = valid

	if len(t.timestamps) >= maxPerHour {
		return false
	}
	t.timestamps = append(t.timestamps, now)
	return true
}

var _ tool.Tool = (*ImproveTool)(nil)
