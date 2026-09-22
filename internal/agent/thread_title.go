package agent

import (
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/tools"
)

// maxThreadTitleWidth is how wide a derived title may be, measured in display
// columns rather than characters. A rune count would give Chinese titles half the
// room of English ones — sixteen Han characters are twice as wide as sixteen
// Latin ones — and the sidebar shows both.
const maxThreadTitleWidth = 32

// DeriveThreadTitle makes a conversation title out of the message that started
// it, the way chat clients do when they have no model call to spare.
//
// It is deliberately mechanical — trim, take the first sentence, cut to width —
// because every "smarter" rule tried here mangles something: stripping a leading
// "请" turns "请假的流程" into "假的流程". A real title needs a model to read the
// conversation, which is why this is documented as the fallback rather than the
// goal.
//
// An empty result means "no title": the caller leaves the thread untitled and
// the interface shows its ID.
func DeriveThreadTitle(message string) string {
	// Newlines are the most common sentence break in a pasted prompt, and they
	// would otherwise collapse into the middle of the label.
	title := strings.Join(strings.Fields(strings.ReplaceAll(message, "\n", " ")), " ")
	if title == "" {
		return ""
	}

	// Prefer the first sentence when the message has one: "帮我看看这个报错。日志
	// 在下面……" titles better as "帮我看看这个报错" than as a mid-word cut.
	if cut := firstSentenceEnd(title); cut > 0 {
		title = strings.TrimSpace(title[:cut])
	}

	return truncateToWidth(title, maxThreadTitleWidth)
}

// firstSentenceEnd returns the byte offset of the first sentence-ending mark, or
// 0 when there is none worth cutting at. The mark itself is left out of the
// title: "帮我看看这个报错。" reads as a sentence, and a label does not need its
// full stop.
//
// A mark inside the first few characters is ignored: "好的。" would otherwise
// title a thread "好的".
//
// ASCII '.' is not treated as an end mark: it appears inside version numbers,
// file names and decimals, and cutting at "v1." is worse than cutting at width.
func firstSentenceEnd(title string) int {
	const minRunes = 4
	for i, r := range title {
		switch r {
		case '。', '！', '？', '；', '!', '?', ';':
		default:
			continue
		}
		if len([]rune(title[:i])) < minRunes {
			return 0
		}
		return i
	}
	return 0
}

// truncateToWidth cuts a title to at most width display columns, appending an
// ellipsis when it had to. Runes are never split: cutting by bytes would produce
// mojibake.
func truncateToWidth(title string, width int) string {
	if tools.DisplayWidth(title) <= width {
		return title
	}
	// One column is reserved for the ellipsis, which is itself wide.
	const ellipsisWidth = 2
	limit := width - ellipsisWidth
	var b strings.Builder
	used := 0
	for _, r := range title {
		w := tools.DisplayWidth(string(r))
		if used+w > limit {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// TitleFromFirstUserMessage picks the message a title is derived from: the first
// thing the user said in the thread, which is what the conversation is about.
func TitleFromFirstUserMessage(messages []*schema.Message) string {
	for _, m := range messages {
		if m.Role != schema.User {
			continue
		}
		if title := DeriveThreadTitle(m.Content); title != "" {
			return title
		}
	}
	return ""
}
