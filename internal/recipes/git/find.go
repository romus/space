//go:build darwin

package git

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// find returns the repositories to work on. Inside a repository that is
// the repository itself. Above, it is every folder below root that has a
// .git folder; a repository found is not searched for more inside.
func find(ctx context.Context, root, home string) []string {
	if repo := enclosing(root, home); repo != "" {
		return []string{repo}
	}
	var out []string
	dev := device(root)
	todo := []string{root}
	for len(todo) > 0 && ctx.Err() == nil {
		dir := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		if kind := gitEntry(entries); kind != notRepo && dir != home {
			if kind == isRepo {
				out = append(out, dir)
			}
			continue // a worktree or submodule belongs to another repository
		}
		for _, e := range entries {
			if e.IsDir() && enter(dir, e.Name(), home) && device(filepath.Join(dir, e.Name())) == dev {
				todo = append(todo, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

const (
	notRepo = iota
	isRepo
	linked // .git is a file: a linked worktree or a submodule
)

func gitEntry(entries []os.DirEntry) int {
	for _, e := range entries {
		if e.Name() != ".git" {
			continue
		}
		if e.IsDir() {
			return isRepo
		}
		return linked
	}
	return notRepo
}

// enter says whether the search goes into a folder. Hidden folders hold
// clones that tools keep for themselves (plugin managers, package caches),
// and they are not the user's to tidy.
func enter(parent, name, home string) bool {
	switch {
	case strings.HasPrefix(name, "."), name == "node_modules":
		return false
	case name == "Library" && parent == home:
		return false
	}
	return true
}

// enclosing returns the repository that path is inside of, or "". The home
// folder is never one: a home kept in git for its dotfiles is not a
// project, and the repositories inside it are what the user came for.
func enclosing(path, home string) string {
	for dir := filepath.Clean(path); dir != home; dir = filepath.Dir(dir) {
		if fi, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			if fi.IsDir() {
				return dir
			}
			return "" // inside a linked worktree or a submodule
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return ""
}

// device identifies the volume a path is on; the search stays on one.
func device(path string) int32 {
	var st syscall.Stat_t
	if syscall.Lstat(path, &st) != nil {
		return -1
	}
	return st.Dev
}
