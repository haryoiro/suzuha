package conversation

import (
	"context"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
)

// TurnEntry は会話ターンの 1 行を表す値オブジェクト。
// conversation_logs テーブルに永続化される。
type TurnEntry struct {
	TurnID     string
	ChannelID  string
	Role       string
	Content    string
	UserID     string
	UserName   string
	MessageID  string
	ToolCalls  string
	ToolCallID string
	SourceKey  string
	Timestamp  time.Time
}

// Store は会話ログ・スナップショット・活動追跡の契約。
// adapter/store/conversation.DBStore が実装する。
type Store interface {
	LogTurn(ctx context.Context, entry TurnEntry) error
	TrackActivity(ctx context.Context, channelID string, at time.Time) error
	SaveSnapshot(ctx context.Context, sourceKey string, messages []llm.Message) error
	LoadSnapshot(ctx context.Context, sourceKey string) ([]llm.Message, error)
	DeleteChannel(ctx context.Context, channelID string) error
}
