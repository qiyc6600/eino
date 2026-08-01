package contextmgr

// TrimByToken implements "keep the most recent N tokens" sliding window strategy.
// System messages are always preserved. Tool call/result pairs are never split.
func TrimByToken(messages []Message, maxTokens int, counter TokenCounter) []Message {
	// Separate system messages and non-system messages
	var systemMsgs []Message
	var nonSystem []Message
	for _, m := range messages {
		if m.IsSystem || m.Role == "system" {
			systemMsgs = append(systemMsgs, m)
		} else {
			nonSystem = append(nonSystem, m)
		}
	}

	// Calculate system message tokens
	systemTokens := counter.CountMessages(systemMsgs)
	remainingTokens := maxTokens - systemTokens
	if remainingTokens <= 0 {
		// Only keep system messages
		return systemMsgs
	}

	// Keep non-system messages from newest to oldest until we hit the token limit
	var kept []Message
	totalTokens := 0
	for i := len(nonSystem) - 1; i >= 0; i-- {
		msgTokens := counter.CountMessage(nonSystem[i])
		if totalTokens+msgTokens > remainingTokens {
			break
		}
		kept = append([]Message{nonSystem[i]}, kept...)
		totalTokens += msgTokens
	}

	// Apply tool pair guard
	kept = GuardToolPairs(kept)

	// Reassemble
	result := make([]Message, 0, len(systemMsgs)+len(kept))
	result = append(result, systemMsgs...)
	result = append(result, kept...)
	return result
}
