//go:build !windows

package composer

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestScanDirectorySkipsUnreadableSubdir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("под root права на каталог не действуют")
	}
	root := t.TempDir()
	writeCfgFile(t, filepath.Join(root, "a.yaml"), "a", "https://a")
	writeCfgFile(t, filepath.Join(root, "z", "b.yaml"), "b", "https://b")
	locked := filepath.Join(root, "locked")
	writeCfgFile(t, filepath.Join(locked, "hidden.yaml"), "h", "https://h")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	c := New(true)
	if err := c.ScanDirectory(root); err != nil {
		t.Fatalf("недоступный подкаталог не должен прерывать сканирование: %v", err)
	}
	var got []string
	for _, f := range c.GetLoadedFiles() {
		r, _ := filepath.Rel(root, f.GetFullPath())
		got = append(got, r)
	}
	sort.Strings(got)
	want := []string{"a.yaml", filepath.Join("z", "b.yaml")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
