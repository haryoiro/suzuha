package admin

import (
	"context"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
)

func (h *AdminHandler) HealthCheck(ctx context.Context) (*gen.HealthCheckOK, error) {
	return &gen.HealthCheckOK{Status: "ok"}, nil
}
