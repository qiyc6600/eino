package tools

import (
	"strings"
	"unicode"
)

// Box-drawing text is a presentation choice that only works in a fixed-width font,
// so the padding has to be measured in display columns rather than bytes. Go's
// fmt pads by bytes — "%-19s" on a Chinese description adds seven spaces where
// eleven were needed — which is what made the order table's borders miss each
// other (measured: the border rows came out 58 columns, the header 60, the data
// rows 61).

// wideRune reports whether a rune occupies two terminal columns.
//
// Emoji are counted as wide because that is how they render in the browsers and
// terminals this project targets. The judgement is font-dependent for emoji
// (some fonts draw them one column wide), so the table keeps them out of the
// columns that must line up where it can — see formatStatus.
func wideRune(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r),
		unicode.Is(unicode.Hangul, r):
		return true
	// Fullwidth forms and fullwidth punctuation.
	case r >= 0xFF01 && r <= 0xFF60, r >= 0xFFE0 && r <= 0xFFE6:
		return true
	// The emoji blocks, which terminals render double-width.
	case r >= 0x1F300 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF:
		return true
	}
	return false
}

// DisplayWidth counts the columns a string occupies.
// DisplayWidth counts the columns a string occupies.
//
// Exported because the title cutter needs the same judgement: a 16-character
// Chinese title and a 16-character English one do not take the same room.
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		if wideRune(r) {
			w += 2
			continue
		}
		w++
	}
	return w
}

// padRight pads s to exactly cols display columns, truncating with an ellipsis when
// it is longer so a long cell cannot push the borders out of line.
func padRight(s string, cols int) string {
	w := DisplayWidth(s)
	if w == cols {
		return s
	}
	if w > cols {
		return truncateToCols(s, cols)
	}
	return s + strings.Repeat(" ", cols-w)
}

// padCenter centres s in cols display columns, biasing the extra space left.
func padCenter(s string, cols int) string {
	w := DisplayWidth(s)
	if w >= cols {
		return padRight(s, cols)
	}
	left := (cols - w) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", cols-w-left)
}

// truncateToCols cuts s to at most cols display columns, ending with an ellipsis
// when anything was dropped.
func truncateToCols(s string, cols int) string {
	if cols <= 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := 1
		if wideRune(r) {
			rw = 2
		}
		if used+rw > cols-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// columnWidths measures each column from its content, so the borders are derived
// from the same numbers as the rows and cannot drift apart.
func columnWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = DisplayWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if w := DisplayWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

// renderTableRow lays out one row: each cell padded to its column width and
// surrounded by the box separators.
func renderTableRow(cells []string, widths []int, center bool) string {
	var b strings.Builder
	b.WriteString("│")
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		b.WriteString(" ")
		if center {
			b.WriteString(padCenter(cell, w))
		} else {
			b.WriteString(padRight(cell, w))
		}
		b.WriteString(" │")
	}
	return b.String()
}

// renderTableBorder draws a rule from the same widths: left+right wrap the row,
// mid separates columns.
func renderTableBorder(widths []int, left, mid, right string) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range widths {
		if i > 0 {
			b.WriteString(mid)
		}
		b.WriteString(strings.Repeat("─", w+2))
	}
	b.WriteString(right)
	return b.String()
}
