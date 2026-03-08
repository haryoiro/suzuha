package admin

import (
	"context"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

func (h *AdminHandler) HealthCheck(ctx context.Context) (*api.HealthCheckOK, error) {
	return &api.HealthCheckOK{Status: "ok"}, nil
}
