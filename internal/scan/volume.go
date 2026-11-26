//go:build darwin

package scan

import (
	"os"
	"path/filepath"
	"syscall"
)

// Volume describes the disk a path lives on.
type Volume struct {
	Name  string // "Macintosh HD"
	Mount string // mount point
	Total int64
	Free  int64 // available to the user
}

// Used is the space taken on the volume.
func (v Volume) Used() int64 { return v.Total - v.Free }

// VolumeOf reads the size and name of the volume holding path.
func VolumeOf(path string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Volume{}, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	mount := cString(st.Mntonname[:])
	v := Volume{
		Mount: mount,
		Total: int64(st.Blocks) * int64(st.Bsize),
		Free:  int64(st.Bavail) * int64(st.Bsize),
	}
	v.Name = volumeName(mount)
	return v, nil
}

// FreeBytes is the space available on the volume holding path.
func FreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

func cString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// volumeName turns a mount point into the name Finder shows. The system
// volume and its data volume share one name: the entry in /Volumes that
// links to "/".
func volumeName(mount string) string {
	if mount != "/" && mount != "/System/Volumes/Data" {
		return filepath.Base(mount)
	}
	entries, err := os.ReadDir("/Volumes")
	if err == nil {
		for _, e := range entries {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if target, err := os.Readlink(filepath.Join("/Volumes", e.Name())); err == nil && target == "/" {
				return e.Name()
			}
		}
	}
	return "Macintosh HD"
}
