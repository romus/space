//go:build darwin

package screens

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/romus/space/internal/config"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// podmanPlan is the plan drawn on the Plan artboard.
func podmanPlan(home string) recipe.Plan {
	disk := filepath.Join(home, ".local/share/containers/podman/machine/applehv/podman-machine-default-arm64.raw")
	return recipe.Plan{
		RecipeID: "podman", Title: "Podman", Seconds: 40, Reclaim: 6_600_000_000,
		Metric: &recipe.Metric{Label: "machine disk on Mac", Before: 38_100_000_000, After: 31_500_000_000},
		Toggles: []recipe.Toggle{
			{Key: "stop", Label: "stop when done", On: true},
			{Key: "start", Label: "start again after"},
		},
		Steps: []recipe.Step{
			{Title: "Remove 6 images", Why: "Images no container is using.",
				Cmd: []string{"podman", "rmi", "3f9a1c07be52", "9e04d7aa1f30", "c81f2e4d9a07", "0b7de5c3f812", "e5a2190bd4c6", "a1b2c3d4e5f6"}},
			{Title: "Clean up dangling layers", Why: "Layers left without an image after step 1.",
				Cmd: []string{"podman", "image", "prune", "-f"}},
			{Title: "Tell your Mac which blocks are free", Safe: true,
				Why: "Only works while the machine runs — so it goes before stopping.",
				Cmd: []string{"podman", "machine", "ssh", "--", "sudo", "fstrim", "-av"}},
			{Title: "Stop the machine", Reversible: true,
				Why: "Can be turned off below. api-dev and db-dev will stop too.",
				Cmd: []string{"podman", "machine", "stop"}},
			{Title: "Check the result", ReadOnly: true, Why: "Compare size on disk before and after.",
				Cmd: []string{"du", "-h", disk}},
		},
	}
}

func testCtx(th *theme.Theme) *Ctx {
	home := "/Users/me"
	return &Ctx{
		Theme: th, W: 118, H: 29,
		Paths: config.Paths{Home: home, ConfigDir: home + "/.config/space", StateDir: home + "/.local/state/space"},
		State: &config.State{},
		Env:   recipe.Env{Home: home, Now: func() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) }},
		Root:  home,
	}
}

func planScreen(th *theme.Theme) *Plan {
	c := testCtx(th)
	plan := podmanPlan(c.Home())
	p := NewPlan(c, PlanSpec{
		Plan:    plan,
		Crumbs:  []string{"Recipes", "Podman"},
		Rebuild: func(recipe.Options) (recipe.Plan, error) { return plan, nil },
	})
	p.cur = 2 // the artboard highlights the third step
	return p
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/ui/screens -update", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs.\n--- got\n%s\n--- want\n%s\nIf the change is intended: go test ./internal/ui/screens -update", name, got, want)
	}
}

func TestPlanScreenLayout(t *testing.T) {
	p := planScreen(theme.DarkTheme())
	lines := p.Lines()
	if len(lines) != 29 {
		t.Fatalf("%d lines, want 29", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 118 {
			t.Fatalf("line %d is %d columns, want 118", i+1, w)
		}
	}
	var plain []string
	for _, l := range lines {
		plain = append(plain, strings.TrimRight(ansi.Strip(l), " "))
	}
	golden(t, "plan.golden", strings.Join(plain, "\n")+"\n")
}

// The two themes draw the same text; only the colours differ, and each
// colour is the one from its artboard.
func TestPlanScreenThemes(t *testing.T) {
	dark := strings.Join(planScreen(theme.DarkTheme()).Lines(), "\n")
	light := strings.Join(planScreen(theme.LightTheme()).Lines(), "\n")
	if ansi.Strip(dark) != ansi.Strip(light) {
		t.Fatal("light and dark must show the same text")
	}
	checks := []struct {
		what        string
		dark, light string // SGR colour parameters
	}{
		{"window background", "48;2;15;15;18", "48;2;255;255;255"},
		{"highlighted step", "48;2;28;28;32", "48;2;245;245;247"},
		{"strong text", "38;2;255;255;255", "38;2;0;0;0"},
		{"muted text", "38;2;139;139;147", "38;2;106;106;114"},
		{"IRREVERSIBLE", "38;2;255;122;110", "38;2;196;53;42"},
		{"SAFE", "38;2;127;200;169", "38;2;30;132;73"},
		{"rule", "38;2;35;35;40", "38;2;228;228;231"},
	}
	for _, c := range checks {
		if !strings.Contains(dark, c.dark) {
			t.Errorf("dark: %s (%s) is missing", c.what, c.dark)
		}
		if !strings.Contains(light, c.light) {
			t.Errorf("light: %s (%s) is missing", c.what, c.light)
		}
		if strings.Contains(dark, c.light) && c.dark != c.light {
			t.Errorf("dark theme uses the light colour of %s", c.what)
		}
	}
}

func pressMsg(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case " ":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
}

func press(s Screen, key string) Screen {
	next, _ := s.Update(pressMsg(key))
	return next
}

func TestPlanNeedsASecondEnterForIrreversibleSteps(t *testing.T) {
	p := planScreen(theme.DarkTheme())
	next, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("the first enter must not start anything")
	}
	if !strings.Contains(ansi.Strip(strings.Join(next.Lines(), "\n")), "this cannot be undone — press enter again") {
		t.Fatal("the screen must say why it is waiting")
	}
	// Any other key takes the confirmation back.
	next = press(next, "j")
	if next.(*Plan).confirm {
		t.Fatal("moving away cancels the confirmation")
	}
}

func TestPlanEditStep(t *testing.T) {
	p := planScreen(theme.DarkTheme())
	var s Screen = p
	s = press(s, "e")
	if !s.Typing() {
		t.Fatal("e starts editing the command")
	}
	for i := 0; i < len(" -av"); i++ {
		s = press(s, "backspace")
	}
	for _, r := range " -v '/var lib'" {
		s = press(s, string(r))
	}
	s = press(s, "enter")
	got := s.(*Plan).spec.Plan.Steps[2].Cmd
	want := []string{"podman", "machine", "ssh", "--", "sudo", "fstrim", "-v", "/var lib"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("edited command: %q", got)
	}
	if !strings.Contains(ansi.Strip(strings.Join(s.Lines(), "\n")), "EDITED · SAFE") {
		t.Fatal("an edited step is labelled")
	}
}

func TestSplitCommand(t *testing.T) {
	cases := map[string][]string{
		`podman rmi a b`:            {"podman", "rmi", "a", "b"},
		`du -h '/Users/me/My Disk'`: {"du", "-h", "/Users/me/My Disk"},
		`echo "it's" 'say "hi"'`:    {"echo", "it's", `say "hi"`},
		`path\ with\ spaces  extra`: {"path with spaces", "extra"},
		`trash '~/it'\''s.zip'`:     {"trash", "~/it's.zip"},
		`empty ''`:                  {"empty", ""},
		`   `:                       nil,
	}
	for in, want := range cases {
		got := splitCommand(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("splitCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
