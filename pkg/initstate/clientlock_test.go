package initstate

import (
	"errors"
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
	_, err := lockKubeconfig(path, 50*time.Millisecond)
	if !errors.Is(err, errKubeconfigLockBusy) {
		t.Fatalf("занятый lock должен дать errKubeconfigLockBusy по таймауту, получено %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("таймаут не соблюдён")
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("чужой lock-файл нельзя удалять: %v", err)
	}
}

func TestLockKubeconfigCreatesMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "sub", "kc.yaml")
	unlock, err := lockKubeconfig(path, time.Second)
	if err != nil {
		t.Fatalf("каталога ещё нет — его нужно создать: %v", err)
	}
	defer unlock()
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("lock-файл не создан: %v", err)
	}
}

func TestLockKubeconfigOtherErrorIsNotBusy(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := lockKubeconfig(filepath.Join(plain, "kc.yaml"), time.Second)
	if err == nil || errors.Is(err, errKubeconfigLockBusy) {
		t.Fatalf("каталог — файл: ожидалась ошибка, отличная от «занят», получено %v", err)
	}
}
