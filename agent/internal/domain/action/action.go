// Package action は予約アクション (Scheduled Action) のドメイン型を定義する。
// 複数 package (feature/action, api/admin) から共有されるため domain/ に集約している。
package action

import "time"

// Action は予約済みアクション 1 行を表す。
type Action struct {
	ID            string
	ChannelID     string
	Content       string
	Mode          string // "direct" (投稿をそのまま送る) / "prompt" (LLM に生成させる)
	ScheduledAt   time.Time
	CronExpr      string // empty なら one-shot
	RandomMinutes int    // ランダムオフセット窓 (分)。0 で無効
	CreatedBy     string
	Status        string // pending / executed / canceled / failed
	RetryCount    int
	ExecutedAt    *time.Time
	CreatedAt     time.Time
}

// ListOpts は List のフィルタリングオプション。
type ListOpts struct {
	Status string
	Limit  int
}

// UpdateFields は部分更新のフィールド。nil は「更新しない」を意味する。
type UpdateFields struct {
	ChannelID   *string
	Content     *string
	Mode        *string
	ScheduledAt *string
	CronExpr    *string
	Status      *string
}
