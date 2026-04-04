package device

import "context"

// Source は device.Hub を gateway.Source として扱うためのラッパー。
// Hub は HTTP ハンドラベースで Run() ループを持たないため、
// Run() は ctx がキャンセルされるまでブロックするだけ。
type Source struct {
	hub *Hub
}

// NewSource は Hub をラップする Source を作成する。
func NewSource(hub *Hub) *Source {
	return &Source{hub: hub}
}

// Name は gateway.Source を満たす。
func (s *Source) Name() string { return "device" }

// Run は ctx がキャンセルされるまでブロックする。
// Hub の WebSocket ハンドラは別途 HTTP mux に登録される。
func (s *Source) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
