package preferences

import (
	"context"
	"database/sql"
	_ "embed"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

//go:embed migration.sql
var migrationSQL string

// Feature is the self-contained preferences feature.
type Feature struct {
	store *Store
}

// New creates a Preferences Feature.
func New(db *sql.DB) *Feature {
	return &Feature{
		store: NewStore(db),
	}
}

func (f *Feature) Name() string { return "preferences" }

func (f *Feature) Setup(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migrationSQL)
	return err
}

func (f *Feature) Tools() []tool.Tool {
	return []tool.Tool{
		NewRegisterPreferenceTool(f.store),
		NewListPreferencesTool(f.store),
	}
}

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{
		NewEvalTask(f.store),
	}
}

var _ scheduler.Feature = (*Feature)(nil)
