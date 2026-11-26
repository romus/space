// Package theme holds the colour tokens of space and decides which set is
// in use: light, dark, or whatever the terminal is showing.
package theme

import (
	"fmt"
	"image/color"
	"os/exec"
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
)

// Mode is the user's choice.
type Mode string

const (
	System Mode = "system" // follow the terminal background
	Light  Mode = "light"
	Dark   Mode = "dark"
)

// ParseMode validates a mode given on the command line or in the config.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case System, Light, Dark:
		return m, nil
	case "", "auto":
		return System, nil
	}
	return System, fmt.Errorf("unknown theme %q: use system, light or dark", s)
}

// Next cycles system → light → dark.
func (m Mode) Next() Mode {
	switch m {
	case System:
		return Light
	case Light:
		return Dark
	}
	return System
}

// Theme is one complete set of colours. Screens never use hex values
// directly; they only read these fields.
type Theme struct {
	Name    string
	IsDark  bool
	NoColor bool // the terminal shows no colour: selection uses reverse video

	Bg         color.Color // window background
	Text       color.Color // body text, key names in hints
	Strong     color.Color // current row, active tab, totals
	Muted      color.Color // labels, percentages, explanations
	Faint      color.Color // column headers, IDs, second-line paths
	Subtle     color.Color // commands, disk gauge, filter chips
	Found      color.Color // the FOUND column of recipes
	Rule       color.Color // horizontal lines
	BoxBorder  color.Color // info box and log frame
	ChipBorder color.Color // filter chip frame
	KeyBorder  color.Color // frame around the primary key
	Selection  color.Color // background of the current row
	SoftSel    color.Color // highlighted step on the plan screen
	Bar        color.Color // size bars
	BarCur     color.Color // size bar of the current row
	BarDim     color.Color // size bar of personal, view-only rows
	Recipe     color.Color // ◆ and everything a recipe owns
	Safe       color.Color // ●, ✓, SAFE and REVERSIBLE
	Danger     color.Color // IRREVERSIBLE
}

func hex(s string) color.Color { return lipgloss.Color(s) }

// DarkTheme is the dark set, taken from the dark artboards.
func DarkTheme() *Theme {
	return &Theme{
		Name: "dark", IsDark: true,
		Bg:         hex("#0F0F12"),
		Text:       hex("#E6E6E9"),
		Strong:     hex("#FFFFFF"),
		Muted:      hex("#8B8B93"),
		Faint:      hex("#6E6E76"),
		Subtle:     hex("#C9C9CE"),
		Found:      hex("#A8A8AF"),
		Rule:       hex("#232328"),
		BoxBorder:  hex("#26262C"),
		ChipBorder: hex("#34343B"),
		KeyBorder:  hex("#5A5A62"),
		Selection:  hex("#26262B"),
		SoftSel:    hex("#1C1C20"),
		Bar:        hex("#6B6B73"),
		BarCur:     hex("#FFFFFF"),
		BarDim:     hex("#3A3A42"),
		Recipe:     hex("#C29BFF"),
		Safe:       hex("#7FC8A9"),
		Danger:     hex("#FF7A6E"),
	}
}

// LightTheme is the light set, taken from the light artboards.
func LightTheme() *Theme {
	return &Theme{
		Name: "light", IsDark: false,
		Bg:         hex("#FFFFFF"),
		Text:       hex("#26262A"),
		Strong:     hex("#000000"),
		Muted:      hex("#6A6A72"),
		Faint:      hex("#74747C"),
		Subtle:     hex("#3A3A40"),
		Found:      hex("#55555C"),
		Rule:       hex("#E4E4E7"),
		BoxBorder:  hex("#DEDEE2"),
		ChipBorder: hex("#CFCFD4"),
		KeyBorder:  hex("#B8B8BE"),
		Selection:  hex("#EDEDF0"),
		SoftSel:    hex("#F5F5F7"),
		Bar:        hex("#B4B4BB"),
		BarCur:     hex("#000000"),
		BarDim:     hex("#DCDCE0"),
		Recipe:     hex("#8944AB"),
		Safe:       hex("#1E8449"),
		Danger:     hex("#C4352A"),
	}
}

// For returns the theme for a mode. terminalDark is what the terminal (or
// the fallback) reported; it only matters in System mode.
func For(mode Mode, terminalDark bool) *Theme {
	switch mode {
	case Light:
		return LightTheme()
	case Dark:
		return DarkTheme()
	}
	if terminalDark {
		return DarkTheme()
	}
	return LightTheme()
}

// Base is the style every other style starts from: it carries the window
// background, so text keeps the right colour behind it even on a terminal
// that ignores a request to change its own background.
func (t *Theme) Base() lipgloss.Style {
	if t.NoColor {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Background(t.Bg)
}

// S is the base style with a foreground colour.
func (t *Theme) S(fg color.Color) lipgloss.Style {
	if t.NoColor {
		return lipgloss.NewStyle()
	}
	return t.Base().Foreground(fg)
}

// On is S drawn over another background, such as the current row.
func (t *Theme) On(fg, bg color.Color) lipgloss.Style {
	if t.NoColor {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(fg).Background(bg)
}

// Sel is a style for text on the current row. Without colour the row is
// shown in reverse video instead of a tinted background.
func (t *Theme) Sel(fg color.Color) lipgloss.Style {
	if t.NoColor {
		return lipgloss.NewStyle().Reverse(true)
	}
	return lipgloss.NewStyle().Foreground(fg).Background(t.Selection)
}

// Soft is a style for text on a softly highlighted block.
func (t *Theme) Soft(fg color.Color) lipgloss.Style {
	if t.NoColor {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(fg).Background(t.SoftSel)
}

// GuessDark is the answer used before the terminal reports its background,
// and for good when it never does: the macOS appearance, else dark.
func GuessDark() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err != nil {
		// The key is absent in light mode, so the command fails.
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "dark")
}
