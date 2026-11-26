//go:build darwin

package trash

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/romus/space/internal/engine"
	"github.com/romus/space/pkg/recipe"
)

func mixed() []recipe.Item {
	return []recipe.Item{
		{Path: "/Users/me/Movies/a.mov", Bytes: 100, Kind: recipe.Plain},
		{Path: "/Users/me/.npm", Bytes: 300, Kind: recipe.Cache},
		{Path: "/Users/me/Projects/web/node_modules", Bytes: 200, Kind: recipe.Cache},
	}
}

func TestPlanMovesCachesToTheTrashToo(t *testing.T) {
	p, err := Plan(mixed(), "/Users/me", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 1 {
		t.Fatalf("want one step, got %d", len(p.Steps))
	}
	s := p.Steps[0]
	if s.Remove == nil || !s.Remove.ToTrash {
		t.Fatalf("the step must move to the Trash: %+v", s)
	}
	if s.Tag() != "REVERSIBLE" {
		t.Fatalf("tag = %s, want REVERSIBLE", s.Tag())
	}
	if s.Title != "Move 3 items to the Trash" {
		t.Fatalf("title = %q", s.Title)
	}
	want := []string{"/Users/me/.npm", "/Users/me/Projects/web/node_modules", "/Users/me/Movies/a.mov"}
	if len(s.Remove.Paths) != len(want) {
		t.Fatalf("paths = %v", s.Remove.Paths)
	}
	for i := range want {
		if s.Remove.Paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v (largest first)", s.Remove.Paths, want)
		}
	}
	if p.ToTrash != 600 || p.Reclaim != 0 {
		t.Fatalf("ToTrash = %d, Reclaim = %d; want 600 and 0", p.ToTrash, p.Reclaim)
	}
}

func TestPlanPermanentDeletesAndKeepsCachesSafe(t *testing.T) {
	p, err := Plan(mixed(), "/Users/me", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("want two steps, got %d", len(p.Steps))
	}
	for _, s := range p.Steps {
		if s.Remove == nil || s.Remove.ToTrash {
			t.Fatalf("a permanent step must not use the Trash: %+v", s)
		}
	}
	if got := p.Steps[0].Tag(); got != "IRREVERSIBLE" {
		t.Fatalf("plain step tag = %s", got)
	}
	if got := p.Steps[1].Tag(); got != "SAFE" {
		t.Fatalf("cache step tag = %s", got)
	}
	if p.Steps[1].Title != "Delete 2 caches" {
		t.Fatalf("title = %q", p.Steps[1].Title)
	}
	if p.ToTrash != 0 || p.Reclaim != 600 {
		t.Fatalf("ToTrash = %d, Reclaim = %d; want 0 and 600", p.ToTrash, p.Reclaim)
	}
}

func TestPlanRefusesWhatItCannotRemove(t *testing.T) {
	if _, err := Plan(nil, "/Users/me", false); err == nil {
		t.Fatal("an empty selection must be refused")
	}
	special := []recipe.Item{{Path: "/Users/me/.local/share/containers", Kind: recipe.Special}}
	for _, permanent := range []bool{false, true} {
		if _, err := Plan(special, "/Users/me", permanent); err == nil {
			t.Fatalf("a recipe's folder must be refused (permanent=%v)", permanent)
		}
	}
}

// The whole way down: a cache marked on the overview ends up in the Trash,
// not deleted.
func TestCacheEndsUpInTheTrash(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPACE_HOME", home)

	cache := filepath.Join(home, ".npm")
	if err := os.MkdirAll(filepath.Join(cache, "_cacache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "_cacache", "blob"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Plan([]recipe.Item{{Path: cache, Bytes: 4, Kind: recipe.Cache}}, home, false)
	if err != nil {
		t.Fatal(err)
	}
	engine.Prepare(&p)
	e := &engine.Engine{Home: home, Volume: home}
	res := e.Run(context.Background(), p, nil)
	if res.Failed != -1 || len(res.Failures()) != 0 {
		t.Fatalf("run failed: %v %v", res.Err, res.Failures())
	}

	if _, err := os.Lstat(cache); !os.IsNotExist(err) {
		t.Fatalf("the cache is still in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash", ".npm", "_cacache", "blob")); err != nil {
		t.Fatalf("the cache is not in the Trash: %v", err)
	}
	if res.Trashed == 0 {
		t.Fatal("the result must report what went to the Trash")
	}
}
