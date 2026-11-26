//go:build darwin

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/romus/space/internal/config"
	"github.com/romus/space/internal/ui/screens"
	"github.com/romus/space/internal/ui/theme"
)

// write creates a file of a given size and age inside the sandbox home.
func write(t *testing.T, home, rel string, size int, daysOld int) {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-time.Duration(daysOld) * 24 * time.Hour)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// sandbox builds a small home directory with files, a cache, personal data
// and Claude Code sessions.
func sandbox(t *testing.T) config.Paths {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPACE_HOME", home)
	const mb = 1 << 20
	write(t, home, "Movies/recording.mov", 12*mb, 200)
	write(t, home, "Downloads/installer.dmg", 8*mb, 400)
	write(t, home, "Downloads/small.txt", 4096, 1)
	write(t, home, "Documents/thesis.docx", 3*mb, 2)
	write(t, home, ".npm/_cacache/blob", 5*mb, 30)
	write(t, home, "Projects/web/node_modules/lib/index.js", 2*mb, 5)

	session := func(project, cwd, id string, size, days int) {
		rel := filepath.Join(".claude/projects", project, id+".jsonl")
		write(t, home, rel, size, days)
		path := filepath.Join(home, rel)
		head := []byte(`{"type":"user","cwd":"` + cwd + `"}` + "\n")
		data, _ := os.ReadFile(path)
		copy(data, head)
		when := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		_ = os.WriteFile(path, data, 0o644)
		_ = os.Chtimes(path, when, when)
	}
	session("-work-api", filepath.Join(home, "work/api"), "old-session", 4*mb, 90)
	session("-work-api", filepath.Join(home, "work/api"), "new-session", 2*mb, 1)
	write(t, home, ".claude/projects/-work-api/memory/MEMORY.md", 100, 90)
	write(t, home, ".claude/settings.json", 100, 90)

	paths, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// harness drives the app without a terminal: it feeds messages and runs the
// commands they return, the way the Bubble Tea runtime would.
type harness struct {
	t   *testing.T
	app *App
}

func newHarness(t *testing.T, mode theme.Mode) *harness {
	paths := sandbox(t)
	app := New(Options{Root: paths.Home, Mode: mode, Paths: paths, State: config.LoadState(paths)})
	h := &harness{t: t, app: app}
	h.run(app.init)
	h.send(tea.WindowSizeMsg{Width: 122, Height: 30})
	h.settle()
	return h
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.app.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil, tickMsg, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		h.send(msg)
	}
}

// settle lets the scan finish and the screen catch up.
func (h *harness) settle() {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.app.c.Frame++
		h.run(h.app.broadcast(screens.TickMsg{}))
		if strings.Contains(h.screen(), "by size on disk") || len(h.app.stack) > 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := 0; i < 6; i++ {
		h.run(h.app.broadcast(screens.TickMsg{}))
	}
}

func (h *harness) key(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "left":
			msg = tea.KeyPressMsg{Code: tea.KeyLeft}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		default:
			r := []rune(k)
			msg = tea.KeyPressMsg{Code: r[0], Text: k}
		}
		h.send(msg)
	}
}

// screen is what the user sees, without colour codes.
func (h *harness) screen() string { return ansi.Strip(h.app.View().Content) }

func (h *harness) want(parts ...string) {
	h.t.Helper()
	s := h.screen()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			h.t.Fatalf("screen does not show %q:\n%s", p, s)
		}
	}
}

func (h *harness) wantNot(parts ...string) {
	h.t.Helper()
	s := h.screen()
	for _, p := range parts {
		if strings.Contains(s, p) {
			h.t.Fatalf("screen shows %q but should not:\n%s", p, s)
		}
	}
}

// cursorTo moves the cursor on the Folders tab to the row with a name.
func (h *harness) cursorTo(name string) {
	h.t.Helper()
	h.key("g")
	for i := 0; i < 40; i++ {
		for _, line := range strings.Split(h.screen(), "\n") {
			// The detail panel names the current row by its path.
			if strings.Contains(line, "~/"+name+"  ") {
				return
			}
		}
		h.key("down")
	}
	h.t.Fatalf("no row %q:\n%s", name, h.screen())
}

