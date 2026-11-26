package classify

import (
	"context"
	"testing"

	"github.com/romus/space/pkg/recipe"
)

// fakeRecipe is a stand-in for a detected recipe that owns a folder.
type fakeRecipe struct{ id, name, hint, path string }

func (o fakeRecipe) ID() string { return o.id }
func (o fakeRecipe) Info() recipe.Info {
	return recipe.Info{Name: o.name, Hint: o.hint, Path: o.path}
}
func (fakeRecipe) Detect(context.Context, recipe.Env) bool                 { return true }
func (fakeRecipe) Inspect(context.Context) ([]recipe.Item, error)          { return nil, nil }
func (fakeRecipe) Summary([]recipe.Item) string                            { return "" }
func (fakeRecipe) Plan([]recipe.Item, recipe.Options) (recipe.Plan, error) { return recipe.Plan{}, nil }

func classifier() *Classifier {
	return New("/Users/me", []recipe.Recipe{
		fakeRecipe{"podman", "Podman", "podman machine", "/Users/me/.local/share/containers"},
		fakeRecipe{"claude", "Claude Code", "Claude Code", "/Users/me/.claude"},
	})
}

func TestKinds(t *testing.T) {
	c := classifier()
	cases := []struct {
		path string
		kind recipe.Kind
		hint string
		how  string
	}{
		// The rows of the Main artboard.
		{"/Users/me/Library", recipe.Plain, "", "Trash"},
		{"/Users/me/.local", recipe.Special, "podman machine", "Podman recipe"},
		{"/Users/me/Developer", recipe.Plain, "", "Trash"},
		{"/Users/me/Movies", recipe.Plain, "", "Trash"},
		{"/Users/me/Pictures", recipe.Plain, "", "Trash"},
		{"/Users/me/.claude", recipe.Special, "Claude Code", "Claude Code recipe"},
		{"/Users/me/Documents", recipe.Plain, "", "Trash"},
		{"/Users/me/.npm", recipe.Cache, "npm cache", "cache"},
		{"/Users/me/.cache", recipe.Cache, "tool caches", "cache"},
		// The rows of the Files artboard.
		{"/Users/me/.local/share/containers/podman/machine/applehv/disk.raw", recipe.Special, "podman machine", "Podman recipe"},
		{"/Users/me/Movies/screen-recording-march.mov", recipe.Plain, "", "Trash"},
		{"/Users/me/Movies/iMovie Library.imovielibrary", recipe.Plain, "iMovie library", "Trash"},
		{"/Users/me/Library/Application Support/MobileSync/Backup/00008110", recipe.Plain, "iPhone backup", "Trash"},
		{"/Users/me/Pictures/Photos Library.photoslibrary", recipe.Plain, "Photos library", "Trash"},
		{"/Users/me/Projects/indexer/.cache/lmdb/data.mdb", recipe.Cache, "", "project cache"},
		{"/Users/me/Library/Caches/colima/vmlinux.img", recipe.Cache, "", "cache"},
		{"/Users/me/.claude/projects/x/session.jsonl", recipe.Special, "Claude Code", "Claude Code recipe"},
		// Elsewhere.
		{"/Users/me/Projects/web/node_modules", recipe.Cache, "node_modules", "project cache"},
		// What is inside a protected folder is ordinary.
		{"/Users/me/Documents/thesis/draft.docx", recipe.Plain, "", "Trash"},
		{"/Users/me/Documents/private/app/node_modules", recipe.Cache, "node_modules", "project cache"},
		{"/Users/me/Desktop/screenshot.png", recipe.Plain, "", "Trash"},
		{"/Users/me/Library/Mail/V10/big.mbox", recipe.Plain, "", "Trash"},
		{"/Volumes/External/stuff", recipe.Plain, "", "Trash"},
		{"/Users/me/.localization", recipe.Plain, "", "Trash"},
	}
	for _, tc := range cases {
		got := c.Path(tc.path)
		if got.Kind != tc.kind || got.Hint != tc.hint || got.How != tc.how {
			t.Errorf("%s:\n got kind=%v hint=%q how=%q\nwant kind=%v hint=%q how=%q",
				tc.path, got.Kind, got.Hint, got.How, tc.kind, tc.hint, tc.how)
		}
	}
}

