package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// TopicsTask implements scheduler.CronTask for periodic topic posting with opt-in mentions.
type TopicsTask struct{}

var _ scheduler.CronTask = (*TopicsTask)(nil)

func (t *TopicsTask) Name() string        { return "topics" }
func (t *TopicsTask) Description() string { return "定期的に話題を提供・メンション" }

func (t *TopicsTask) Setup(_ context.Context, _ *scheduler.CronContext) error {
	return nil
}

// topicsConfig holds task-specific configuration from config.yaml.
type topicsConfig struct {
	ChannelID string   `json:"channel_id"`
	Topics    []string `json:"topics"`
	PromptDir string   `json:"prompt_dir"`
}

// optInUser holds a user who opted in to mentions along with their Discord ID.
type optInUser struct {
	UserID         string
	DisplayName    string
	PlatformUserID string // Discord user ID for <@ID> mentions
}

func (t *TopicsTask) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var tc topicsConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &tc)
	}
	if tc.ChannelID == "" {
		cc.Logger.Warn("topics: no channel_id configured, skipping")
		return nil
	}
	if len(tc.Topics) == 0 {
		cc.Logger.Warn("topics: no topics configured, skipping")
		return nil
	}

	// 1. Load system prompt from IDENTITY.md / SOUL.md.
	systemPrompt := loadPromptFiles(tc.PromptDir)

	// 2. Get opt-in users.
	users, err := getOptInUsers(ctx, cc.DB)
	if err != nil {
		cc.Logger.Error("topics: get opt-in users", "error", err)
		// Continue without mentions.
		users = nil
	}

	// 3. Collect user memories for context.
	userContexts := buildUserContexts(ctx, cc, users)

	// 4. Pick a random topic.
	topic := tc.Topics[rand.IntN(len(tc.Topics))]

	// 5. Generate message via LLM.
	message, err := generateTopicMessage(ctx, cc, systemPrompt, topic, userContexts)
	if err != nil {
		cc.Logger.Error("topics: generate message", "error", err)
		return nil
	}

	// 6. Send notification.
	if err := cc.Notifier(ctx, tc.ChannelID, message, "topics"); err != nil {
		cc.Logger.Error("topics: notify", "error", err)
		return fmt.Errorf("topics: notify: %w", err)
	}

	// 7. Save to memory for history.
	mem := &memory.Memory{
		Type:    memory.MemoryTypeWorld,
		Content: fmt.Sprintf("話題提供: %s\n%s", topic, message),
		Metadata: map[string]any{
			"source": "topics",
			"topic":  topic,
		},
	}
	if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
		cc.Logger.Error("topics: save memory", "error", saveErr)
	}

	cc.Logger.Info("topics: posted", "topic", topic, "users", len(users))
	return nil
}

// loadPromptFiles reads IDENTITY.md and SOUL.md from the given directory.
func loadPromptFiles(dir string) string {
	if dir == "" {
		return ""
	}
	var parts []string
	for _, name := range []string{"IDENTITY.md", "SOUL.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		parts = append(parts, strings.TrimSpace(string(data)))
	}
	return strings.Join(parts, "\n\n")
}

// getOptInUsers queries users who have opted in to mentions.
func getOptInUsers(ctx context.Context, db *sql.DB) ([]optInUser, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.id, u.display_name, pl.platform_user_id
		FROM users u
		JOIN platform_links pl ON pl.user_id = u.id AND pl.platform = 'discord'
		WHERE json_extract(u.metadata, '$.mention_opt_in') = 1
		  AND u.is_bot = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []optInUser
	for rows.Next() {
		var u optInUser
		if err := rows.Scan(&u.UserID, &u.DisplayName, &u.PlatformUserID); err != nil {
			continue
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// userContext holds a user and their known memories.
type userContext struct {
	User     optInUser
	Memories []memory.Memory
}

// buildUserContexts retrieves known memories about each opt-in user.
func buildUserContexts(ctx context.Context, cc *scheduler.CronContext, users []optInUser) []userContext {
	var contexts []userContext
	for _, u := range users {
		query := u.DisplayName
		if query == "" {
			query = u.UserID
		}
		mems, err := cc.Memory.SearchByType(ctx, query, memory.MemoryTypeUser, 5)
		if err != nil {
			cc.Logger.Debug("topics: search user memories", "user", u.UserID, "error", err)
		}
		contexts = append(contexts, userContext{User: u, Memories: mems})
	}
	return contexts
}

// generateTopicMessage generates a natural topic message via LLM.
func generateTopicMessage(ctx context.Context, cc *scheduler.CronContext, systemPrompt, topic string, userContexts []userContext) (string, error) {
	var sb strings.Builder
	sb.WriteString("以下のトピックについて、チャンネルに投稿する話題を1つ生成してください。\n\n")

	fmt.Fprintf(&sb, "## トピック\n%s\n\n", topic)

	if len(userContexts) > 0 {
		sb.WriteString("## メンション可能なユーザー\n")
		sb.WriteString("以下のユーザーにはメンション（Discord形式: <@DiscordユーザーID>）してよいです。\n\n")
		for _, uc := range userContexts {
			name := uc.User.DisplayName
			if name == "" {
				name = uc.User.PlatformUserID
			}
			fmt.Fprintf(&sb, "- %s (<@%s>)\n", name, uc.User.PlatformUserID)
			if len(uc.Memories) > 0 {
				sb.WriteString("  既知情報:\n")
				for _, m := range uc.Memories {
					fmt.Fprintf(&sb, "  - %s\n", truncateStr(m.Content, 100))
				}
			} else {
				sb.WriteString("  既知情報: まだあまり知らない\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 方針\n")
	sb.WriteString("- ユーザーについて知らないことがあれば、それを自然に聞く形で話題にする\n")
	sb.WriteString("- 会話を促すような質問形式が望ましい\n")
	sb.WriteString("- 200文字以内で自然に\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return resp.Text, nil
}

// truncateStr shortens a string to maxRunes runes.
func truncateStr(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
