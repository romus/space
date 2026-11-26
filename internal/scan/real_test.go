//go:build darwin

package scan

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestRealScan measures a real folder and prints its largest children, to
// compare against du and to time the scanner. It only reads. Run it with:
//
//	SPACE_REAL_SCAN=$HOME go test ./internal/scan -run TestRealScan -v
func TestRealScan(t *testing.T) {
	root := os.Getenv("SPACE_REAL_SCAN")
	if root == "" {
		t.Skip("set SPACE_REAL_SCAN to a folder to measure it")
	}
	s, err := New(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Run(context.Background())
	p := s.Progress()
	fmt.Printf("%d files, %d folders, %d not readable, %.1f s\n", p.Files, p.Dirs, p.Unreadable, p.Elapsed.Seconds())
	fmt.Printf("%12d KB  %s\n", s.Root.Size()/1024, root)
	for i, d := range s.Children(s.Root) {
		if i == 12 {
			break
		}
		fmt.Printf("%12d KB  %s\n", d.Size()/1024, d.Name)
	}
}
