//go:build darwin

package git

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KeepBackups is how long the bundles of deleted branches are kept.
const KeepBackups = 30 * 24 * time.Hour

// BackupDir is where the bundles go: ~/.local/state/space/git.
func (r *Recipe) BackupDir() string { return filepath.Join(r.env.StateDir, "git") }

// bundlePath names the bundle of one repository for one run. Two
// repositories with the same folder name, or two runs on one repository,
// never share a file: the name carries a mark of the full path and the time.
func (r *Recipe) bundlePath(repo Repo, now time.Time) string {
	h := fnv.New32a()
	h.Write([]byte(repo.Path))
	slug := strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			return c
		}
		return '-'
	}, filepath.Base(repo.Path))
	name := fmt.Sprintf("%s-%06x-%s.bundle", slug, h.Sum32()&0xffffff, now.Format("20060102-150405"))
	return filepath.Join(r.BackupDir(), name)
}

// oldBackups lists the bundles that are past their time.
func (r *Recipe) oldBackups(now time.Time) []string {
	entries, err := os.ReadDir(r.BackupDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".bundle") {
			continue
		}
		if now.Sub(info.ModTime()) > KeepBackups {
			out = append(out, filepath.Join(r.BackupDir(), e.Name()))
		}
	}
	return out
}
