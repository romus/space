//go:build darwin

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// remove deletes or trashes the paths of a step, one by one, refusing
// anything outside the step's root.
//
// The paths are independent, so one that cannot be removed does not stop
// the rest: it is reported as a Failure with the reason. Only a root that
// is not allowed at all fails the whole step.
func (e *Engine) remove(ctx context.Context, r *recipe.Remove, out io.Writer) (int64, []Failure, error) {
	root, err := CheckRoot(r.Root, e.Home)
	if err != nil {
		return 0, nil, err
	}
	trash := e.Trash
	if r.ToTrash && trash == nil {
		trash = SystemTrash(e.Home)
	}
	var (
		total    int64
		failures []Failure
	)
	for i, p := range r.Paths {
		if ctx.Err() != nil {
			return total, failures, ctx.Err()
		}
		var ino uint64
		if i < len(r.Inodes) {
			ino = r.Inodes[i]
		}
		size, err := e.removeOne(p, root, ino, r.ToTrash, trash)
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(out, "already gone: %s\n", humanize.Tilde(p, e.Home))
		case err != nil:
			f := Failure{Path: p, Reason: Explain(err), Err: err}
			failures = append(failures, f)
			fmt.Fprintf(out, "could not remove %s: %s\n", humanize.Tilde(p, e.Home), f.Reason)
		default:
			total += size
			verb := "removed"
			if r.ToTrash {
				verb = "trashed"
			}
			fmt.Fprintf(out, "%s %s  %s\n", verb, humanize.Bytes(size), humanize.Tilde(p, e.Home))
		}
	}
	return total, failures, nil
}

// Explain says in plain words why a path could not be removed.
func Explain(err error) string {
	switch {
	case errors.Is(err, ErrUnsafe), errors.Is(err, ErrChanged):
		return err.Error()
	case errors.Is(err, fs.ErrPermission):
		return "macOS did not allow it. Give your terminal Full Disk Access in " +
			"System Settings › Privacy & Security, or remove it in Finder"
	case errors.Is(err, syscall.EBUSY):
		return "in use by another program"
	case errors.Is(err, syscall.EROFS):
		return "the disk is read-only"
	}
	// The system's own words, without the path repeated in front.
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err.Error()
	}
	return err.Error()
}

// ErrUnsafe is returned when a path fails the safety checks.
var ErrUnsafe = errors.New("refused: outside the allowed folder")

// ErrChanged is returned when a path is not the one the plan was made for.
var ErrChanged = errors.New("refused: changed since the plan was made")

// CheckRoot decides whether files may be removed under root, and returns
// the root with symlinks resolved. Nothing may be removed under "/" or under
// any folder that contains the home directory.
func CheckRoot(root, home string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: no root", ErrUnsafe)
	}
	root = filepath.Clean(root)
	if root == "/" {
		return "", fmt.Errorf("%w: root is /", ErrUnsafe)
	}
	// The root may be the home directory, but never anything above it.
	if home != "" && root != home && strings.HasPrefix(home+"/", root+"/") {
		return "", fmt.Errorf("%w: root is above the home directory", ErrUnsafe)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, nil
}

func (e *Engine) removeOne(path, root string, ino uint64, toTrash bool, trash Trasher) (int64, error) {
	if !filepath.IsAbs(path) {
		return 0, ErrUnsafe
	}
	path = filepath.Clean(path)
	// Resolve the parent, not the path itself: a symlink is removed as a
	// link, but it must not be reached through a link that leaves the root.
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	real := filepath.Join(parent, filepath.Base(path))
	if real == root || !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return 0, ErrUnsafe
	}
	if real == "/" || real == e.Home || e.Home != "" && strings.HasPrefix(e.Home+"/", real+"/") {
		return 0, ErrUnsafe
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(real, &st); err != nil {
		return 0, err
	}
	if ino != 0 && st.Ino != ino {
		return 0, ErrChanged
	}
	size := scan.SizeOf(real)
	if toTrash {
		// One at a time: a Trash that picks a free name and then moves the
		// file there would let two groups pick the same name.
		e.trashMu.Lock()
		defer e.trashMu.Unlock()
		return size, trash.Trash(real)
	}
	return size, os.RemoveAll(real)
}
