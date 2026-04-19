package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/runtime/agent"
)

// Session は CLI (stdin/stdout) の agent.Session 実装。
type Session struct {
	agentCtx *agent.Context
	out      io.Writer
	logger   *slog.Logger
}

// NewSession は新しい CLI Session を作成する。
func NewSession(agentCtx *agent.Context, out io.Writer, logger *slog.Logger) *Session {
	return &Session{
		agentCtx: agentCtx,
		out:      out,
		logger:   logger,
	}
}

func (s *Session) Source() agent.SourceKey      { return agent.SourceKeyCLI }
func (s *Session) Context() *agent.Context      { return s.agentCtx }
func (s *Session) PersistKey() string           { return "cli" }
func (s *Session) BeginTurn(*agent.Perception)  {}

// DirectiveConfig は CLI 固有のパイプライン設定を返す。
func (s *Session) DirectiveConfig() agent.DirectiveConfig {
	return agent.DirectiveConfig{
		ForceRespond:       true,
		DrainWindow:        1 * time.Second,
		SkipChannelFilter:  true,
		SkipCatchUpStale:   true,
		SkipChannelHistory: true,
	}
}

// Respond はテキストを stdout に書き込む。
func (s *Session) Respond(_ context.Context, text string) error {
	_, err := fmt.Fprintln(s.out, text)
	return err
}
