//go:build !windows

package initstate

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTryLockExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "init.lock")

	first, err := TryLock(path)
	if err != nil || first == nil {
		t.Fatalf("первая блокировка: lock=%v err=%v", first, err)
	}
	second, err := TryLock(path)
	if err != nil || second != nil {
		t.Fatalf("вторая блокировка должна быть занята: lock=%v err=%v", second, err)
	}
	first.Unlock()
	third, err := TryLock(path)
	if err != nil || third == nil {
		t.Fatalf("после Unlock блокировка должна браться: lock=%v err=%v", third, err)
	}
	third.Unlock()
}

func TestWaitLockTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lock")
	held, _ := TryLock(path)
	defer held.Unlock()

	start := time.Now()
	if _, err := WaitLock(path, 150*time.Millisecond); err == nil {
		t.Fatal("ожидалась ошибка по таймауту")
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("WaitLock вернулся раньше таймаута")
	}
}

func TestWaitLockAcquiresAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lock")
	held, _ := TryLock(path)
	go func() {
		time.Sleep(100 * time.Millisecond)
		held.Unlock()
	}()

	l, err := WaitLock(path, 5*time.Second)
	if err != nil || l == nil {
		t.Fatalf("lock=%v err=%v", l, err)
	}
	l.Unlock()
}
