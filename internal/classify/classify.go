// Package classify decides how a path may be removed: to the Trash, safely
// as a cache, through a recipe, or not at all.
package classify

import (
	"path/filepath"
	"strings"

	"github.com/romus/space/pkg/recipe"
)

// Class is the verdict for one path.
type Class struct {
	Kind      recipe.Kind
	Hint      string // short label for the row: "npm cache"
	How       string // the REMOVED VIA column: "Trash", "Podman recipe"
	RecipeID  string // set when a recipe owns the path
	Protected bool   // plain, but too important to remove as a whole
}

// Markable reports whether the user may mark the path for removal.
func (c Class) Markable() bool {
	return !c.Protected && (c.Kind == recipe.Plain || c.Kind == recipe.Cache)
}

type owner struct {
	id, name, hint, path string
}

// Classifier holds the rules for one home directory.
type Classifier struct {
	home   string
	owners []owner
}

// New builds a classifier. recipes are the detected ones: the folders they
// own are marked ◆.
func New(home string, recipes []recipe.Recipe) *Classifier {
	c := &Classifier{home: filepath.Clean(home)}
	for _, r := range recipes {
		info := r.Info()
		if info.Path == "" {
			continue
		}
		c.owners = append(c.owners, owner{
			id: r.ID(), name: info.Name, hint: info.Hint, path: filepath.Clean(info.Path),
		})
	}
	return c
}

type rule struct {
	rel  string // path relative to home
	hint string
}

// Folders that hold caches: safe to delete, they come back.
var cacheRoots = []rule{
	{".npm", "npm cache"},
	{".cache", "tool caches"},
	{".pnpm-store", "pnpm store"},
	{".yarn/cache", "yarn cache"},
	{".gradle/caches", "Gradle cache"},
	{".m2/repository", "Maven cache"},
	{".cargo/registry", "Cargo registry"},
	{".bun/install/cache", "bun cache"},
	{"go/pkg/mod", "Go module cache"},
	{"Library/Caches", "app caches"},
	{"Library/Developer/Xcode/DerivedData", "Xcode build cache"},
	{"Library/Developer/CoreSimulator/Caches", "simulator caches"},
	{"Library/Logs", "logs"},
}

// Directory names that are caches wherever they appear.
var cacheNames = map[string]string{
	"node_modules": "node_modules",
	".cache":       "project cache",
	"__pycache__":  "Python bytecode",
	"DerivedData":  "Xcode build cache",
}

// Folders with personal data: space shows them and never removes them.
var personalRoots = []rule{
	{"Documents", "personal"},
	{"Desktop", "personal"},
	{"Pictures", "Photos · view only"},
	{"Music", "personal"},
	{"Library/Mail", "Mail · view only"},
	{"Library/Messages", "Messages · view only"},
	{"Library/Mobile Documents", "iCloud Drive"},
	{"Library/CloudStorage", "cloud storage"},
	{"Library/Application Support/MobileSync/Backup", "iPhone backup · Finder"},
	{"Library/Keychains", "keychains"},
	{"Library/Photos", "Photos · view only"},
}

// Folders that are plain but must never be removed whole.
var protectedRoots = []string{
	"Library", "Applications", ".Trash", ".ssh", ".gnupg", ".config", ".local",
	"Movies", "Downloads", "Public",
}

// Opaque package extensions: measured, then shown as a single item.
var bundleExt = map[string]string{
	".photoslibrary":  "Photos library",
	".imovielibrary":  "personal · bundle",
	".fcpbundle":      "personal · bundle",
	".tvlibrary":      "personal · bundle",
	".musiclibrary":   "personal · bundle",
	".app":            "application",
	".xcarchive":      "Xcode archive",
	".logicx":         "personal · bundle",
	".band":           "personal · bundle",
	".photosboothlib": "personal · bundle",
}

// Opaque reports directories the scanner should measure without descending
// for display: packages and device backups.
func (c *Classifier) Opaque(path, name string) bool {
	if _, ok := bundleExt[strings.ToLower(filepath.Ext(name))]; ok {
		return true
	}
	backups := filepath.Join(c.home, "Library/Application Support/MobileSync/Backup")
	return filepath.Dir(path) == backups
}

