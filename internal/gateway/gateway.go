package gateway

import (
	"context"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
)

type registeredSource struct {
	source Source
	status SourceStatus
}

// Gateway はすべての Source のライフサイクルを管理し、ヘルス状態を追跡する。
type Gateway struct {
	mu      sync.RWMutex
	sources []registeredSource
	logger  *slog.Logger
}

// New は新しい Gateway を作成する。
func New(logger *slog.Logger) *Gateway {
	return &Gateway{
		logger: logger,
	}
}

// Register はソースを登録する。Run() の前に呼ぶこと。
func (g *Gateway) Register(s Source) {
	g.sources = append(g.sources, registeredSource{
		source: s,
		status: SourceStatus{
			Name:  s.Name(),
			State: StateStarting,
		},
	})
}

// Run はすべての登録済みソースを起動する。
// いずれかのソースがエラーで終了した場合、全ソースをキャンセルしてエラーを返す。
// ctx がキャンセルされるまでブロックする。
func (g *Gateway) Run(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)

	for i := range g.sources {
		idx := i
		eg.Go(func() error {
			return g.runSource(ctx, idx)
		})
	}

	return eg.Wait()
}

// Status は全ソースの現在の健全性を返す。
func (g *Gateway) Status() []SourceStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]SourceStatus, len(g.sources))
	for i, rs := range g.sources {
		result[i] = rs.status
	}
	return result
}

// SourceNames は登録済みソース名の一覧を返す。
func (g *Gateway) SourceNames() []string {
	names := make([]string, len(g.sources))
	for i, rs := range g.sources {
		names[i] = rs.source.Name()
	}
	return names
}

func (g *Gateway) runSource(ctx context.Context, idx int) error {
	src := g.sources[idx]
	g.logger.Info("ソース起動", "source", src.source.Name())

	g.updateState(idx, StateRunning)

	err := src.source.Run(ctx)

	if err != nil && ctx.Err() == nil {
		g.updateState(idx, StateError)
		g.setError(idx, err.Error())
		g.logger.Error("ソース異常終了", "source", src.source.Name(), "error", err)
		return err
	}

	g.updateState(idx, StateStopped)
	g.logger.Info("ソース停止", "source", src.source.Name())
	return nil
}

func (g *Gateway) updateState(idx int, state SourceState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sources[idx].status.State = state
	if state == StateRunning {
		now := jtime.Now()
		g.sources[idx].status.StartedAt = &now
	}
}

func (g *Gateway) setError(idx int, msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sources[idx].status.Error = msg
}
