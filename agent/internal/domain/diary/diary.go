// Package diary は会話要約エントリ (hourly / daily) のドメイン型を定義する。
// admin API と capability/memory (将来の task_summarize) から共有される。
package diary

import "time"

// EntryKind は日記エントリの種別。
type EntryKind string

const (
	// EntryKindHourly は毎時の要約。
	EntryKindHourly EntryKind = "hourly"
	// EntryKindDaily は 1 日分の要約。
	EntryKindDaily EntryKind = "daily"
)

// Entry は 1 件の会話要約エントリ。
type Entry struct {
	ID          string
	Kind        string // EntryKind の string 表現 ("hourly" / "daily")
	Content     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CreatedAt   time.Time
}

// Period は要約の対象期間。
type Period struct {
	Start time.Time
	End   time.Time
}
