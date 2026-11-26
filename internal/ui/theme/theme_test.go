package theme

import (
	"image/color"
	"math"
	"reflect"
	"testing"
)

func TestParseMode(t *testing.T) {
	ok := map[string]Mode{"system": System, "light": Light, "DARK": Dark, " Light ": Light, "": System, "auto": System}
	for in, want := range ok {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseMode("solarized"); err == nil {
		t.Error("an unknown theme must be an error")
	}
}

func TestModeCycles(t *testing.T) {
	if System.Next() != Light || Light.Next() != Dark || Dark.Next() != System {
		t.Error("t cycles system → light → dark → system")
	}
}

func TestFor(t *testing.T) {
	cases := []struct {
		mode     Mode
		termDark bool
		want     string
	}{
		{System, true, "dark"}, {System, false, "light"},
		{Light, true, "light"}, {Light, false, "light"},
		{Dark, true, "dark"}, {Dark, false, "dark"},
	}
	for _, c := range cases {
		if got := For(c.mode, c.termDark).Name; got != c.want {
			t.Errorf("For(%s, dark=%v) = %s, want %s", c.mode, c.termDark, got, c.want)
		}
	}
}

// The values are the ones written in the artboards.
func TestTokensMatchTheDesign(t *testing.T) {
	d, l := DarkTheme(), LightTheme()
	cases := []struct {
		name        string
		dark, light color.Color
		wantD       string
		wantL       string
	}{
		{"Bg", d.Bg, l.Bg, "#0F0F12", "#FFFFFF"},
		{"Text", d.Text, l.Text, "#E6E6E9", "#26262A"},
		{"Strong", d.Strong, l.Strong, "#FFFFFF", "#000000"},
		{"Muted", d.Muted, l.Muted, "#8B8B93", "#6A6A72"},
		{"Faint", d.Faint, l.Faint, "#6E6E76", "#74747C"},
		{"Rule", d.Rule, l.Rule, "#232328", "#E4E4E7"},
		{"Selection", d.Selection, l.Selection, "#26262B", "#EDEDF0"},
		{"Bar", d.Bar, l.Bar, "#6B6B73", "#B4B4BB"},
		{"Recipe", d.Recipe, l.Recipe, "#C29BFF", "#8944AB"},
		{"Safe", d.Safe, l.Safe, "#7FC8A9", "#1E8449"},
		{"Danger", d.Danger, l.Danger, "#FF7A6E", "#C4352A"},
	}
	for _, c := range cases {
		if Hex(c.dark) != c.wantD || Hex(c.light) != c.wantL {
			t.Errorf("%s: dark %s (want %s), light %s (want %s)", c.name, Hex(c.dark), c.wantD, Hex(c.light), c.wantL)
		}
	}
}

func TestEveryTokenIsSet(t *testing.T) {
	for _, th := range []*Theme{DarkTheme(), LightTheme()} {
		v := reflect.ValueOf(*th)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() == reflect.Interface && v.Field(i).IsNil() {
				t.Errorf("%s theme: %s is not set", th.Name, v.Type().Field(i).Name)
			}
		}
	}
}

func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		f := float64(v) / 65535
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Text must stay readable on the window and on the current row, in both
// themes: at least 4.5:1 for anything the user has to read.
func TestTextIsReadable(t *testing.T) {
	for _, th := range []*Theme{DarkTheme(), LightTheme()} {
		text := map[string]color.Color{
			"Text": th.Text, "Strong": th.Strong, "Muted": th.Muted, "Subtle": th.Subtle,
			"Found": th.Found, "Recipe": th.Recipe, "Safe": th.Safe, "Danger": th.Danger,
		}
		for name, fg := range text {
			if c := contrast(fg, th.Bg); c < 4.5 {
				t.Errorf("%s theme: %s on the background is %.2f:1", th.Name, name, c)
			}
		}
		for _, name := range []string{"Text", "Strong", "Recipe"} {
			if c := contrast(text[name], th.Selection); c < 4.5 {
				t.Errorf("%s theme: %s on the current row is %.2f:1", th.Name, name, c)
			}
		}
		// The design's green on the light current row is 4.04:1: under 4.5,
		// kept as drawn. It only colours a glyph and a short hint, never the
		// name or the size, so the floor here is the 3:1 of non-text marks.
		for _, name := range []string{"Safe", "Danger"} {
			if c := contrast(text[name], th.Selection); c < 3 {
				t.Errorf("%s theme: %s on the current row is %.2f:1", th.Name, name, c)
			}
		}
	}
}
