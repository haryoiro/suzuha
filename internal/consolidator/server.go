package consolidator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// Server implements the consolidation logic.
// It uses an LLM to decide which messages to keep and what to extract
// as long-term memories.
type Server struct {
	llmClient *llm.Client
	store     memory.Store
	logger    *slog.Logger
}

// NewServer creates a consolidator server.
func NewServer(llmClient *llm.Client, store memory.Store, logger *slog.Logger) *Server {
	return &Server{
		llmClient: llmClient,
		store:     store,
		logger:    logger,
	}
}

// Compact analyzes messages and decides what to keep vs. store as long-term memory.
func (s *Server) Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error) {
	if len(req.Messages) == 0 {
		return &CompactResult{}, nil
	}

	// Build a prompt asking the LLM to select important messages and extract memories.
	prompt := buildCompactPrompt(req.Messages, req.TargetCount)

	resp, err := s.llmClient.CompleteRaw(ctx, []providers.Message{
		{Role: "system", Content: compactSystemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("consolidator: compact llm call: %w", err)
	}

	result := parseCompactResponse(resp.Text, len(req.Messages))

	// Save extracted memories to long-term store.
	for i := range result.Memories {
		mem := &result.Memories[i]
		if err := s.store.Save(ctx, mem); err != nil {
			s.logger.Warn("consolidator: save memory failed", "error", err)
		}
	}

	return result, nil
}

const compactSystemPrompt = `You are a memory consolidation agent. Your job is to analyze a conversation and:
1. Select which messages are most important to keep in short-term context.
2. Extract key information that should be stored as long-term memories.

Respond in this exact format:
KEEP: 0,2,5,7 (comma-separated message indices to keep)
MEMORIES:
- [user] Information about user preferences or facts
- [world] General world knowledge or facts discussed
- [tool] Tool usage patterns or results worth remembering`

func buildCompactPrompt(messages []llm.Message, targetCount int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here are %d messages. Select approximately %d to keep.\n\n", len(messages), targetCount)
	for i, m := range messages {
		fmt.Fprintf(&sb, "[%d] %s: %s\n", i, m.Role, m.Content)
	}
	return sb.String()
}

func parseCompactResponse(text string, msgCount int) *CompactResult {
	result := &CompactResult{}

	lines := strings.Split(text, "\n")
	inMemories := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "KEEP:") {
			indices := strings.TrimPrefix(line, "KEEP:")
			for _, part := range strings.Split(indices, ",") {
				part = strings.TrimSpace(part)
				var idx int
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 0 && idx < msgCount {
					result.KeepIndices = append(result.KeepIndices, idx)
				}
			}
			continue
		}

		if strings.HasPrefix(line, "MEMORIES:") {
			inMemories = true
			continue
		}

		if inMemories && strings.HasPrefix(line, "- ") {
			content := strings.TrimPrefix(line, "- ")
			memType := memory.MemoryTypeWorld
			if strings.HasPrefix(content, "[user]") {
				memType = memory.MemoryTypeUser
				content = strings.TrimPrefix(content, "[user]")
			} else if strings.HasPrefix(content, "[world]") {
				memType = memory.MemoryTypeWorld
				content = strings.TrimPrefix(content, "[world]")
			} else if strings.HasPrefix(content, "[tool]") {
				memType = memory.MemoryTypeTool
				content = strings.TrimPrefix(content, "[tool]")
			}
			content = strings.TrimSpace(content)
			if content != "" {
				result.Memories = append(result.Memories, memory.Memory{
					Type:    memType,
					Content: content,
				})
			}
		}
	}

	return result
}
