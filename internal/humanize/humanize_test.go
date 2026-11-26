package humanize

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	cases := map[int64]string{
		0:                 "0 B",
		999:               "999 B",
		18_000_000:        "18 MB",
		412_000_000:       "412 MB",
		38_100_000_000:    "38.1 GB",
		6_640_000_000:     "6.6 GB",
		1_500_000_000_000: "1.5 TB",
	}
	for n, want := range cases {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
	if Binary(4<<30) != "4 GB" || Binary(100<<30) != "100 GB" || Binary(1536<<20) != "1.5 GB" || Binary(512<<20) != "512 MB" {
		t.Errorf("Binary: %q %q %q %q", Binary(4<<30), Binary(100<<30), Binary(1536<<20), Binary(512<<20))
	}
	if GB(494_000_000_000) != "494" {
		t.Errorf("GB: %q", GB(494_000_000_000))
	}
}

func TestCounts(t *testing.T) {
	if Count(1284) != "1,284" || Count(612) != "612" || Count(1234567) != "1,234,567" {
		t.Errorf("Count: %q %q %q", Count(1284), Count(612), Count(1234567))
	}
	if Short(1_200_000) != "1.2M" || Short(412_000) != "412K" || Short(96) != "96" {
		t.Errorf("Short: %q %q %q", Short(1_200_000), Short(412_000), Short(96))
	}
	if Plural(1, "step") != "1 step" || Plural(5, "step") != "5 steps" || Plural(1102, "session") != "1,102 sessions" {
		t.Errorf("Plural: %q %q", Plural(1, "step"), Plural(1102, "session"))
	}
}

func TestAges(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ago := func(days int) time.Time { return now.Add(-time.Duration(days) * 24 * time.Hour) }
	long := map[int]string{0: "now", 1: "1 day ago", 33: "33 days ago", 241: "241 days ago", 380: "1 year ago", 800: "2 years ago"}
	for days, want := range long {
		if got := Ago(ago(days), now); got != want {
			t.Errorf("Ago(%d days) = %q, want %q", days, got, want)
		}
	}
	short := map[int]string{1: "1 day", 2: "2 days", 14: "2 wk", 30: "4 wk", 90: "3 mo", 150: "5 mo", 400: "1 yr"}
	for days, want := range short {
		if got := Age(ago(days), now); got != want {
			t.Errorf("Age(%d days) = %q, want %q", days, got, want)
		}
	}
	if Ago(now.Add(-3*time.Hour), now) != "today" || Ago(time.Time{}, now) != "—" {
		t.Error("today and the zero time")
	}
	if Days(ago(47), now) != 47 || Days(now.Add(time.Hour), now) != 0 {
		t.Error("Days")
	}
}

func TestDurations(t *testing.T) {
	if Duration(4*time.Second) != "4 s" || Duration(72*time.Second) != "1 m 12 s" {
		t.Errorf("%q %q", Duration(4*time.Second), Duration(72*time.Second))
	}
	if Clock(17*time.Second) != "00:17" || Clock(125*time.Second) != "02:05" {
		t.Errorf("%q %q", Clock(17*time.Second), Clock(125*time.Second))
	}
}

func TestTilde(t *testing.T) {
	home := "/Users/me"
	cases := map[string]string{
		"/Users/me":              "~",
		"/Users/me/.local/share": "~/.local/share",
		"/Users/meow/file":       "/Users/meow/file",
		"/tmp/x":                 "/tmp/x",
	}
	for in, want := range cases {
		if got := Tilde(in, home); got != want {
			t.Errorf("Tilde(%q) = %q, want %q", in, got, want)
		}
	}
}
