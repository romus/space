// Package humanize formats sizes, counts and ages the way the UI shows them.
package humanize

import (
	"fmt"
	"strings"
	"time"
)

// Bytes formats a size with decimal units, like Finder: 38.1 GB, 412 MB.
func Bytes(n int64) string {
	if n < 0 {
		return "-" + Bytes(-n)
	}
	const (
		kb = 1000
		mb = kb * 1000
		gb = mb * 1000
		tb = gb * 1000
	)
	f := float64(n)
	switch {
	case n >= tb:
		return fmt.Sprintf("%.1f TB", f/tb)
	case n >= gb:
		return fmt.Sprintf("%.1f GB", f/gb)
	case n >= mb:
		return fmt.Sprintf("%.0f MB", f/mb)
	case n >= kb:
		return fmt.Sprintf("%.0f KB", f/kb)
	}
	return fmt.Sprintf("%d B", n)
}

// Binary formats a configured size in powers of two, the way tools that
// allocate it report it: a 4 GiB machine with a 100 GiB disk reads
// "4 GB" and "100 GB".
func Binary(n int64) string {
	const gib = 1 << 30
	if n >= gib {
		if n%gib == 0 {
			return fmt.Sprintf("%d GB", n/gib)
		}
		return fmt.Sprintf("%.1f GB", float64(n)/gib)
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// GB formats a size as whole gigabytes without a unit: 494.
func GB(n int64) string { return fmt.Sprintf("%.0f", float64(n)/1e9) }

// Count formats a number with thousands separators: 1,284.
func Count(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return "-" + Count(-n)
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Short formats a large count compactly: 1.2M, 412K, 96.
func Short(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// Days is the whole number of days between t and now.
func Days(t, now time.Time) int {
	if t.IsZero() {
		return 0
	}
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// Ago says how long ago t was: "now", "today", "33 days ago", "1 year ago".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "now"
	case d < 24*time.Hour:
		return "today"
	}
	days := int(d.Hours() / 24)
	switch {
	case days == 1:
		return "1 day ago"
	case days < 365:
		return fmt.Sprintf("%d days ago", days)
	case days < 730:
		return "1 year ago"
	}
	return fmt.Sprintf("%d years ago", days/365)
}

// Age is the compact form used in narrow columns: "now", "2 days", "3 mo".
func Age(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "now"
	case d < 24*time.Hour:
		return "today"
	}
	days := int(d.Hours() / 24)
	switch {
	case days == 1:
		return "1 day"
	case days < 14:
		return fmt.Sprintf("%d days", days)
	case days < 60:
		return fmt.Sprintf("%d wk", days/7)
	case days < 365:
		return fmt.Sprintf("%d mo", days/30)
	}
	return fmt.Sprintf("%d yr", days/365)
}

// Duration formats a short duration: "4 s", "1 m 12 s".
func Duration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	return fmt.Sprintf("%d m %d s", s/60, s%60)
}

// Clock formats elapsed time as 00:17.
func Clock(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// Plural picks the singular or plural form: Plural(1, "step") is "1 step".
func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%s %ss", Count(int64(n)), word)
}

// Tilde shortens a path under home to ~/… for display.
func Tilde(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}
