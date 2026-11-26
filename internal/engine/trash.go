//go:build darwin

package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Trasher moves a path to the Trash.
type Trasher interface {
	Trash(path string) error
}

const trashTool = "/usr/bin/trash"

// SystemTrash picks how to reach the Trash: the trash tool that ships with
// macOS 15 and later, else a move into ~/.Trash. Under SPACE_HOME the
// sandbox's own .Trash is always used, so tests never touch the real one.
func SystemTrash(home string) Trasher {
	if os.Getenv("SPACE_HOME") == "" {
		if _, err := os.Stat(trashTool); err == nil {
			return toolTrash{}
		}
	}
	return DirTrash{Dir: filepath.Join(home, ".Trash")}
}

type toolTrash struct{}

func (toolTrash) Trash(path string) error {
	out, err := exec.Command(trashTool, "-s", path).CombinedOutput()
	if err != nil {
		// The tool prints a long Cocoa error; the human sentence is the
		// first quoted part of it.
		msg := strings.TrimSpace(string(out))
		if i := strings.Index(msg, `"`); i >= 0 {
			if j := strings.Index(msg[i+1:], `"`); j > 0 {
				msg = msg[i+1 : i+1+j]
			}
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("could not move to the Trash: %s", msg)
	}
	return nil
}

// DirTrash moves paths into a directory, the way Finder fills ~/.Trash: a
// name that is already taken gets the time appended.
type DirTrash struct{ Dir string }

func (t DirTrash) Trash(path string) error {
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	name := filepath.Base(path)
	dst := filepath.Join(t.Dir, name)
	if _, err := os.Lstat(dst); err == nil {
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		stamp := time.Now().Format("15.04.05")
		for i := 0; ; i++ {
			suffix := " " + stamp
			if i > 0 {
				suffix = fmt.Sprintf(" %s %d", stamp, i)
			}
			dst = filepath.Join(t.Dir, stem+suffix+ext)
			if _, err := os.Lstat(dst); err != nil {
				break
			}
		}
	}
	return os.Rename(path, dst)
}
