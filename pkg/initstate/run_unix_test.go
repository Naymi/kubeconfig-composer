//go:build !windows

package initstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestRunBusyLockWithOutputSkipsRebuild(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	f.source(t, "prod.yaml", "prod", "https://prod")
	_, lockPath := f.paths(t)
	held, err := TryLock(lockPath)
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

	_, lockPath := f.paths(t)
	held, _ := TryLock(lockPath)
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

func TestRunBusyLockOfOtherPairDoesNotBlock(t *testing.T) {
	a := newFixture(t)
	b := newFixture(t)
	b.opts.StateDir = a.state
	b.state = a.state
	a.source(t, "a.yaml", "a", "https://a")
	b.source(t, "b.yaml", "b", "https://b")
	a.run(t)
	b.run(t)

	_, lockA := a.paths(t)
	held, err := TryLock(lockA)
	if err != nil || held == nil {
		t.Fatalf("не удалось занять блокировку пары A: %v", err)
	}
	defer held.Unlock()

	b.source(t, "b2.yaml", "b2", "https://b2")
	if res := b.run(t); !res.Rebuilt {
		t.Fatal("блокировка пары A не должна мешать пересборке пары B")
	}
}

// symlinkedKube делает каталог источников ссылкой: root/home/.kube ->
// root/dotfiles/kube, как при dotfiles-репозитории.
func symlinkedKube(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	real := filepath.Join(f.root, "dotfiles", "kube")
	link := filepath.Join(f.root, "home", ".kube")
	for _, d := range []string{real, filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f.dir = link
	f.output = filepath.Join(link, "kc.yaml")
	f.opts.Dir = f.dir
	f.opts.Output = f.output
	return f
}

func TestRunSymlinkedDirIsWalked(t *testing.T) {
	f := symlinkedKube(t)
	f.source(t, "dev.yaml", "dev", "https://dev")

	res := f.run(t)
	if res.Output != f.output {
		t.Errorf("Output = %q, ожидался путь через ссылку %q", res.Output, f.output)
	}
	if got := contextNames(f.load(t)); len(got) != 1 || got[0] != "kc:dev:dev.yaml" {
		t.Fatalf("каталог-ссылка не обойдён, контексты: %v", got)
	}
}

func TestRunOutputInsideSymlinkedDirIsNotASource(t *testing.T) {
	f := symlinkedKube(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.source(t, "prod.yaml", "prod", "https://prod")
	f.run(t)
	// Запись с типовым именем: если kc.yaml прочитан как источник, она
	// превратится в kc:kc:kc.yaml.
	f.edit(t, func(cfg *clientcmdapi.Config) {
		cfg.Clusters["default"] = &clientcmdapi.Cluster{Server: "https://default"}
		cfg.AuthInfos["default"] = &clientcmdapi.AuthInfo{Token: "x"}
		cfg.Contexts["default"] = &clientcmdapi.Context{Cluster: "default", AuthInfo: "default"}
	})

	f.opts.Refresh = true
	f.run(t)
	got := contextNames(f.load(t))
	if len(got) != 2 {
		t.Fatalf("после двух сборок ожидалось 2 контекста: %v", got)
	}
	for _, n := range got {
		if strings.HasPrefix(n, "kc:kc:") {
			t.Errorf("kc.yaml стал источником сам себе: %v", got)
		}
	}
}
