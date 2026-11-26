//go:build darwin

package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mb = 1 << 20

func mkfile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanDir(t *testing.T, root string, opt Options) *Scanner {
	t.Helper()
	s, err := New(root, opt)
	if err != nil {
		t.Fatal(err)
	}
	s.Run(context.Background())
	return s
}

// near allows for the few blocks a filesystem adds on its own.
func near(got, want int64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= 64*1024
}

func TestSizesAreOnDisk(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "a/big.bin"), 3*mb)
	mkfile(t, filepath.Join(root, "a/deep/small.bin"), 1*mb)
	mkfile(t, filepath.Join(root, "b/one.bin"), 2*mb)

	// A sparse file: 1 GB long, nothing on disk.
	sparse := filepath.Join(root, "b/sparse.raw")
	f, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s := scanDir(t, root, Options{})
	if got := s.Root.Size(); !near(got, 6*mb) {
		t.Fatalf("root: got %d, want about %d — a sparse file must count by blocks, not length", got, 6*mb)
	}
	if got := s.Root.Files(); got != 4 {
		t.Fatalf("files: got %d, want 4", got)
	}
	kids := s.Children(s.Root)
	if len(kids) != 2 || kids[0].Name != "a" || kids[1].Name != "b" {
		t.Fatalf("children must be sorted by size: %v", names(kids))
	}
	if !near(kids[0].Size(), 4*mb) || !near(kids[1].Size(), 2*mb) {
		t.Fatalf("sizes: a=%d b=%d", kids[0].Size(), kids[1].Size())
	}
	p := s.Progress()
	if !p.Done || p.Files != 4 || p.Dirs != 4 {
		t.Fatalf("progress: %+v", p)
	}
}

func names(dirs []*Dir) []string {
	var out []string
	for _, d := range dirs {
		out = append(out, d.Name)
	}
	return out
}

func TestHardLinksCountOnce(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "x/file.bin"), 4*mb)
	if err := os.MkdirAll(filepath.Join(root, "y"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "x/file.bin"), filepath.Join(root, "y/link.bin")); err != nil {
		t.Fatal(err)
	}
	s := scanDir(t, root, Options{Workers: 1})
	if got := s.Root.Size(); !near(got, 4*mb) {
		t.Fatalf("got %d, want about %d: a hard link is the same blocks", got, 4*mb)
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mkfile(t, filepath.Join(outside, "huge.bin"), 8*mb)
	mkfile(t, filepath.Join(root, "real.bin"), 1*mb)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s := scanDir(t, root, Options{})
	if got := s.Root.Size(); !near(got, 1*mb) {
		t.Fatalf("got %d: the target of a symlink must not be counted", got)
	}
	if len(s.Children(s.Root)) != 0 {
		t.Fatal("a symlink to a folder is not a folder")
	}
}

func TestTopFiles(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "a/five.bin"), 5*mb)
	mkfile(t, filepath.Join(root, "b/c/nine.bin"), 9*mb)
	mkfile(t, filepath.Join(root, "two.bin"), 2*mb)
	mkfile(t, filepath.Join(root, "tiny.txt"), 100)

	s := scanDir(t, root, Options{})
	top := s.TopFiles()
	if len(top) != 3 {
		t.Fatalf("got %d files, want 3 (tiny files are skipped)", len(top))
	}
	if top[0].Name != "nine.bin" || top[1].Name != "five.bin" || top[2].Name != "two.bin" {
		t.Fatalf("order: %s %s %s", top[0].Name, top[1].Name, top[2].Name)
	}
	if got, want := top[0].Path(), filepath.Join(root, "b/c/nine.bin"); got != want {
		t.Fatalf("path: got %s, want %s", got, want)
	}
}

func TestLookupAndNodeModules(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "web/node_modules/a/index.js"), 2*mb)
	mkfile(t, filepath.Join(root, "web/node_modules/a/node_modules/b/index.js"), 1*mb)
	mkfile(t, filepath.Join(root, "api/node_modules/c/index.js"), 1*mb)
	mkfile(t, filepath.Join(root, "api/src/main.go"), 1000)

	s := scanDir(t, root, Options{})
	if d := s.Lookup(filepath.Join(root, "web/node_modules")); d == nil || d.Name != "node_modules" {
		t.Fatal("Lookup did not find web/node_modules")
	}
	if s.Lookup(filepath.Join(root, "nope")) != nil || s.Lookup("/") != nil {
		t.Fatal("Lookup found something that is not there")
	}
	if s.Lookup(root) != s.Root {
		t.Fatal("Lookup of the root")
	}
	// Nested node_modules belong to the outer one: count the topmost only.
	n, size := s.NodeModules(s.Root)
	if n != 2 || !near(size, 4*mb) {
		t.Fatalf("node_modules: got %d folders, %d bytes; want 2 folders, about %d", n, size, 4*mb)
	}
}

