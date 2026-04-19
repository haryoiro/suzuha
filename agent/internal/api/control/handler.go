package control

import (
	"context"

	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/channel"
)

// Handler は control API の ogen Handler 実装。
// 各メソッドは gen が定義する interface を満たす。
type Handler struct {
	gen.UnimplementedHandler
	channelStore *channel.Store
}

// NewHandler は Control API のハンドラを生成する。
func NewHandler(channelStore *channel.Store) *Handler {
	return &Handler{channelStore: channelStore}
}

// RuntimeReloadChannelSettings implements POST /internal/reload-channel-settings.
func (h *Handler) RuntimeReloadChannelSettings(ctx context.Context) (*gen.OkResponse, error) {
	if err := h.channelStore.Reload(ctx); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}
