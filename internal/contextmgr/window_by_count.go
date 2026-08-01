package contextmgr

// TrimByCount implements "keep the most recent N messages" sliding window strategy.
// System messages are always preserved. Tool call/result pairs are never split.
func TrimByCount(messages []Message, maxMessages int) []Message {
	if len(messages) <= maxMessages {
		return messages
	}

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

	// Trim non-system messages from the front, keeping the most recent
	if len(nonSystem) > maxMessages {
		nonSystem = nonSystem[len(nonSystem)-maxMessages:]
	}

	// Apply tool pair guard
	nonSystem = GuardToolPairs(nonSystem)

	// Reassemble: system messages first, then trimmed non-system
	result := make([]Message, 0, len(systemMsgs)+len(nonSystem))
	result = append(result, systemMsgs...)
	result = append(result, nonSystem...)
	return result
}
