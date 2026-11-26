// Package kit holds the small drawing pieces every screen is built from:
// lines made of styled spans, size bars, tabs, the footer.
package kit

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/romus/space/internal/ui/theme"
)

// Span is a piece of plain text with a style.
type Span struct {
	Text  string
	Style lipgloss.Style
}

// Sp makes a span.
func Sp(st lipgloss.Style, text string) Span { return Span{Text: text, Style: st} }

// Width is the display width of plain text.
func Width(s string) int { return ansi.StringWidth(s) }

// Fit truncates text to w columns, with an ellipsis, and pads it to w.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-Width(s)))
}

// FitLeft keeps the end of the text: useful for long paths.
func FitLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := Width(s); n > w {
		s = "…" + ansi.TruncateLeft(s, n-w+1, "")
	}
	return s + strings.Repeat(" ", max(0, w-Width(s)))
}

// FitRight right-aligns text in w columns.
func FitRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", max(0, w-Width(s))) + s
}

// Cell is a left-aligned span of exactly w columns.
func Cell(st lipgloss.Style, s string, w int) Span { return Span{Fit(s, w), st} }

// CellR is a right-aligned span of exactly w columns.
func CellR(st lipgloss.Style, s string, w int) Span { return Span{FitRight(s, w), st} }

func spansWidth(spans []Span) int {
	n := 0
	for _, s := range spans {
		n += Width(s.Text)
	}
	return n
}

func render(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Text != "" {
			b.WriteString(s.Style.Render(s.Text))
		}
	}
	return b.String()
}

// cut shortens a list of spans to at most w columns.
func cut(spans []Span, w int) []Span {
	var out []Span
	for _, s := range spans {
		if w <= 0 {
			break
		}
		n := Width(s.Text)
		if n > w {
			s.Text = ansi.Truncate(s.Text, w, "…")
			n = Width(s.Text)
		}
		out = append(out, s)
		w -= n
	}
	return out
}

// Row lays out left spans and right spans on a line exactly w columns wide.
// The gap between them is filled with the fill style. When the line is too
// narrow, the left side gives way first.
func Row(w int, fill lipgloss.Style, left, right []Span) string {
	if w <= 0 {
		return ""
	}
	rw := spansWidth(right)
	if rw > w {
		right = cut(right, w)
		rw = spansWidth(right)
	}
	if lw := spansWidth(left); lw+rw > w {
		left = cut(left, w-rw)
	}
	gap := w - spansWidth(left) - rw
	return render(left) + fill.Render(strings.Repeat(" ", max(0, gap))) + render(right)
}

// Blank is an empty line of the window background.
func Blank(t *theme.Theme, w int) string {
	return t.Base().Render(strings.Repeat(" ", max(0, w)))
}

// Rule is a horizontal line.
func Rule(t *theme.Theme, w int) string {
	return t.S(t.Rule).Render(strings.Repeat("─", max(0, w)))
}

// Center places text in the middle of a line.
func Center(t *theme.Theme, w int, spans ...Span) string {
	n := spansWidth(spans)
	if n > w {
		spans = cut(spans, w)
		n = spansWidth(spans)
	}
	left := (w - n) / 2
	return t.Base().Render(strings.Repeat(" ", left)) + render(spans) +
		t.Base().Render(strings.Repeat(" ", max(0, w-n-left)))
}

// --- bars -------------------------------------------------------------------

var eighths = []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// Bar draws a horizontal bar for a fraction of cols columns, precise to an
// eighth of a column.
func Bar(frac float64, cols int) string {
	if math.IsNaN(frac) || frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	e := int(math.Round(frac * float64(cols) * 8))
	if e == 0 && frac > 0 {
		e = 1 // anything that exists gets a sliver
	}
	return strings.Repeat("█", e/8) + eighths[e%8]
}

// Gauge draws a used/free meter: filled blocks, then light shade.
func Gauge(used, total int64, cols int) string {
	if total <= 0 || cols <= 0 {
		return strings.Repeat("░", max(0, cols))
	}
	n := int(math.Round(float64(used) / float64(total) * float64(cols)))
	n = min(max(n, 0), cols)
	return strings.Repeat("█", n) + strings.Repeat("░", cols-n)
}

// --- tabs -------------------------------------------------------------------

// Tab is one tab in a tab bar.
type Tab struct {
	Label string
	Badge string // a count shown after the label
	Mark  bool   // draw the badge in the recipe colour
}

