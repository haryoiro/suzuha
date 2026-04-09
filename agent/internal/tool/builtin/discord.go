package builtin

import (
	"context"
	"encoding/json"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/tool"
)

// discordTool is a generic tool backed by a discordgo.Session.
type discordTool struct {
	session  *discordgo.Session
	name     string
	desc     string
	schema   json.RawMessage
	readOnly bool
	fn       func(*discordgo.Session, context.Context, json.RawMessage) (*tool.ToolResult, error)
}

func (d *discordTool) Name() string                { return d.name }
func (d *discordTool) Description() string          { return d.desc }
func (d *discordTool) InputSchema() json.RawMessage { return d.schema }
func (d *discordTool) ReadOnly() bool               { return d.readOnly }
func (d *discordTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	return d.fn(d.session, ctx, input)
}

var _ tool.Tool = (*discordTool)(nil)
var _ tool.ReadOnlyTool = (*discordTool)(nil)

// unmarshal is a helper that unmarshals input and returns an error result on failure.
func unmarshal[T any](input json.RawMessage) (T, *tool.ToolResult) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return v, tool.ErrorResult("無効な入力: " + err.Error())
	}
	return v, nil
}

// NewDiscordTools returns all Discord tools for the given session.
func NewDiscordTools(s *discordgo.Session) []tool.Tool {
	return []tool.Tool{
		newDiscordReact(s),
		newDiscordReply(s),
		newDiscordGetHistory(s),
		newDiscordSendDM(s),
		newDiscordCreateChannel(s),
		newDiscordEditChannel(s),
		newDiscordDeleteChannel(s),
		newDiscordListChannels(s),
		newDiscordKickMember(s),
		newDiscordBanMember(s),
		newDiscordTimeoutMember(s),
		newDiscordListMembers(s),
		newDiscordDeleteMessage(s),
		newDiscordPinMessage(s),
		newDiscordAddRole(s),
		newDiscordRemoveRole(s),
		newDiscordListRoles(s),
		newDiscordServerInfo(s),
		newDiscordCreateThread(s),
		newDiscordRenameServer(s),
		newDiscordSetNickname(s),
		newDiscordUpdateStatus(s),
	}
}
