package contextmgr

import (
	"unicode"
)

// TokenCounter provides token counting capability for messages.
type TokenCounter interface {
	CountMessage(msg Message) int
	CountMessages(messages []Message) int
}

// SimpleTokenCounter is a basic token estimator.
// Chinese: ~1.5 chars per token. English: ~0.75 words per token.
// Each message adds a fixed role overhead.
type SimpleTokenCounter struct {
	RoleOverhead int // tokens added per message for role formatting, default 4
}

// NewSimpleTokenCounter creates a SimpleTokenCounter with defaults.
func NewSimpleTokenCounter() *SimpleTokenCounter {
	return &SimpleTokenCounter{
		RoleOverhead: 4,
	}
}

// CountMessage estimates the token count for a single message.
func (c *SimpleTokenCounter) CountMessage(msg Message) int {
	return c.estimateTokens(msg.Content) + c.RoleOverhead
}

// CountMessages estimates the total token count for multiple messages.
func (c *SimpleTokenCounter) CountMessages(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += c.CountMessage(msg)
	}
	return total
}

// estimateTokens provides a rough token count for a text string.
func (c *SimpleTokenCounter) estimateTokens(text string) int {
	if text == "" {
		return 0
	}

	chineseCount := 0
	englishWords := 0
	inWord := false

	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			chineseCount++
			if inWord {
				englishWords++
				inWord = false
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			inWord = true
		} else {
			if inWord {
				englishWords++
				inWord = false
			}
		}
	}
	if inWord {
		englishWords++
	}

	// Chinese: ~1.5 chars per token
	// English: ~0.75 words per token (1 word ≈ 1.33 tokens)
	chineseTokens := float64(chineseCount) / 1.5
	englishTokens := float64(englishWords) * 1.33

	// Count punctuation and special characters
	specialCount := 0
	for _, r := range text {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			specialCount++
		}
	}

	return int(chineseTokens + englishTokens) + specialCount/3 + 1
}

// CountText estimates tokens for a raw text string.
func CountText(text string) int {
	counter := NewSimpleTokenCounter()
	return counter.estimateTokens(text)
}

// CountString is an alias for CountText.
func CountString(text string) int {
	return CountText(text)
}

// IsChinese checks if the text contains Chinese characters.
func IsChinese(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// isWordChar checks if a rune is a word character.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
