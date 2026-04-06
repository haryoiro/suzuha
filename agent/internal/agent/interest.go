package agent

import "github.com/haryoiro/suzuha/internal/event"

// ShouldRespond determines whether the agent should respond to an event.
// Rule-based priority: mentions/replies always respond.
// For other messages, returns false (batch LLM scoring is a future enhancement).
func ShouldRespond(e event.Event, botID string) bool {
	// Always respond to CLI messages.
	if e.Source == "cli" {
		return true
	}

	// Always respond to triggers.
	if e.Type == "trigger" {
		return true
	}

	// Always respond to DMs.
	if e.Message.IsDM {
		return true
	}

	// Always respond to mentions.
	if e.Message.IsMention {
		return true
	}

	// Check if bot ID is mentioned in content.
	if botID != "" && containsBotMention(e.Message.Content, botID) {
		return true
	}

	// For regular channel messages, let the LLM decide.
	return true
}

// isDirectlyAddressed returns true if the event is a DM, mention, CLI input, or trigger.
func isDirectlyAddressed(e event.Event, botID string) bool {
	if e.Source == "cli" || e.Type == "trigger" || e.Type == event.TypeSelfPrompt {
		return true
	}
	if e.Message.IsDM {
		return true
	}
	if e.Message.IsMention {
		return true
	}
	if botID != "" && containsBotMention(e.Message.Content, botID) {
		return true
	}
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