func TestMarkable(t *testing.T) {
	c := classifier()
	yes := []string{
		// Inside the protected folders everything can go.
		"/Users/me/Documents/thesis",
		"/Users/me/Documents/thesis/draft.docx",
		"/Users/me/Documents/private/app/node_modules",
		"/Users/me/Desktop/screenshot.png",
		"/Users/me/Pictures/2024/photo.raw",
		"/Users/me/Pictures/Photos Library.photoslibrary",
		"/Users/me/Music/album/track.flac",
		"/Users/me/Library/Application Support/MobileSync/Backup/00008110",
		"/Users/me/Movies/screen-recording-march.mov",
		"/Users/me/Downloads/Xcode_16.1.xip",
		"/Users/me/.npm",
		"/Users/me/Projects/web/node_modules",
		"/Users/me/Projects/old",
		"/Users/me/Library/Caches/com.example",
	}
	no := []string{
		"/Users/me",                             // home
		"/",                                     // never
		"/Users/me/Library",                     // too important whole
		"/Users/me/Library/Application Support", // application data
		"/Users/me/Downloads",                   // open it instead
		"/Users/me/.ssh",                        // keys
		"/Users/me/Documents",                   // the folder itself, not what is in it
		"/Users/me/Desktop",
		"/Users/me/Pictures",
		"/Users/me/Music",
		"/Users/me/Movies",
		"/Users/me/.claude", // recipe
		"/Users/me/.local/share/containers/podman", // recipe
	}
	for _, p := range yes {
		if !c.Path(p).Markable() {
			t.Errorf("%s should be markable", p)
		}
	}
	for _, p := range no {
		if c.Path(p).Markable() {
			t.Errorf("%s must not be markable", p)
		}
	}
}

// ○ marks a folder that cannot be removed whole, and nothing else.
func TestGlyphs(t *testing.T) {
	c := classifier()
	cases := map[string]string{
		"/Users/me/Documents":                             "○",
		"/Users/me/Desktop":                               "○",
		"/Users/me/Pictures":                              "○",
		"/Users/me/Music":                                 "○",
		"/Users/me/Downloads":                             "○",
		"/Users/me/Movies":                                "○",
		"/Users/me/Library":                               "○",
		"/Users/me/Library/Application Support":           "○",
		"/Users/me/.ssh":                                  "○",
		"/Users/me/.config":                               "○",
		"/Users/me/Documents/thesis":                      "",
		"/Users/me/Documents/thesis/draft.docx":           "",
		"/Users/me/Pictures/Photos Library.photoslibrary": "",
		"/Users/me/Downloads/Xcode_16.1.xip":              "",
		"/Users/me/Projects":                              "",
		"/Users/me/Documents/private/app/node_modules":    "●",
		"/Users/me/.npm":                                  "●",
		"/Users/me/.claude":                               "◆",
		"/Users/me/.local":                                "◆", // the recipe owns it
	}
	for path, want := range cases {
		if got := c.Path(path).Glyph(); got != want {
			t.Errorf("%s: glyph %q, want %q", path, got, want)
		}
	}
}

func TestWithoutRecipesTheirFoldersArePlain(t *testing.T) {
	c := New("/Users/me", nil)
	if got := c.Path("/Users/me/.claude"); got.Kind != recipe.Plain {
		t.Fatalf("no recipe detected, no ◆: %+v", got)
	}
}

func TestOpaque(t *testing.T) {
	c := classifier()
	if !c.Opaque("/Users/me/Pictures/Photos Library.photoslibrary", "Photos Library.photoslibrary") {
		t.Error("a Photos library is one item")
	}
	if !c.Opaque("/Users/me/Library/Application Support/MobileSync/Backup/00008110", "00008110") {
		t.Error("a device backup is one item")
	}
	if c.Opaque("/Users/me/Projects/web", "web") {
		t.Error("an ordinary folder is not opaque")
	}
}

func TestFileTypes(t *testing.T) {
	cases := map[string]FileType{
		"screen-recording.MOV": Video, "Xcode_16.1.xip": Archives, "ubuntu.iso": DiskImages,
		"podman-machine.raw": DiskImages, "events.parquet": AllTypes, "noext": AllTypes,
	}
	for name, want := range cases {
		if got := TypeOf(name); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
}
