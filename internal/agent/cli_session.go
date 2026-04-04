package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// CLISession は CLI (stdin/stdout) の Session 実装。
type CLISession struct {
	agentCtx *Context
	out      io.Writer
	logger   *slog.Logger
}

// NewCLISession は新しい CLISession を作成する。
func NewCLISession(agentCtx *Context, out io.Writer, logger *slog.Logger) *CLISession {
	return &CLISession{
		agentCtx: agentCtx,
		out:      out,
		logger:   logger,
	}
}

func (s *CLISession) Source() SourceKey    { return SourceKeyCLI }
func (s *CLISession) Context() *Context    { return s.agentCtx }
func (s *CLISession) PersistKey() string   { return "cli" }
func (s *CLISession) BeginTurn(*Perception) {}

// DirectiveConfig は CLI 固有のパイプライン設定を返す。
func (s *CLISession) DirectiveConfig() DirectiveConfig {
	return DirectiveConfig{
		ForceRespond:       true,
		DrainWindow:        1 * time.Second,
		SkipChannelFilter:  true,
		SkipCatchUpStale:   true,
		SkipChannelHistory: true,
	}
}

// Respond はテキストを stdout に書き込む。
func (s *CLISession) Respond(_ context.Context, text string) error {
	_, err := fmt.Fprintln(s.out, text)
	return err
}
