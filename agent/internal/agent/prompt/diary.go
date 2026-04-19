package prompt

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

// DiaryEntry は日記エントリの最小表現 (consumer-side type)。
type DiaryEntry struct {
	PeriodStart time.Time
	Content     string
}

// DiaryReader は日記エントリを取得する (consumer-side interface)。
type DiaryReader interface {
	ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]DiaryEntry, error)
}

// DiaryProvider は直近の日記エントリをコンテキストに注入する。
type DiaryProvider struct {
	Reader DiaryReader
	Logger *slog.Logger
}

func (p *DiaryProvider) ProvideContext(ctx context.Context, _ Request) Block {
	if p.Reader == nil {
		return Block{}
	}
	entries, err := p.Reader.ListByKind(ctx, "hourly", jtime.Now().Add(-12*time.Hour), 24)
	if err != nil {
		p.Logger.Debug("日記を取得できなかった", "error", err)
		return Block{}
	}
	if len(entries) == 0 {
		return Block{}
	}

	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	var sb strings.Builder
	sb.WriteString("[直近12時間の日記]\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "- [%s] %s\n", jtime.In(e.PeriodStart).Format("2006-01-02 15:00"), e.Content)
	}

	return Block{Background: []llm.Message{{
		Role: "system", Content: sb.String(), Timestamp: jtime.Now(),
	}}}
}
