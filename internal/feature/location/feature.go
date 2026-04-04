package location

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the self-contained location tracking feature.
type Feature struct {
	store *Store
}

// NewFeature creates a Location Feature.
func NewFeature(store *Store) *Feature {
	return &Feature{store: store}
}

func (f *Feature) Name() string { return "location" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error {
	return f.store.Setup(context.Background())
}

func (f *Feature) Tools() []tool.Tool {
	return []tool.Tool{
		NewGetLocationTool(f.store),
		NewGetLocationHistoryTool(f.store),
		&ReverseGeocode{},
	}
}

func (f *Feature) Tasks() []scheduler.CronTask {
	return nil
}

var _ scheduler.Feature = (*Feature)(nil)
