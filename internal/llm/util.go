package llm

// EstimateTokens provides a rough estimate of the number of tokens in a given text.
// It uses the common heuristic of 4 characters per token.
func EstimateTokens(text string) int {
	return len(text) / 4
}

// EstimateHistoryTokens estimates the total number of tokens in a conversation history.
func EstimateHistoryTokens(messages []Message) int {
	var total int
	for _, msg := range messages {
		total += EstimateTokens(msg.Content)
	}
	return total
}

// PruneHistory keeps the context window within the specified limit by removing older messages.
// System messages (especially the first one) are generally preserved if possible.
func PruneHistory(messages []Message, maxTokens int) []Message {
	if EstimateHistoryTokens(messages) <= maxTokens {
		return messages
	}

	// Keep the first message if it's a system prompt
	var systemPrompt Message
	hasSystemPrompt := false
	if len(messages) > 0 && messages[0].Role == RoleSystem {
		systemPrompt = messages[0]
		hasSystemPrompt = true
		messages = messages[1:]
	}

	// Remove older messages until we are under the limit
	// We remove from the front of the remaining slice.
	for len(messages) > 0 {
		messages = messages[1:]
		total := EstimateHistoryTokens(messages)
		if hasSystemPrompt {
			total += EstimateTokens(systemPrompt.Content)
		}
		if total <= maxTokens {
			break
		}
	}

	if hasSystemPrompt {
		messages = append([]Message{systemPrompt}, messages...)
	}

	return messages
}