func under(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// Path classifies an absolute path.
func (c *Classifier) Path(path string) Class {
	path = filepath.Clean(path)

	// Recipes come first: what they own must not be touched by hand.
	for _, o := range c.owners {
		if under(path, o.path) {
			return Class{Kind: recipe.Special, Hint: o.hint, How: o.name + " recipe", RecipeID: o.id}
		}
		// A parent whose only purpose is the recipe's folder, like ~/.local
		// for ~/.local/share/containers, is shown as the recipe too.
		if under(o.path, path) && under(path, c.home) && path != c.home {
			return Class{Kind: recipe.Special, Hint: o.hint, How: o.name + " recipe", RecipeID: o.id}
		}
	}

	name := filepath.Base(path)
	if label, ok := bundleExt[strings.ToLower(filepath.Ext(name))]; ok && label != "application" && label != "Xcode archive" {
		return Class{Kind: recipe.Personal, Hint: label, How: label}
	}

	if under(path, c.home) {
		rel, _ := filepath.Rel(c.home, path)
		for _, r := range personalRoots {
			if under(rel, filepath.FromSlash(r.rel)) {
				hint := r.hint
				if rel != filepath.FromSlash(r.rel) && !strings.Contains(hint, "·") {
					hint = "personal"
				}
				return Class{Kind: recipe.Personal, Hint: hint, How: hint}
			}
		}
		for _, r := range cacheRoots {
			root := filepath.FromSlash(r.rel)
			if rel == root {
				return Class{Kind: recipe.Cache, Hint: r.hint, How: "cache"}
			}
			if under(rel, root) {
				return Class{Kind: recipe.Cache, Hint: "", How: "cache"}
			}
		}
	}

	// Cache directories by name, anywhere in the path.
	for p := path; p != "/" && p != "." && p != c.home; p = filepath.Dir(p) {
		if label, ok := cacheNames[filepath.Base(p)]; ok {
			how := "cache"
			if label == "project cache" || label == "node_modules" {
				how = "project cache"
			}
			hint := ""
			if p == path {
				hint = label
			}
			return Class{Kind: recipe.Cache, Hint: hint, How: how}
		}
	}

	cl := Class{Kind: recipe.Plain, How: "Trash"}
	if path == c.home || path == "/" {
		cl.Protected = true
	}
	if under(path, c.home) {
		rel, _ := filepath.Rel(c.home, path)
		for _, p := range protectedRoots {
			if rel == p {
				cl.Protected = true
			}
		}
		// Direct children of ~/Library hold application data.
		if filepath.Dir(rel) == "Library" {
			cl.Protected = true
		}
	}
	return cl
}

// Advice is the second line of the detail panel for a class.
func (c Class) Advice() string {
	switch {
	case c.Kind == recipe.Cache:
		return "Safe to remove — it will be rebuilt when needed. Mark it with space, then press enter for the plan."
	case c.Kind == recipe.Personal:
		return "Personal data — view only. space never removes it; manage it in the app that owns it."
	case c.Protected:
		return "Too important to remove whole. Open it with → and pick what to remove inside."
	}
	return "Press d to move it to the Trash, or mark several with space and press enter for the plan."
}

// Label names the kind in words.
func (c Class) Label() string {
	switch c.Kind {
	case recipe.Cache:
		return "cache"
	case recipe.Special:
		return "recipe"
	case recipe.Personal:
		return "personal"
	}
	return "files"
}

// FileType groups files for the type filter on the Files tab.
type FileType int

const (
	AllTypes FileType = iota
	Video
	Archives
	DiskImages
)

func (t FileType) String() string {
	switch t {
	case Video:
		return "video"
	case Archives:
		return "archives"
	case DiskImages:
		return "disk images"
	}
	return "all types"
}

var typeByExt = map[string]FileType{
	".mov": Video, ".mp4": Video, ".mkv": Video, ".avi": Video, ".m4v": Video, ".webm": Video,
	".zip": Archives, ".xip": Archives, ".tar": Archives, ".gz": Archives, ".tgz": Archives,
	".7z": Archives, ".rar": Archives, ".bz2": Archives, ".xz": Archives, ".zst": Archives, ".pkg": Archives,
	".dmg": DiskImages, ".iso": DiskImages, ".raw": DiskImages, ".qcow2": DiskImages,
	".img": DiskImages, ".vmdk": DiskImages, ".sparseimage": DiskImages,
}

// TypeOf returns the filter group of a file name.
func TypeOf(name string) FileType {
	return typeByExt[strings.ToLower(filepath.Ext(name))]
}