func TestOpaqueFoldersAreMeasuredAsOneItem(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "Photos.photoslibrary/originals/1.jpg"), 3*mb)
	mkfile(t, filepath.Join(root, "Photos.photoslibrary/originals/2.jpg"), 2*mb)
	mkfile(t, filepath.Join(root, "plain/file.bin"), 1*mb)

	s := scanDir(t, root, Options{Opaque: func(path, name string) bool {
		return strings.HasSuffix(name, ".photoslibrary")
	}})
	lib := s.Lookup(filepath.Join(root, "Photos.photoslibrary"))
	if lib == nil || !lib.Bundle {
		t.Fatal("the library should be in the tree, marked as a bundle")
	}
	if !near(lib.Size(), 5*mb) || lib.Files() != 2 {
		t.Fatalf("bundle: %d bytes, %d files", lib.Size(), lib.Files())
	}
	if len(s.Children(lib)) != 0 {
		t.Fatal("a bundle is not descended into")
	}
	top := s.TopFiles()
	if len(top) != 2 || !top[0].Bundle || top[0].Name != "Photos.photoslibrary" {
		t.Fatalf("the bundle should be the largest single item, files inside it hidden: %+v", top)
	}
	if !near(s.Root.Size(), 6*mb) {
		t.Fatalf("root: %d", s.Root.Size())
	}
}

func TestListAndSizeOf(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "b.bin"), 2*mb)
	mkfile(t, filepath.Join(root, "a.bin"), 3*mb)
	mkfile(t, filepath.Join(root, "sub/c.bin"), 1*mb)

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "a.bin" || list[1].Name != "b.bin" {
		t.Fatalf("List returns files only, largest first: %+v", list)
	}
	if got := SizeOf(root); !near(got, 6*mb) {
		t.Fatalf("SizeOf folder: %d", got)
	}
	if got := SizeOf(filepath.Join(root, "a.bin")); !near(got, 3*mb) {
		t.Fatalf("SizeOf file: %d", got)
	}
	if SizeOf(filepath.Join(root, "missing")) != 0 {
		t.Fatal("SizeOf a missing path")
	}
}

func TestUnreadableFolderIsCounted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "locked/secret.bin"), 1*mb)
	mkfile(t, filepath.Join(root, "open/file.bin"), 1*mb)
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)

	s := scanDir(t, root, Options{})
	if p := s.Progress(); p.Unreadable != 1 {
		t.Fatalf("unreadable: got %d, want 1", p.Unreadable)
	}
	if d := s.Lookup(locked); d == nil || !d.Unreadable {
		t.Fatal("the folder should be marked unreadable")
	}
}

func TestVolume(t *testing.T) {
	v, err := VolumeOf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if v.Total <= 0 || v.Free <= 0 || v.Free > v.Total || v.Name == "" {
		t.Fatalf("volume: %+v", v)
	}
}

func TestNewRejectsFiles(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "f"), 10)
	if _, err := New(filepath.Join(root, "f"), Options{}); err == nil {
		t.Fatal("a file is not a folder to scan")
	}
	if _, err := New(filepath.Join(root, "missing"), Options{}); err == nil {
		t.Fatal("a missing path")
	}
}

// With First set the walk heads for that folder: at every level the child
// leading to it is queued last, which is what the stack hands out next.
func TestFirstIsReachedBeforeTheRest(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a", "b", "c", "d"} {
		mkfile(t, filepath.Join(root, d, "x/y/file.bin"), 1*mb)
		mkfile(t, filepath.Join(root, d, "z/file.bin"), 1*mb)
	}
	first := filepath.Join(root, "b/x/y")

	s, err := New(root, Options{First: first + "/"})
	if err != nil {
		t.Fatal(err)
	}
	d := s.Root
	for _, want := range []string{"b", "x", "y"} {
		todo := s.walk(d)
		if len(todo) == 0 || todo[len(todo)-1].Name != want {
			t.Fatalf("after %s the next folder must be %q, got %v", d.Path(), want, names(todo))
		}
		d = todo[len(todo)-1]
	}
	if got := s.Lookup(first); got != d {
		t.Fatalf("Lookup(%s) = %v, want the folder just reached", first, got)
	}
	// Off the way nothing is reordered, and a name that only starts the
	// same is not on the way.
	if s.towardsFirst(filepath.Join(root, "a")) != "" || s.towardsFirst(filepath.Join(root, "b/x/y")) != "" {
		t.Fatal("a folder off the way, or First itself, leads nowhere")
	}

	// The order changes, the numbers do not.
	plain := scanDir(t, root, Options{})
	headed := scanDir(t, root, Options{First: first})
	if plain.Root.Size() != headed.Root.Size() || plain.Root.Files() != headed.Root.Files() {
		t.Fatalf("First changed the totals: %d/%d against %d/%d",
			headed.Root.Size(), headed.Root.Files(), plain.Root.Size(), plain.Root.Files())
	}
	if headed.Lookup(first) == nil {
		t.Fatal("Lookup did not find First after a full scan")
	}
}
