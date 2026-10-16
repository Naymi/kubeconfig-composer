//go:build !windows

package initstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunBusyLockWithOutputSkipsRebuild(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	f.source(t, "prod.yaml", "prod", "https://prod")
	held, err := TryLock(filepath.Join(f.state, "init.lock"))
	if err != nil || held == nil {
		t.Fatalf("не удалось занять блокировку: %v", err)
	}
	defer held.Unlock()

	res, err := Run(f.opts)
	if err != nil || res.Rebuilt || !res.ExportOK {
		t.Fatalf("при занятой блокировке и готовом kc.yaml пересборки быть не должно: res=%+v err=%v", res, err)
	}
	if got := contextNames(f.load(t)); len(got) != 1 {
		t.Errorf("kc.yaml не должен был измениться: %v", got)
	}
}

func TestRunBusyLockWithoutOutputWaitsThenSkipsDuplicateWork(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.opts.LockTimeout = 5 * time.Second

	held, _ := TryLock(filepath.Join(f.state, "init.lock"))
	go func() {
		time.Sleep(150 * time.Millisecond)
		held.Unlock()
	}()

	res, err := Run(f.opts)
	if err != nil || !res.ExportOK {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, statErr := os.Stat(f.output); statErr != nil {
		t.Errorf("kc.yaml не создан: %v", statErr)
	}
}
