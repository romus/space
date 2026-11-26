package kit

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/romus/space/internal/ui/theme"
)

// The numbers come from the Main artboard: 30 columns, 148.2 GB is the
// largest folder.
func TestBarMatchesTheDesign(t *testing.T) {
	cases := []struct {
		gb   float64
		want string
	}{
		{148.2, strings.Repeat("█", 30)},
		{38.4, "███████▊"}, // 38.4/148.2 × 240 eighths = 62 = 7 full + 6/8
		{31.7, "██████▍"},
		{2.9, "▋"}, // 4.7 eighths rounds to 5: a little more than half a column
		{0, ""},
	}
	for _, c := range cases {
		if got := Bar(c.gb/148.2, 30); got != c.want {
			t.Errorf("%.1f GB: got %q (%d cols), want %q", c.gb, got, Width(got), c.want)
		}
	}
	if Bar(0.0001, 30) != "▏" {
		t.Error("anything that exists gets a sliver")
	}
	if Bar(5, 10) != strings.Repeat("█", 10) || Bar(-1, 10) != "" {
		t.Error("fractions are clamped")
	}
}

func TestGauge(t *testing.T) {
	// 412 of 494 GB used on 24 columns, as in the header of the artboard.
	if got, want := Gauge(412, 494, 24), strings.Repeat("█", 20)+strings.Repeat("░", 4); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if Gauge(1, 0, 4) != "░░░░" {
		t.Fatal("an unknown total is an empty gauge")
	}
}

func TestFit(t *testing.T) {
	if got := Fit("Library", 10); got != "Library   " {
		t.Fatalf("%q", got)
	}
	if got := Fit("podman-machine-default-arm64.raw", 12); got != "podman-mach…" || Width(got) != 12 {
		t.Fatalf("%q", got)
	}
	if got := FitRight("38.1 GB", 10); got != "   38.1 GB" {
		t.Fatalf("%q", got)
	}
	if got := FitLeft("~/Library/Application Support/MobileSync", 16); Width(got) != 16 || !strings.HasSuffix(got, "MobileSync") {
		t.Fatalf("a long path keeps its end: %q", got)
	}
	// Wide characters take two columns.
	if got := Fit("日本語のファイル", 6); Width(got) != 6 {
		t.Fatalf("%q is %d columns", got, Width(got))
	}
	if Fit("x", 0) != "" {
		t.Fatal("zero width")
	}
}

func TestRowIsAlwaysExactlyWide(t *testing.T) {
	th := theme.DarkTheme()
	left := []Span{Sp(th.S(th.Text), "a long label on the left side")}
	right := []Span{Sp(th.S(th.Muted), "right side")}
	for _, w := range []int{80, 40, 20, 12, 5, 1} {
		got := ansi.StringWidth(Row(w, th.Base(), left, right))
		if got != w {
			t.Errorf("width %d: got %d", w, got)
		}
	}
	// The right side survives when space runs out.
	if s := ansi.Strip(Row(20, th.Base(), left, right)); !strings.HasSuffix(s, "right side") {
		t.Errorf("the right side must stay: %q", s)
	}
}

func TestTabsUnderlineTheActiveTab(t *testing.T) {
	th := theme.DarkTheme()
	tabs := []Tab{{Label: "Folders"}, {Label: "Files"}, {Label: "Recipes", Badge: "3", Mark: true}}
	line, rule := Tabs(th, 40, tabs, 1, nil)
	plain, under := ansi.Strip(line), ansi.Strip(rule)
	if ansi.StringWidth(plain) != 40 || ansi.StringWidth(under) != 40 {
		t.Fatalf("widths: %d %d", ansi.StringWidth(plain), ansi.StringWidth(under))
	}
	start := strings.Index(plain, "Files")
	heavy := strings.Index(under, "━")
	// Both strings are ASCII up to the tab, so byte offsets are columns.
	if heavy/len("─") != start || strings.Count(under, "━") != len("Files") {
		t.Fatalf("the heavy rule must sit under the active tab:\n%s\n%s", plain, under)
	}
}

func TestFrameKeepsTheFooter(t *testing.T) {
	th := theme.LightTheme()
	top := []string{"1", "2", "3", "4", "5"}
	bottom := []string{"rule", "footer"}
	got := Frame(th, 10, 4, top, bottom)
	if len(got) != 4 || got[2] != "rule" || got[3] != "footer" {
		t.Fatalf("the footer stays when there is no room: %q", got)
	}
	got = Frame(th, 10, 9, top, bottom)
	if len(got) != 9 || got[8] != "footer" || got[4] != "5" {
		t.Fatalf("short content is padded in the middle: %q", got)
	}
}

func TestWindow(t *testing.T) {
	cases := []struct{ cur, off, total, visible, want int }{
		{0, 0, 100, 10, 0},
		{9, 0, 100, 10, 0},
		{10, 0, 100, 10, 1},
		{50, 0, 100, 10, 41},
		{3, 20, 100, 10, 3},
		{99, 0, 100, 10, 90},
		{2, 5, 4, 10, 0},
	}
	for _, c := range cases {
		if got := Window(c.cur, c.off, c.total, c.visible); got != c.want {
			t.Errorf("Window(%d,%d,%d,%d) = %d, want %d", c.cur, c.off, c.total, c.visible, got, c.want)
		}
	}
}

func TestNoColorDrawsNoColour(t *testing.T) {
	th := theme.DarkTheme()
	th.NoColor = true
	line := Row(30, th.Base(), []Span{Sp(th.S(th.Recipe), "◆ Podman")}, nil) +
		Footer(th, 30, []Hint{{"q", "quit"}}, nil, "⏎", "plan") + Rule(th, 10)
	if strings.Contains(line, "38;2") || strings.Contains(line, "48;2") {
		t.Fatalf("colour codes leaked: %q", line)
	}
	if !strings.Contains(th.Sel(th.Strong).Render("x"), "\x1b[7m") {
		t.Fatal("without colour the current row is shown in reverse video")
	}
}