// Tabs draws a tab bar and the rule under it. The rule is heavier under the
// active tab: that is the underline of the design.
func Tabs(t *theme.Theme, w int, tabs []Tab, active int, right []Span) (line, rule string) {
	var left []Span
	start, length, x := 0, 0, 0
	for i, tab := range tabs {
		text := tab.Label
		st := t.S(t.Muted)
		if i == active {
			st = t.S(t.Strong).Bold(true)
		}
		left = append(left, Sp(t.Base(), " "), Sp(st, text))
		n := Width(text)
		if tab.Badge != "" {
			bst := st
			if tab.Mark && i != active {
				bst = t.S(t.Recipe)
			}
			left = append(left, Sp(t.Base(), " "), Sp(bst, tab.Badge))
			n += 1 + Width(tab.Badge)
		}
		left = append(left, Sp(t.Base(), " "))
		if i == active {
			start, length = x+1, n
		}
		x += n + 2
	}
	line = Row(w, t.Base(), left, right)
	start = min(start, w)
	length = min(length, w-start)
	rule = t.S(t.Rule).Render(strings.Repeat("─", start)) +
		t.S(t.Strong).Render(strings.Repeat("━", length)) +
		t.S(t.Rule).Render(strings.Repeat("─", max(0, w-start-length)))
	return line, rule
}

// --- footer -----------------------------------------------------------------

// Hint is a key and what it does.
type Hint struct{ Key, Label string }

// KeyCap draws the primary key with a thin frame.
func KeyCap(t *theme.Theme, key string) []Span {
	return []Span{
		Sp(t.S(t.KeyBorder), "▏"),
		Sp(t.S(t.Text), key),
		Sp(t.S(t.KeyBorder), "▕"),
	}
}

// Chip draws a small framed label, like a filter value.
func Chip(t *theme.Theme, text string, focused bool) []Span {
	border, fg := t.ChipBorder, t.Subtle
	st := t.S(fg)
	if focused {
		border, fg = t.Strong, t.Strong
		st = t.S(fg).Bold(true)
	}
	return []Span{Sp(t.S(border), "▏"), Sp(st, text), Sp(t.S(border), "▕")}
}

// Footer draws the bottom line: key hints on the left, a summary and the
// primary action on the right.
func Footer(t *theme.Theme, w int, hints []Hint, summary []Span, key, action string) string {
	var left []Span
	for i, h := range hints {
		if i > 0 {
			left = append(left, Sp(t.Base(), "  "))
		}
		left = append(left, Sp(t.S(t.Text), h.Key), Sp(t.S(t.Muted), " "+h.Label))
	}
	var right []Span
	right = append(right, summary...)
	if action != "" {
		if len(summary) > 0 {
			right = append(right, Sp(t.Base(), "  "))
		}
		right = append(right, KeyCap(t, key)...)
		right = append(right, Sp(t.S(t.Strong).Bold(true), " "+action))
	}
	if len(left) > 0 && len(right) > 0 {
		left = append(left, Sp(t.Base(), "  "))
	}
	return Row(w, t.Base(), left, right)
}

// --- box --------------------------------------------------------------------

// Box frames pre-rendered lines. Every line must be exactly w-4 columns.
func Box(t *theme.Theme, w int, lines []string) []string {
	if w < 4 {
		return lines
	}
	b := t.S(t.BoxBorder)
	out := []string{b.Render("╭" + strings.Repeat("─", w-2) + "╮")}
	for _, l := range lines {
		out = append(out, b.Render("│")+t.Base().Render(" ")+l+t.Base().Render(" ")+b.Render("│"))
	}
	return append(out, b.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

// --- frame ------------------------------------------------------------------

// Frame stacks top lines, a filler and bottom lines into exactly h lines.
// When there is no room, the top gives way: the footer always stays.
func Frame(t *theme.Theme, w, h int, top, bottom []string) []string {
	if len(bottom) > h {
		bottom = bottom[len(bottom)-h:]
	}
	room := h - len(bottom)
	if len(top) > room {
		top = top[:room]
	}
	out := make([]string, 0, h)
	out = append(out, top...)
	for i := len(top); i < room; i++ {
		out = append(out, Blank(t, w))
	}
	return append(out, bottom...)
}

// Window keeps a cursor inside a scrolling list and returns the new offset.
func Window(cur, off, total, visible int) int {
	if visible <= 0 || total <= visible {
		return 0
	}
	if cur < off {
		off = cur
	}
	if cur >= off+visible {
		off = cur - visible + 1
	}
	return min(max(off, 0), total-visible)
}

// Spinner is the frame of a braille spinner.
func Spinner(frame int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[((frame%len(frames))+len(frames))%len(frames)]
}
