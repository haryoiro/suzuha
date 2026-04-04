package prompt

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/feature/diary"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

type DiaryProvider struct {
	DB     *sql.DB
	Logger *slog.Logger
}

func (p *DiaryProvider) ProvideContext(ctx context.Context, _ Request) Block {
	if p.DB == nil {
		return Block{}
	}
	ds := diary.NewStore(p.DB)
	entries, err := ds.ListByKind(ctx, "hourly", jtime.Now().Add(-12*time.Hour), 24)
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
	sb.WriteString("Recent diary (past 12h):\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "- [%s] %s\n", e.PeriodStart.Format("2006-01-02T15:00"), e.Content)
	}

	return Block{Background: []llm.Message{{
		Role: "system", Content: sb.String(), Timestamp: jtime.Now(),
	}}}
}
