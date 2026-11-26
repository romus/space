//go:build darwin

package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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
	write(t, home, "Projects/data/stuck.bin", 3*mb, 10)
	write(t, home, "Projects/free.bin", 2*mb, 10)

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
	// reverse runs the commands of a batch last to first. A batch promises
	// no order, so the app must work either way.
	reverse bool
	// raw is every escape sequence the app wrote to the terminal directly.
	raw []string
}

func newHarness(t *testing.T, mode theme.Mode) *harness {
	return newHarnessAt(t, mode, func(home string) string { return home })
}

// newHarnessAt opens the app on a folder chosen relative to the sandbox home.
func newHarnessAt(t *testing.T, mode theme.Mode, root func(home string) string) *harness {
	paths := sandbox(t)
	app := New(Options{Root: root(paths.Home), Mode: mode, Paths: paths, State: config.LoadState(paths)})
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
	case tea.RawMsg:
		// Bubble Tea formats the sequence when it writes it.
		h.raw = append(h.raw, fmt.Sprint(msg.Msg))
	case tea.BatchMsg:
		if h.reverse {
			for i := len(msg) - 1; i >= 0; i-- {
				h.run(msg[i])
			}
			return
		}
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

// cursorToName moves the cursor to a row by its name, wherever the folder
// is: the detail panel ends the path of the current row with two spaces.
func (h *harness) cursorToName(name string) {
	h.t.Helper()
	h.key("g")
	for i := 0; i < 40; i++ {
		for _, line := range strings.Split(h.screen(), "\n") {
			if strings.Contains(line, "/"+name+"  ") {
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
	h.want("npm cache", "Claude Code")
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

// Coming back from a removal the folder that was open is open again, not
// the folder space was started in.
func TestAfterRemovingYouStayInTheFolder(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home

	h.cursorTo("Downloads")
	h.key("right")
	h.cursorToName("small.txt")
	h.key("space", "enter", "enter")
	h.want("Moved", "to the Trash")
	if _, err := os.Stat(filepath.Join(home, "Downloads/small.txt")); !os.IsNotExist(err) {
		t.Fatal("the file should be gone from Downloads")
	}

	h.key("enter")                     // back to overview, rescan
	h.wantNot("Movies/", "Documents/") // not even before the scan gets there
	h.settle()
	h.want("space  ~/Downloads/", "installer.dmg")
	h.wantNot("Movies/", "small.txt")

	// It is the real folder, not a picture of it: left goes up from here.
	h.key("left")
	h.want("Movies/", "Downloads/")
}

func TestAfterRemovingDeepInsideYouStayThere(t *testing.T) {
	h := newHarness(t, theme.Dark)

	h.cursorTo("Projects")
	h.key("right")
	h.cursorToName("web")
	h.key("right")
	h.cursorToName("node_modules")
	h.key("right")
	h.want("space  ~/Projects/web/node_modules/", "lib/")
	h.key("space", "enter", "enter")
	h.want("Done", "back to overview")

	h.key("enter")
	h.settle()
	h.want("space  ~/Projects/web/node_modules/", "This folder is empty")
	h.wantNot("lib/")
}

// The open folder itself can be what was removed: mark it, step inside,
// run. Then the nearest folder that is still there opens.
func TestAfterRemovingTheOpenFolderYouLandInItsParent(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home

	h.cursorTo("Projects")
	h.key("right")
	h.cursorToName("data")
	h.key("space")
	h.cursorToName("data")
	h.key("right")
	h.want("space  ~/Projects/data/", "stuck.bin")
	h.key("enter", "enter")
	h.want("Moved", "to the Trash")
	if _, err := os.Stat(filepath.Join(home, "Projects/data")); !os.IsNotExist(err) {
		t.Fatal("the folder should be gone")
	}

	h.key("enter")
	h.settle()
	h.want("space  ~/Projects/", "free.bin", "web/")
	h.wantNot("data/", "Movies/")
}

// ○ marks a folder that cannot be removed whole. What is inside it is
// ordinary: it can be marked and removed like anything else.
func TestProtectedFoldersKeepTheCircleButNotTheirContents(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home

	row := func(name string) string {
		t.Helper()
		for _, line := range strings.Split(h.screen(), "\n") {
			if strings.Contains(line, " "+name+"/ ") && strings.Contains(line, "%") {
				return line
			}
		}
		t.Fatalf("no row %q:\n%s", name, h.screen())
		return ""
	}
	for _, name := range []string{"Documents", "Downloads", "Movies"} {
		if !strings.Contains(row(name), "○ "+name+"/") {
			t.Errorf("%s is protected and should carry ○: %q", name, row(name))
		}
	}
	if !strings.Contains(row(".npm"), "● .npm/") || !strings.Contains(row(".claude"), "◆ .claude/") {
		t.Errorf("cache and recipe marks: %q / %q", row(".npm"), row(".claude"))
	}
	if strings.ContainsAny(row("Projects"), "○●◆") {
		t.Errorf("a plain folder has no mark: %q", row("Projects"))
	}
	h.wantNot("personal")

	// The folder itself cannot be marked.
	h.cursorTo("Documents")
	h.key("space")
	h.want("Too important to remove whole — open it and pick what to remove inside")
	h.wantNot("1 marked")

	// Inside, nothing carries the circle and everything can go.
	h.key("right")
	h.want("thesis.docx")
	h.wantNot("○", "personal", "view only")
	h.key("space")
	h.want("1 marked")
	h.key("enter")
	h.want("Move 1 item to the Trash", "$ trash ~/Documents/thesis.docx")
	h.key("enter")
	h.want("Moved", "to the Trash")
	if _, err := os.Stat(filepath.Join(home, "Documents/thesis.docx")); !os.IsNotExist(err) {
		t.Fatal("the document should be gone from Documents")
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash/thesis.docx")); err != nil {
		t.Fatal("the document should be in the sandbox Trash:", err)
	}
}

func TestRecipeRowsCannotBeMarked(t *testing.T) {
	h := newHarness(t, theme.Dark)
	h.cursorTo(".claude")
	h.key("space")
	h.want("belongs to a recipe")
	h.wantNot("1 marked")
}

// What cannot be removed is not blocked up front: space tries, removes the
// rest, and says which item stayed and why.
func TestWhatCouldNotBeRemovedIsListedWithTheReason(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes everything")
	}
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home
	locked := filepath.Join(home, "Projects/data")
	// Nothing can be moved out of a read-only folder.
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	h.cursorTo("Projects")
	h.key("right")
	h.cursorToName("data")
	h.key("right")
	h.want("stuck.bin")
	h.key("space", "left")
	h.cursorToName("free.bin")
	h.key("space")
	h.want("2 marked")
	h.key("enter")
	h.want("Move 2 items to the Trash")
	h.key("enter")

	h.want("1 item could not be removed", "~/Projects/data/stuck.bin",
		"macOS did not allow it. Give your terminal Full Disk Access", "or remove it in Finder")
	h.wantNot("…")                  // the reason is wrapped, never cut
	h.want("Moved", "to the Trash") // the other one did go
	h.wantNot("Nothing was removed")
	t.Log("\n" + h.screen())

	if _, err := os.Stat(filepath.Join(locked, "stuck.bin")); err != nil {
		t.Fatal("the stuck file cannot have gone:", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash/free.bin")); err != nil {
		t.Fatal("the free file should be in the Trash:", err)
	}
	logs, _ := filepath.Glob(filepath.Join(h.app.c.Paths.StateDir, "*.log"))
	if len(logs) != 1 {
		t.Fatalf("want one log, got %v", logs)
	}
	log, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(log), "could not remove ~/Projects/data/stuck.bin: macOS did not allow it") {
		t.Fatalf("the log should say what stayed and why:\n%s", log)
	}
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

	// Back to system, which t always reaches from dark. The window is painted
	// by space, so the question must step around space's own colour.
	h.raw = nil
	h.key("t")
	if h.app.c.Mode != theme.System {
		t.Fatalf("after t t t: mode %s", h.app.c.Mode)
	}
	if len(h.raw) != 1 || h.raw[0] != probeOf(h.app.c.Theme.Bg) {
		t.Fatalf("asking the terminal: wrote %q, want %q", h.raw, probeOf(h.app.c.Theme.Bg))
	}
}

// probeOf is the one write that asks a terminal painted with bg for its own
// background: its colour back, the question, then bg again.
func probeOf(bg color.Color) string {
	return ansi.ResetBackgroundColor + ansi.RequestBackgroundColor + ansi.SetBackgroundColor(theme.Hex(bg))
}

func TestSystemThemeAsksTheTerminalNotItself(t *testing.T) {
	h := newHarness(t, theme.System)

	// The first question goes out once the colour profile is known.
	h.send(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if len(h.raw) != 1 || h.raw[0] != probeOf(h.app.c.Theme.Bg) {
		t.Fatalf("at the start: wrote %q", h.raw)
	}
	if i, j := strings.Index(h.raw[0], ansi.ResetBackgroundColor), strings.Index(h.raw[0], ansi.RequestBackgroundColor); i < 0 || i > j {
		t.Fatalf("the terminal's own colour must be back before the question: %q", h.raw[0])
	}

	// Coming back to the window asks again, and paints the current theme back.
	h.send(tea.BackgroundColorMsg{Color: theme.DarkTheme().Bg})
	h.raw = nil
	h.send(tea.FocusMsg{})
	if len(h.raw) != 1 || h.raw[0] != probeOf(theme.DarkTheme().Bg) {
		t.Fatalf("on focus: wrote %q", h.raw)
	}

	// A terminal without colour is not painted, so there is nothing to step
	// around and nothing to paint back.
	h.raw = nil
	h.send(tea.ColorProfileMsg{Profile: colorprofile.ASCII})
	if len(h.raw) != 1 || h.raw[0] != ansi.RequestBackgroundColor {
		t.Fatalf("without colour: wrote %q", h.raw)
	}

	// A fixed theme never asks.
	h.key("t")
	h.raw = nil
	h.send(tea.FocusMsg{})
	h.send(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if len(h.raw) != 0 {
		t.Fatalf("a fixed theme asked the terminal: %q", h.raw)
	}
}

func TestTooSmallWindow(t *testing.T) {
	h := newHarness(t, theme.Dark)
	h.send(tea.WindowSizeMsg{Width: 60, Height: 12})
	h.want("at least 80 × 20")
}

func TestStartedInASubfolderShowsOnlyThatFolder(t *testing.T) {
	h := newHarnessAt(t, theme.Dark, func(home string) string { return filepath.Join(home, "Downloads") })
	h.want("space  ~/Downloads/", "installer.dmg", "small.txt")
	h.wantNot("Movies/", "Documents/", ".npm/", ".claude/")

	// The folder it was started in is the top: left goes nowhere.
	h.key("left", "left")
	h.want("space  ~/Downloads/", "installer.dmg")
	h.wantNot("Movies/")

	// Recipes belong to the Mac, not to the folder.
	h.want("Recipes 1")

	// Removing works here as anywhere inside the home folder.
	h.key("space", "enter")
	h.want("Here’s what will happen", "Move 1 item to the Trash")
}

func TestAboveTheHomeFolderSpaceOnlyLooks(t *testing.T) {
	h := newHarnessAt(t, theme.Dark, filepath.Dir)
	home := h.app.c.Paths.Home
	h.want(filepath.Base(home) + "/")

	h.key("right") // into the home folder
	h.want("Movies/")
	h.cursorToName("Movies")
	h.key("right", "space")
	h.want("1 marked")
	h.key("enter")
	h.want("Here space only looks")
	h.wantNot("Here’s what will happen")
	if len(h.app.stack) != 1 {
		t.Fatal("the plan screen must not open")
	}
	if _, err := os.Stat(filepath.Join(home, "Movies/recording.mov")); err != nil {
		t.Fatal("nothing may be removed:", err)
	}
}

// A fast step can report before the run screen is on the stack. The run
// must still reach the done screen: it used to stay on "Running" for ever,
// with the work already finished.
func TestRunFinishesWhateverOrderTheMessagesArrive(t *testing.T) {
	h := newHarness(t, theme.Dark)
	home := h.app.c.Paths.Home
	h.reverse = true

	h.cursorTo("Movies")
	h.key("right", "space", "enter")
	h.want("Move 1 item to the Trash")
	h.key("enter")
	for i := 0; i < 20; i++ { // let the screen catch up
		h.run(h.app.broadcast(screens.TickMsg{}))
	}
	h.want("Moved", "to the Trash", "back to overview")
	h.wantNot("Running")
	if _, err := os.Stat(filepath.Join(home, ".Trash/recording.mov")); err != nil {
		t.Fatal("the file should be in the Trash:", err)
	}
}
