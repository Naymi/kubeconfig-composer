package initstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockKubeconfigCreatesAndRemovesLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kc.yaml")
	unlock, err := lockKubeconfig(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("lock-файл client-go не создан: %v", err)
	}
	unlock()
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock-файл должен быть удалён, stat: %v", err)
	}
}

func TestLockKubeconfigWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kc.yaml")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Remove(path + ".lock")
	}()
	unlock, err := lockKubeconfig(path, 5*time.Second)
	if err != nil {
		t.Fatalf("ожидалось дождаться снятия lock: %v", err)
	}
	unlock()
}

func TestLockKubeconfigTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kc.yaml")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := lockKubeconfig(path, 50*time.Millisecond); err == nil {
		t.Fatal("занятый lock должен дать ошибку по таймауту")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("таймаут не соблюдён")
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("чужой lock-файл нельзя удалять: %v", err)
	}
}
