package acquirer

import (
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/memento"
	"github.com/haryoiro/suzuha/internal/memory"
)

// compactSystemPromptBase はメモリ抽出用のベースシステムプロンプト。
// 抽出ルールは AcquireConfig.Rules を通じて動的に追加される。
const compactSystemPromptBase = `You are a memory extraction agent. Your job is to analyze a conversation and extract key information that should be stored as long-term memories.

IMPORTANT: Write all memory content in Japanese (日本語). The conversation is in Japanese, and memories must also be in Japanese.

Respond with a JSON array. Each element has these fields:

- "type": one of "user", "world", "tool", "episode", "self"
- "content": the memory text (Japanese, self-contained)
- "keywords": array of search keywords (names, places, entities, topic words)
- "topic": 「大カテゴリ/自由記述」形式。大カテゴリは 技術, 日常, 趣味, 仕事, 人間関係, 知識, その他 から選ぶ (例: "技術/Go", "日常/食事", "趣味/アニメ", "その他/天気")
- "persons": array of user IDs of people involved
- "event_time": ISO 8601 datetime if a specific time is mentioned, null otherwise
- "emotional_tone": emotion label for episodes (e.g. "楽しい"), omit for non-episodes
- "image_indices": array of message indices with relevant images, omit if none

Type guidelines:
- "user": facts/preferences about a specific person. persons must include their user_id.
- "world": general knowledge or facts from conversation.
- "tool": tool usage patterns or notable results.
- "episode": shared events involving multiple participants. Include all participant IDs in persons.
- "self": new self-awareness about the bot's own behavior. Only emit when genuinely new.

If the conversation has nothing worth extracting, return an empty array: []

IMPORTANT: The user_id can be found in message metadata (user_id=... in the message header).

OUTPUT FORMAT: You MUST respond with ONLY a valid JSON array. No markdown, no explanation, no text before or after the JSON. Just the raw JSON array.`

// buildSystemPrompt はベースプロンプトとルールから完全なシステムプロンプトを組み立てる。
func buildSystemPrompt(rules []ExtractionRule) string {
	var sb strings.Builder
	sb.WriteString(compactSystemPromptBase)
	for _, rule := range rules {
		section := rule.PromptSection()
		if section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
		}
	}
	return sb.String()
}

// buildCompactPrompt はメッセージとオプションの既存メモリコンテキストからユーザープロンプトを構築する。
func buildCompactPrompt(messages []memento.ConversationMessage, existingMemories []memory.Memory) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "以下の%d件のメッセージから、長期記憶として保存すべき情報を抽出してください。\n\n", len(messages))

	// 重複排除コンテキストとして既存メモリを含める。
	if len(existingMemories) > 0 {
		sb.WriteString("[既存メモリ（重複を避けるための参考。これらと同じ内容は抽出しないこと）]\n")
		for _, m := range existingMemories {
			fmt.Fprintf(&sb, "- [%s] %s\n", string(m.Type), m.Content)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("[会話メッセージ]\n")
	for i, m := range messages {
		imgTag := ""
		if len(m.MediaKeys) > 0 || len(m.ImageURLs) > 0 {
			imgTag = " [IMG]"
		}
		if m.UserID != "" {
			fmt.Fprintf(&sb, "[%d]%s %s (user_id=%s, platform=%s, name=%s): %s\n",
				i, imgTag, m.Role, m.UserID, m.Source, m.UserName, m.Content)
		} else {
			fmt.Fprintf(&sb, "[%d]%s %s: %s\n", i, imgTag, m.Role, m.Content)
		}
	}
	return sb.String()
}
