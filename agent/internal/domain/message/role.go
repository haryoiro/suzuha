package message

// Role 定数。Message.Role 比較を文字列リテラルから型付き定数に置き換える。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// IsSystem は system ロールのメッセージか判定する。
func (m Message) IsSystem() bool { return m.Role == RoleSystem }

// IsUser は user ロールのメッセージか判定する。
func (m Message) IsUser() bool { return m.Role == RoleUser }

// IsAssistant は assistant ロールのメッセージか判定する。
func (m Message) IsAssistant() bool { return m.Role == RoleAssistant }

// IsTool は tool ロール (tool_result) のメッセージか判定する。
func (m Message) IsTool() bool { return m.Role == RoleTool }

// IsFromAgent は agent 自身の assistant メッセージか判定する。
// botID が空なら role == "assistant" だけで判定する。
func (m Message) IsFromAgent(botID string) bool {
	if !m.IsAssistant() {
		return false
	}
	if botID == "" {
		return true
	}
	return m.UserID == botID
}

// HasToolCalls は assistant メッセージがツール呼び出しを含むか判定する。
func (m Message) HasToolCalls() bool { return len(m.ToolCalls) > 0 }