func TestEveryLineFillsTheWindow(t *testing.T) {
	h := newHarness(t, theme.Dark)
	check := func(where string) {
		t.Helper()
		lines := strings.Split(h.screen(), "\n")
		if len(lines) != 30 {
			t.Fatalf("%s: %d lines, want 30:\n%s", where, len(lines), h.screen())
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != 122 {
				t.Fatalf("%s: line %d is %d columns wide, want 122: %q", where, i+1, w, l)
			}
		}
	}
	check("folders")
	h.key("tab")
	check("files")
	h.key("tab")
	check("recipes")
	h.key("enter")
	check("agents")
	h.key("s")
	check("sessions")
	h.key("esc", "enter")
	check("plan")
	h.key("esc", "esc", "?")
	check("help")
}

func TestOverviewShowsFoldersBySize(t *testing.T) {
	h := newHarness(t, theme.Dark)
	h.want("space", "Folders", "Files", "Recipes 1", "by size on disk", "free of")
	h.want("Movies/", "Downloads/", "Documents/", ".npm/", ".claude/")
	h.want("npm cache", "personal", "Claude Code")
	t.Log("\n" + h.screen())

	s := h.screen()
	if strings.Index(s, "Movies/") > strings.Index(s, "Documents/") {
		t.Fatalf("Movies (12 MB) must come before Documents (3 MB):\n%s", s)
	}
}

func TestFilesTabAndFilters(t *testing.T) {
	h := newHarness(t, theme.Light)
	h.key("tab")
	h.want("FILE", "ON DISK", "LAST OPENED", "REMOVED VIA", "≥ 500 MB")
	h.wantNot("recording.mov")                // 12 MB is under the default filter
	h.key("f", "down", "down", "down", "esc") // ≥ 10 MB
	h.want("≥ 10 MB", "recording.mov", "~/Movies", "Trash")
	h.wantNot("installer.dmg")
	t.Log("\n" + h.screen())
}

func TestMarkPlanRunMovesToTrash(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home

	h.cursorTo("Movies")
	h.key("right") // open Movies
	h.want("recording.mov")
	h.key("space")
	h.want("1 marked", "plan")
	h.key("enter")
	h.want("Here’s what will happen", "Move 1 item to the Trash", "REVERSIBLE", "nothing has run yet")
	h.want("$ trash ~/Movies/recording.mov")
	t.Log("\n" + h.screen())

	if _, err := os.Stat(filepath.Join(home, "Movies/recording.mov")); err != nil {
		t.Fatal("the plan screen must not change anything:", err)
	}
	h.key("enter") // run
	h.want("Moved", "to the Trash", "back to overview", "empty Trash")
	t.Log("\n" + h.screen())

	if _, err := os.Stat(filepath.Join(home, "Movies/recording.mov")); !os.IsNotExist(err) {
		t.Fatal("the file should be gone from Movies")
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash/recording.mov")); err != nil {
		t.Fatal("the file should be in the sandbox Trash:", err)
	}
	logs, _ := filepath.Glob(filepath.Join(h.app.c.Paths.StateDir, "*.log"))
	if len(logs) != 1 {
		t.Fatalf("want one log file, got %v", logs)
	}

	h.key("enter") // back to overview, rescan
	h.settle()
	h.want("Folders", "by size on disk")
}

func TestPersonalAndRecipeRowsCannotBeMarked(t *testing.T) {
	h := newHarness(t, theme.Dark)
	h.cursorTo("Documents")
	h.key("space")
	h.want("Personal data is view only")
	h.wantNot("1 marked")

	h.cursorTo(".claude")
	h.key("space")
	h.want("belongs to a recipe")
	h.wantNot("1 marked")
}

