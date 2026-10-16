//go:build !windows

package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// lockDir создаёт каталог без прав и возвращает права в t.Cleanup, иначе
// t.TempDir не сможет его удалить.
func lockDir(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("под root права на каталог не действуют")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func TestFindKubeconfigFilesSkipsUnreadableSubdir(t *testing.T) {
	root := t.TempDir()
	writeKubeconfig(t, filepath.Join(root, "a.yaml"))
	writeKubeconfig(t, filepath.Join(root, "z", "b.yaml"))
	writeKubeconfig(t, filepath.Join(root, "locked", "hidden.yaml"))
	lockDir(t, filepath.Join(root, "locked"))

	files, err := FindKubeconfigFiles(root)
	if err != nil {
		t.Fatalf("недоступный подкаталог не должен прерывать обход: %v", err)
	}
	var rel []string
	for _, f := range files {
		r, _ := filepath.Rel(root, f)
		rel = append(rel, r)
	}
	sort.Strings(rel)
	want := []string{"a.yaml", filepath.Join("z", "b.yaml")}
	if len(rel) != len(want) || rel[0] != want[0] || rel[1] != want[1] {
		t.Fatalf("got %v, want %v", rel, want)
	}
}
