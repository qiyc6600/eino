package tools

import (
	"strings"
	"testing"
)

// TestFormatOrderList_ColumnsLineUp is the guard for the order table.
//
// The table is box-drawn, so every line has to occupy the same number of display
// columns. It did not: the padding used fmt's %-8s, which pads by bytes, and a CJK
// rune is three bytes but two columns. Measured on the previous version, the rules
// came out 58 columns, the header 60 and the data rows 61 — the separators landed
// in a different place on every line. The rules and the rows are now derived from
// the same measured widths, so this cannot drift again.
func TestFormatOrderList_ColumnsLineUp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		orders []Order
	}{
		{"ascii only", []Order{{ID: "A-1", Status: "pending", Amount: "1", Desc: "mouse"}}},
		{"chinese", DefaultOrders()[:3]},
		{"the widest row", DefaultOrders()},
		{"an over-long description", []Order{{
			ID: "A-9999", Status: "pending", Amount: "¥1.00",
			Desc: strings.Repeat("很长的商品描述", 6),
		}}},
		{"emoji status", []Order{{ID: "A-1", Status: "shipped", Amount: "¥1", Desc: "键盘"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := formatOrderList(tc.orders)
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

			// Only the table body: the heading above and the hint below are prose.
			var table []string
			for _, line := range lines {
				if strings.HasPrefix(line, "│") || strings.HasPrefix(line, "┌") ||
					strings.HasPrefix(line, "├") || strings.HasPrefix(line, "└") {
					table = append(table, line)
				}
			}
			if len(table) < 4 {
				t.Fatalf("expected a header, rules and rows, got %d table lines:\n%s", len(table), out)
			}

			want := displayWidth(table[0])
			for i, line := range table {
				if got := displayWidth(line); got != want {
					t.Errorf("line %d is %d columns, want %d (all lines must match for the "+
						"borders to line up)\n%s", i, got, want, out)
				}
				if got := len(line); got == want {
					// Not a failure, but worth knowing: it means this line happens to
					// be pure ASCII, so it does not exercise the width logic.
					continue
				}
			}

			// Every row must be bounded by the box characters, or a truncation has
			// eaten one.
			for i, line := range table {
				if !strings.HasPrefix(line, "│") && !strings.HasPrefix(line, "┌") &&
					!strings.HasPrefix(line, "├") && !strings.HasPrefix(line, "└") {
					t.Errorf("line %d lost its left border: %q", i, line)
				}
			}
		})
	}
}

// TestFormatOrderList_KeepsEveryOrder pins that laying the table out cannot drop a
// row: an over-long description is truncated, not omitted.
func TestFormatOrderList_KeepsEveryOrder(t *testing.T) {
	orders := DefaultOrders()
	out := formatOrderList(orders)
	for _, o := range orders {
		if !strings.Contains(out, o.ID) {
			t.Errorf("order %s is missing from the table", o.ID)
		}
	}
	// Data rows are the lines starting with the box character, minus the header.
	// Counting by ID prefix would only see the A-* ids and miss the B-* ones.
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "│") {
			rows++
		}
	}
	if rows-1 != len(orders) {
		t.Errorf("expected %d data rows plus a header, counted %d rows", len(orders), rows)
	}
}

// TestDisplayWidth covers the measurement the layout depends on.
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"A-1001", 6},
		{"待处理", 6},  // three Han runes, two columns each
		{"🟡待处理", 8}, // emoji two columns plus three Han
		{"¥199.00", 7},
		{"（已取消）", 10}, // fullwidth punctuation is wide too
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestPadRight covers padding and the truncation that keeps a long cell from
// pushing the borders out.
func TestPadRight(t *testing.T) {
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight(%q) = %q", "ab", got)
	}
	if got := padRight("待处理", 8); got != "待处理  " {
		t.Errorf("padRight(%q) = %q, want two spaces (6 columns + 2)", "待处理", got)
	}
	if got := padRight("abcdef", 4); displayWidth(got) != 4 {
		t.Errorf("a long cell must be truncated to the column, got %q (%d columns)", got, displayWidth(got))
	}
}