func TestPermanentDeleteAsksTwice(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home
	h.cursorTo("Downloads")
	h.key("right")
	h.key("D")
	h.want("Delete 1 item forever", "IRREVERSIBLE")
	h.key("enter")
	h.want("this cannot be undone")
	if _, err := os.Stat(filepath.Join(home, "Downloads/installer.dmg")); err != nil {
		t.Fatal("one enter must not delete:", err)
	}
	h.key("enter")
	if _, err := os.Stat(filepath.Join(home, "Downloads/installer.dmg")); !os.IsNotExist(err) {
		t.Fatal("the file should be deleted after the second enter")
	}
}

func TestAgentsRecipeKeepsRecentSessionsAndMemory(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home

	h.cursorTo(".claude")
	h.key("r")
	h.want("AI agents", "keep", "last 30 days", "PROJECT", "TO REMOVE", "~/work/api", "never touch", "settings.json")
	h.want("1 session ·")
	t.Log("\n" + h.screen())

	h.key("enter")
	h.want("Remove 1 session from ~/work/api", "REVERSIBLE")
	h.key("enter")
	h.want("Moved", "to the Trash")

	project := filepath.Join(home, ".claude/projects/-work-api")
	if _, err := os.Stat(filepath.Join(project, "old-session.jsonl")); !os.IsNotExist(err) {
		t.Fatal("the old session should be gone")
	}
	for _, keep := range []string{"new-session.jsonl", "memory/MEMORY.md"} {
		if _, err := os.Stat(filepath.Join(project, keep)); err != nil {
			t.Fatalf("%s must be kept: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/settings.json")); err != nil {
		t.Fatal("settings.json must be kept:", err)
	}
}

func TestPinnedProjectIsKept(t *testing.T) {
	h := newHarness(t, theme.Dark)
	h.key("tab", "tab", "enter")
	h.want("[✓]")
	h.key("p")
	h.want(" ★ ", "0 sessions ·")
	h.key("enter")
	h.want("nothing to remove")
}

func TestThemeSwitching(t *testing.T) {
	h := newHarness(t, theme.System)

	// System follows what the terminal reports.
	h.send(tea.BackgroundColorMsg{Color: theme.LightTheme().Bg})
	if h.app.c.Theme.Name != "light" {
		t.Fatalf("system on a light terminal: got %s", h.app.c.Theme.Name)
	}
	h.send(tea.BackgroundColorMsg{Color: theme.DarkTheme().Bg})
	if h.app.c.Theme.Name != "dark" {
		t.Fatalf("system on a dark terminal: got %s", h.app.c.Theme.Name)
	}
	if got := h.app.View().BackgroundColor; got != theme.DarkTheme().Bg {
		t.Fatalf("window background: got %v", got)
	}

	// t cycles system → light → dark and the choice is remembered.
	h.key("t")
	if h.app.c.Mode != theme.Light || h.app.c.Theme.Name != "light" {
		t.Fatalf("after t: mode %s theme %s", h.app.c.Mode, h.app.c.Theme.Name)
	}
	h.want("Theme: light")
	if got := h.app.View().BackgroundColor; got != theme.LightTheme().Bg {
		t.Fatalf("light window background: got %v", got)
	}
	h.key("t")
	if h.app.c.Mode != theme.Dark {
		t.Fatalf("after t t: mode %s", h.app.c.Mode)
	}
	// A forced theme ignores the terminal.
	h.send(tea.BackgroundColorMsg{Color: theme.LightTheme().Bg})
	if h.app.c.Theme.Name != "dark" {
		t.Fatal("a forced dark theme must not follow the terminal")
	}
	if got := config.LoadState(h.app.c.Paths).Theme; got != "dark" {
		t.Fatalf("saved theme: got %q", got)
	}
}

func TestTooSmallWindow(t *testing.T) {
	h := newHarness(t, theme.Dark)
	h.send(tea.WindowSizeMsg{Width: 60, Height: 12})
	h.want("at least 80 × 20")
}
