package agent

import "github.com/haryoiro/suzuha/internal/event"

// ShouldRespond determines whether the agent should respond to an event.
// Rule-based priority: mentions/replies always respond.
// For other messages, returns false (batch LLM scoring is a future enhancement).
func ShouldRespond(e event.Event, botID string) bool {
	payload := e.Payload

	// Always respond to CLI messages.
	if e.Source == "cli" {
		return true
	}

	// Always respond to triggers.
	if e.Type == "trigger" {
		return true
	}

	// Always respond to mentions.
	if isMention, ok := payload["is_mention"].(bool); ok && isMention {
		return true
	}

	// Check if bot ID is mentioned in content.
	if content, ok := payload["content"].(string); ok && botID != "" {
		if containsBotMention(content, botID) {
			return true
		}
	}

	// Default: don't respond to random messages.
	return false
}

func containsBotMention(content, botID string) bool {
	// Simple substring check for <@botID> pattern.
	mention := "<@" + botID + ">"
	for i := 0; i <= len(content)-len(mention); i++ {
		if content[i:i+len(mention)] == mention {
			return true
		}
	}
	return false
}
