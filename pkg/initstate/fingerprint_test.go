package initstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func touch(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func mustScan(t *testing.T, dir, output string, salt ...string) *Snapshot {
	t.Helper()
	s, err := Scan(dir, output, salt...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScanStableAndSensitive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kube") // корень скрытый, как настоящий ~/.kube
	output := filepath.Join(dir, "kc.yaml")
	touch(t, filepath.Join(dir, "config"), "aaaa", t0)
	touch(t, filepath.Join(dir, "nested", "dev.yaml"), "bb", t0)

	base := mustScan(t, dir, output, "v1", "kc")
	if base.Files != 2 || base.TotalSize != 6 {
		t.Fatalf("Files=%d TotalSize=%d, ожидалось 2 и 6", base.Files, base.TotalSize)
	}
	if again := mustScan(t, dir, output, "v1", "kc"); again.Hash != base.Hash {
		t.Error("отпечаток нестабилен между двумя обходами")
	}

	touch(t, filepath.Join(dir, "config"), "aaaa", t0.Add(time.Second))
	if mustScan(t, dir, output, "v1", "kc").Hash == base.Hash {
		t.Error("смена mtime не меняет отпечаток")
	}
	touch(t, filepath.Join(dir, "config"), "aaaaa", t0)
	if mustScan(t, dir, output, "v1", "kc").Hash == base.Hash {
		t.Error("смена размера не меняет отпечаток")
	}
	touch(t, filepath.Join(dir, "config"), "aaaa", t0)
	if mustScan(t, dir, output, "v2", "kc").Hash == base.Hash {
		t.Error("смена версии не меняет отпечаток")
	}
	if mustScan(t, dir, output, "v1", "src").Hash == base.Hash {
		t.Error("смена префикса не меняет отпечаток")
	}
}

func TestScanIgnoresServiceFilesAndOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kube")
	output := filepath.Join(dir, "kc.yaml")
	touch(t, filepath.Join(dir, "config"), "aaaa", t0)
	base := mustScan(t, dir, output)

	touch(t, filepath.Join(dir, "cache", "http", "x"), "junk", t0)
	touch(t, filepath.Join(dir, "http-cache", "y"), "junk", t0)
	touch(t, filepath.Join(dir, ".git", "z"), "junk", t0)
	touch(t, filepath.Join(dir, ".DS_Store"), "junk", t0)
	touch(t, output, "derived", t0)
	// Остаток оборванной записи (cleaner.WriteFileAtomic) не должен считаться источником.
	touch(t, filepath.Join(dir, ".kc.yaml.tmp-123"), "half", t0)

	got := mustScan(t, dir, output)
	if got.Hash != base.Hash || got.Files != 1 || got.TotalSize != 4 {
		t.Fatalf("служебные файлы повлияли на результат: %+v vs %+v", got, base)
	}
}

// kubectx, kubens и client-go пишут своё состояние в ~/.kube при каждом
// переключении контекста; отпечаток от этого меняться не должен.
func TestScanIgnoresContextSwitcherState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kube")
	output := filepath.Join(dir, "kc.yaml")
	touch(t, filepath.Join(dir, "config"), "aaaa", t0)
	base := mustScan(t, dir, output)

	touch(t, filepath.Join(dir, "kubectx"), "prev-ctx", t0)
	touch(t, filepath.Join(dir, "kubens", "ctx"), "prev-ns", t0)
	touch(t, filepath.Join(dir, "config.lock"), "", t0)
	touch(t, filepath.Join(dir, "kc.yaml.lock"), "", t0)

	got := mustScan(t, dir, output)
	if got.Hash != base.Hash || got.Files != base.Files {
		t.Fatalf("служебные файлы kubectx/kubens/client-go повлияли на отпечаток: %+v vs %+v", got, base)
	}

	// Контроль: настоящий новый источник отпечаток меняет.
	touch(t, filepath.Join(dir, "x.yaml"), "bb", t0)
	if mustScan(t, dir, output).Hash == base.Hash {
		t.Error("новый источник x.yaml не меняет отпечаток")
	}
}

func TestScanFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kube")
	target := filepath.Join(root, "elsewhere", "real.yaml")
	touch(t, target, "aaaa", t0)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.yaml")); err != nil {
		t.Skipf("symlink недоступен: %v", err)
	}
	output := filepath.Join(dir, "kc.yaml")

	base := mustScan(t, dir, output)
	if base.Files != 1 {
		t.Fatalf("Files=%d, ссылка на файл должна считаться источником", base.Files)
	}
	touch(t, target, "aaaa", t0.Add(time.Minute))
	if mustScan(t, dir, output).Hash == base.Hash {
		t.Error("изменение цели ссылки не меняет отпечаток")
	}
}

func TestScanMissingRootIsError(t *testing.T) {
	if _, err := Scan(filepath.Join(t.TempDir(), "nope"), "kc.yaml"); err == nil {
		t.Fatal("ожидалась ошибка для несуществующего каталога")
	}
}
