package admin

import (
	"context"
	"time"
)

// ActionStore はスケジュールアクションの CRUD を提供する。
type ActionStore interface {
	List(ctx context.Context, opts ActionListOpts) ([]Action, error)
	Create(ctx context.Context, a *Action) error
	Update(ctx context.Context, id string, fields ActionUpdateFields) error
	Delete(ctx context.Context, id string) error
}

// Action はスケジュールアクション行を表す。
type Action struct {
	ID            string
	ChannelID     string
	Content       string
	Mode          string
	ScheduledAt   time.Time
	CronExpr      string
	RandomMinutes int
	CreatedBy     string
	Status        string
	RetryCount    int
	ExecutedAt    *time.Time
	CreatedAt     time.Time
}

// ActionListOpts は List のフィルタリングオプション。
type ActionListOpts struct {
	Status string
	Limit  int
}

// ActionUpdateFields は部分更新のフィールド。
type ActionUpdateFields struct {
	ChannelID   *string
	Content     *string
	Mode        *string
	ScheduledAt *string
	CronExpr    *string
	Status      *string
}

// DiaryStore は日記エントリの読み取りアクセスを提供する。
type DiaryStore interface {
	ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]DiaryEntry, error)
}

// DiaryEntry は日記エントリ。
type DiaryEntry struct {
	ID          string
	Kind        string
	Content     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CreatedAt   time.Time
}

