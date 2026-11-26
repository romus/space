//go:build darwin

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romus/space/internal/config"
)

// tree makes a home folder with a few folders and a file in it.
func tree(t *testing.T) config.Paths {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"Downloads/sub", "Movies"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Paths{Home: home}
}

func TestResolveRoot(t *testing.T) {
	t.Setenv("SPACE_HOME", "")
	paths := tree(t)
	home := paths.Home
	t.Chdir(filepath.Join(home, "Downloads"))

	cases := []struct {
		name      string
		arg, flag string
		want      string
	}{
		{"no path: the folder space was started in", "", "", filepath.Join(home, "Downloads")},
		{"an argument", filepath.Join(home, "Movies"), "", filepath.Join(home, "Movies")},
		{"the flag", "", filepath.Join(home, "Movies"), filepath.Join(home, "Movies")},
		{"a relative argument", "sub", "", filepath.Join(home, "Downloads/sub")},
		{"a relative flag", "", "..", home},
		{"dot", ".", "", filepath.Join(home, "Downloads")},
		{"a quoted tilde", "~/Movies", "", filepath.Join(home, "Movies")},
		{"a bare tilde", "", "~", home},
		{"a trailing slash", filepath.Join(home, "Movies") + "/", "", filepath.Join(home, "Movies")},
	}
	for _, c := range cases {
		got, err := resolveRoot(c.arg, c.flag, paths)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestResolveRootErrors(t *testing.T) {
	t.Setenv("SPACE_HOME", "")
	paths := tree(t)
	home := paths.Home
	t.Chdir(home)

	cases := []struct {
		name      string
		arg, flag string
		wantErr   string
	}{
		{"both an argument and the flag", "Movies", "Downloads", "given twice"},
		{"a file", "notes.txt", "", "is not a folder"},
		{"a missing folder", "", "nowhere", "does not exist"},
	}
	for _, c := range cases {
		_, err := resolveRoot(c.arg, c.flag, paths)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.wantErr)
		}
	}
}

func TestResolveRootFollowsSymlinks(t *testing.T) {
	t.Setenv("SPACE_HOME", "")
	paths := tree(t)
	home := paths.Home
	link := filepath.Join(home, "dl")
	if err := os.Symlink(filepath.Join(home, "Downloads"), link); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRoot(link, "", paths)
	if err != nil || got != filepath.Join(home, "Downloads") {
		t.Fatalf("got %q, %v", got, err)
	}
}

// A sandboxed run opens the sandbox, wherever it was started: it must not
// scan, or remove from, the real folder it happens to be in.
func TestResolveRootInASandbox(t *testing.T) {
	paths := tree(t)
	home := paths.Home
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	t.Setenv("SPACE_HOME", home)

	if got, err := resolveRoot("", "", paths); err != nil || got != home {
		t.Fatalf("no path: got %q, %v; want the sandbox %q", got, err, home)
	}
	want := filepath.Join(home, "Movies")
	if got, err := resolveRoot("", want, paths); err != nil || got != want {
		t.Fatalf("an explicit path still wins: got %q, %v", got, err)
	}
}
