package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Agent is the main event loop that processes events, calls the LLM,
// executes tools, and sends responses.
type Agent struct {
	ctx     *Context
	llm     *llm.Client
	tools   *tool.Registry
	memory  memory.Store
	bus     *event.Bus
	chat    chat.Interface
	consol  consolidator.Client
	logger  *slog.Logger
	metrics *observe.Metrics

	systemPrompt     string
	botID            string
	contextWindowPct float64
}

// Config holds agent configuration.
type Config struct {
	SystemPrompt     string
	BotID            string
	ContextWindowPct float64 // trigger compaction at this ratio (e.g. 0.8)
	MaxContextTokens int
}

// New creates an Agent.
func New(
	cfg Config,
	llmClient *llm.Client,
	tools *tool.Registry,
	memStore memory.Store,
	bus *event.Bus,
	chatIface chat.Interface,
	consolClient consolidator.Client,
	logger *slog.Logger,
	metrics *observe.Metrics,
) *Agent {
	agentCtx := NewContext(cfg.MaxContextTokens)

	// Inject system prompt as first message.
	if cfg.SystemPrompt != "" {
		agentCtx.Add(llm.Message{
			Role:      "system",
			Content:   cfg.SystemPrompt,
			Timestamp: time.Now(),
		})
	}

	return &Agent{
		ctx:              agentCtx,
		llm:              llmClient,
		tools:            tools,
		memory:           memStore,
		bus:              bus,
		chat:             chatIface,
		consol:           consolClient,
		logger:           logger,
		metrics:          metrics,
		systemPrompt:     cfg.SystemPrompt,
		botID:            cfg.BotID,
		contextWindowPct: cfg.ContextWindowPct,
	}
}

// Run starts the agent event loop. Blocks until ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	events := a.bus.Subscribe()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-events:
			if a.metrics != nil {
				a.metrics.EventsTotal.WithLabelValues(evt.Source, evt.Type).Inc()
			}
			if err := a.handleEvent(ctx, evt); err != nil {
				a.logger.Error("handle event failed", "event_id", evt.ID, "error", err)
			}
		}
	}
}

func (a *Agent) handleEvent(ctx context.Context, evt event.Event) error {
	// 1. Convert event to message and add to context.
	msg := eventToMessage(evt)
	a.ctx.Add(msg)

	// 2. Check if we should respond.
	if !ShouldRespond(evt, a.botID) {
		return nil
	}

	// 3. Check context window usage — compact if needed.
	if a.contextWindowPct > 0 && a.ctx.UsageRatio() > a.contextWindowPct {
		a.compact(ctx)
	}

	// 4. Retrieve relevant long-term memories.
	if a.memory != nil {
		a.injectMemories(ctx, msg.Content)
	}

	// 5. LLM completion with tool loop.
	resp, err := a.completeWithTools(ctx)
	if err != nil {
		return fmt.Errorf("agent: complete: %w", err)
	}

	// 6. Add assistant response to context.
	a.ctx.Add(llm.Message{
		Role:      "assistant",
		Content:   resp.Text,
		Timestamp: time.Now(),
		ToolCalls: resp.ToolCalls,
	})

	// 7. Send response.
	if resp.Text != "" {
		channel, _ := evt.Payload["channel"].(string)
		if err := a.chat.Send(ctx, channel, resp.Text); err != nil {
			return fmt.Errorf("agent: send: %w", err)
		}
	}

	return nil
}

// completeWithTools runs the LLM and executes tool calls in a loop.
func (a *Agent) completeWithTools(ctx context.Context) (*llm.Response, error) {
	allTools := a.tools.All()
	maxIter := 10

	for range maxIter {
		resp, err := a.llm.Complete(ctx, a.ctx.Messages(), allTools)
		if err != nil {
			return nil, err
		}

		if !resp.HasToolCalls() {
			return resp, nil
		}

		// Add assistant message with tool calls.
		a.ctx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Timestamp: time.Now(),
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call.
		for _, tc := range resp.ToolCalls {
			t, ok := a.tools.Get(tc.Function.Name)
			if !ok {
				a.ctx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: unknown tool %q", tc.Function.Name),
					ToolCallID: tc.ID,
					Timestamp:  time.Now(),
				})
				continue
			}

			if a.metrics != nil {
				a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "called").Inc()
			}

			result, err := t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				if a.metrics != nil {
					a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "error").Inc()
				}
				a.ctx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: %v", err),
					ToolCallID: tc.ID,
					Timestamp:  time.Now(),
				})
				continue
			}

			if a.metrics != nil {
				status := "success"
				if result.IsError {
					status = "error"
				}
				a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, status).Inc()
			}

			// Serialize tool result as content string.
			content := ""
			for _, c := range result.Content {
				content += c.Text
			}
			a.ctx.Add(llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Timestamp:  time.Now(),
			})
		}
	}

	return nil, fmt.Errorf("agent: tool loop exceeded %d iterations", maxIter)
}

// compact requests the consolidator to compress the context.
func (a *Agent) compact(ctx context.Context) {
	msgs := a.ctx.Messages()
	target := len(msgs) / 2

	if a.consol != nil {
		result, err := a.consol.Compact(ctx, &consolidator.CompactRequest{
			Messages:    msgs,
			TargetCount: target,
		})
		if err != nil {
			a.logger.Warn("consolidator compact failed, falling back to truncation", "error", err)
			a.ctx.TruncateOldest(target)
			return
		}
		a.ctx.KeepOnly(result.KeepIndices)
		return
	}

	// No consolidator available — simple truncation fallback.
	a.ctx.TruncateOldest(target)
}

// injectMemories searches long-term memory and injects relevant results.
func (a *Agent) injectMemories(ctx context.Context, query string) {
	memories, err := a.memory.Search(ctx, query, 3)
	if err != nil {
		a.logger.Debug("memory search failed", "error", err)
		return
	}

	if len(memories) == 0 {
		return
	}

	content := "Relevant memories:\n"
	for _, m := range memories {
		content += fmt.Sprintf("- [%s] %s\n", m.Type, m.Content)
	}

	a.ctx.Add(llm.Message{
		Role:      "system",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// eventToMessage converts an event to an llm.Message.
func eventToMessage(evt event.Event) llm.Message {
	content, _ := evt.Payload["content"].(string)
	userID, _ := evt.Payload["user_id"].(string)
	userName, _ := evt.Payload["user_name"].(string)
	channel, _ := evt.Payload["channel"].(string)

	return llm.Message{
		Role:      "user",
		Content:   content,
		UserID:    userID,
		UserName:  userName,
		Channel:   channel,
		Timestamp: evt.Timestamp,
	}
}
