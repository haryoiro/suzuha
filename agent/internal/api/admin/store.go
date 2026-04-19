package admin

import (
	"context"
	"time"

	actionDom "github.com/haryoiro/suzuha/internal/domain/action"
	diaryDom "github.com/haryoiro/suzuha/internal/domain/diary"
)

// ActionStore はスケジュールアクションの CRUD を提供する。
type ActionStore interface {
	List(ctx context.Context, opts actionDom.ListOpts) ([]actionDom.Action, error)
	Create(ctx context.Context, a *actionDom.Action) error
	Update(ctx context.Context, id string, fields actionDom.UpdateFields) error
	Delete(ctx context.Context, id string) error
}

// DiaryStore は日記エントリの読み取りアクセスを提供する。
type DiaryStore interface {
	ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]diaryDom.Entry, error)
}
