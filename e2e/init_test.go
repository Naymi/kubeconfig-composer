//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type env struct{ home, kube string }

func newEnv(t *testing.T) *env {
	t.Helper()
	if os.Getenv("KC_BIN") == "" {
		t.Skip("KC_BIN не задан: e2e запускаются в контейнере (task test:container)")
	}
	home := t.TempDir()
	e := &env{home: home, kube: filepath.Join(home, ".kube")}
	if err := os.MkdirAll(e.kube, 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) cmd(args ...string) *exec.Cmd {
	c := exec.Command(os.Getenv("KC_BIN"), args...)
	c.Env = []string{"HOME=" + e.home, "PATH=" + os.Getenv("PATH")}
	return c
}

func (e *env) run(args ...string) (stdout, stderr string, code int) {
	c := e.cmd(args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb // Stdin не задан: это /dev/null, читать вопросы нельзя
	err := c.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	return out.String(), errb.String(), code
}

func (e *env) source(t *testing.T, rel, ctx, server string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[ctx+"-c"] = &clientcmdapi.Cluster{Server: server}
	cfg.AuthInfos[ctx+"-u"] = &clientcmdapi.AuthInfo{Token: "t-" + ctx}
	cfg.Contexts[ctx] = &clientcmdapi.Context{Cluster: ctx + "-c", AuthInfo: ctx + "-u"}
	cfg.CurrentContext = ctx
	path := filepath.Join(e.kube, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
}

func (e *env) output() string { return filepath.Join(e.kube, "kc.yaml") }

func (e *env) load(t *testing.T) *clientcmdapi.Config {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(e.output())
	if err != nil {
		t.Fatalf("kc.yaml не читается: %v", err)
	}
	return cfg
}

func TestInitStdoutIsOnlyExportAndStderrQuiet(t *testing.T) {
	e := newEnv(t)
	e.source(t, "dev.yaml", "dev", "https://dev")
	want := fmt.Sprintf("export KUBECONFIG='%s'\n", e.output())

	for i, name := range []string{"первый запуск", "повторный запуск"} {
		stdout, stderr, code := e.run("init")
		if code != 0 || stdout != want || stderr != "" {
			t.Fatalf("%s (#%d): code=%d stdout=%q stderr=%q", name, i, code, stdout, stderr)
		}
	}
}

func TestInitIgnoresCacheDirAndStaysFast(t *testing.T) {
	e := newEnv(t)
	e.source(t, "dev.yaml", "dev", "https://dev")
	e.source(t, "cache/http/leak.yaml", "leak", "https://leak")
	for i := 0; i < 10000; i++ {
		p := filepath.Join(e.kube, "cache", "http", fmt.Sprintf("f%05d", i))
		if err := os.WriteFile(p, []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, code := e.run("init"); code != 0 {
		t.Fatalf("первый запуск: код %d", code)
	}
	if _, ok := e.load(t).Contexts["kc:leak:cache/http/leak.yaml"]; ok {
		t.Error("kubeconfig из cache/ попал в kc.yaml")
	}

	start := time.Now()
	if _, _, code := e.run("init"); code != 0 {
		t.Fatalf("второй запуск: код %d", code)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("запуск без изменений занял %s при 10000 файлов в cache/", d)
	}
}

func TestInitPreservesSessionState(t *testing.T) {
	e := newEnv(t)
	e.source(t, "a.yaml", "a", "https://a")
	e.source(t, "b.yaml", "b", "https://b")
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("первый запуск")
	}

	cfg := e.load(t)
	cfg.CurrentContext = "kc:b:b.yaml"
	cfg.Contexts["kc:a:a.yaml"].Namespace = "team-a"
	if err := clientcmd.WriteToFile(*cfg, e.output()); err != nil {
		t.Fatal(err)
	}

	e.source(t, "c.yaml", "c", "https://c")
	if _, stderr, code := e.run("init"); code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	got := e.load(t)
	if got.CurrentContext != "kc:b:b.yaml" || got.Contexts["kc:a:a.yaml"].Namespace != "team-a" {
		t.Errorf("состояние сессии потеряно: current=%q ns=%q", got.CurrentContext, got.Contexts["kc:a:a.yaml"].Namespace)
	}
	if len(got.Contexts) != 3 {
		t.Errorf("ожидалось 3 контекста: %d", len(got.Contexts))
	}
}

func TestInitThresholdWarnsAndRefreshRebuilds(t *testing.T) {
	e := newEnv(t)
	e.source(t, "dev.yaml", "dev", "https://dev")
	if _, _, code := e.run("init", "--max-size", "1"); code != 0 {
		t.Fatal("первый запуск")
	}

	e.source(t, "big.yaml", "big", "https://big")
	f, err := os.OpenFile(filepath.Join(e.kube, "big.yaml"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("# " + strings.Repeat("x", 2<<20) + "\n")
	f.Close()

	stdout, stderr, code := e.run("init", "--max-size", "1")
	if code != 0 || !strings.HasPrefix(stdout, "export KUBECONFIG=") || !strings.Contains(stderr, "--refresh") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(e.load(t).Contexts) != 1 {
		t.Error("kc.yaml изменился при превышении порога")
	}

	if _, _, code := e.run("init", "--max-size", "1", "--refresh"); code != 0 {
		t.Fatal("--refresh")
	}
	if len(e.load(t).Contexts) != 2 {
		t.Error("--refresh не пересобрал kc.yaml")
	}
}

func TestInitForeignEntryWarnsBacksUpAndRevertSeesBackup(t *testing.T) {
	e := newEnv(t)
	e.source(t, "dev.yaml", "dev", "https://dev")
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("первый запуск")
	}

	cfg := e.load(t)
	cfg.Clusters["mine-c"] = &clientcmdapi.Cluster{Server: "https://mine"}
	cfg.AuthInfos["mine-u"] = &clientcmdapi.AuthInfo{Token: "x"}
	cfg.Contexts["mine"] = &clientcmdapi.Context{Cluster: "mine-c", AuthInfo: "mine-u"}
	if err := clientcmd.WriteToFile(*cfg, e.output()); err != nil {
		t.Fatal(err)
	}
	e.source(t, "prod.yaml", "prod", "https://prod")

	_, stderr, code := e.run("init")
	if code != 0 || !strings.Contains(stderr, "mine") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	backups, _ := filepath.Glob(filepath.Join(e.home, ".kubeconfig-composer", "backups", "kc.yaml.backup.*"))
	if len(backups) != 1 {
		t.Fatalf("бэкапов: %v", backups)
	}

	out, _, code := e.run("revert", "--list", "--output", e.output())
	if code != 0 || !strings.Contains(out, filepath.Base(backups[0])) {
		t.Errorf("revert не видит бэкап: code=%d out=%q", code, out)
	}
}

func TestInitParallelStartsProduceValidFile(t *testing.T) {
	e := newEnv(t)
	e.source(t, "a.yaml", "a", "https://a")
	e.source(t, "b.yaml", "b", "https://b")

	var wg sync.WaitGroup
	results := make([]string, 10)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stdout, stderr, code := e.run("init")
			results[i] = fmt.Sprintf("%d|%s|%s", code, stdout, stderr)
		}(i)
	}
	wg.Wait()

	want := fmt.Sprintf("0|export KUBECONFIG='%s'\n|", e.output())
	for i, r := range results {
		if r != want {
			t.Errorf("запуск #%d: %q", i, r)
		}
	}
	if got := len(e.load(t).Contexts); got != 2 {
		t.Errorf("контекстов %d, ожидалось 2", got)
	}
}

func TestInitSameContextNameInTwoFilesNeedsNoInput(t *testing.T) {
	e := newEnv(t)
	e.source(t, "a.yaml", "prod", "https://one")
	e.source(t, "b.yaml", "prod", "https://two")

	if _, stderr, code := e.run("init"); code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if got := len(e.load(t).Contexts); got != 2 {
		t.Errorf("контекстов %d, ожидалось 2", got)
	}
}

func TestInitFailureModes(t *testing.T) {
	e := newEnv(t)

	// Нет каталога источников и нет kc.yaml: stdout пуст, код 1.
	stdout, stderr, code := e.run("init", "--dir", filepath.Join(e.home, "nope"), "--output", filepath.Join(e.home, "out", "kc.yaml"))
	if code != 1 || stdout != "" || stderr == "" {
		t.Errorf("нет каталога: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// Битый источник рядом с хорошим: предупреждение, export, код 0.
	e.source(t, "dev.yaml", "dev", "https://dev")
	if err := os.WriteFile(filepath.Join(e.kube, "bad.yaml"), []byte("apiVersion: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = e.run("init")
	if code != 0 || !strings.HasPrefix(stdout, "export KUBECONFIG=") || !strings.Contains(stderr, "bad.yaml") {
		t.Errorf("битый источник: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestInitUnchangedRunDoesNotRewriteOutput(t *testing.T) {
	e := newEnv(t)
	e.source(t, "dev.yaml", "dev", "https://dev")
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("первый запуск")
	}

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(e.output(), old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("повторный запуск")
	}
	fi, err := os.Stat(e.output())
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("kc.yaml перезаписан без изменений источников: mtime=%s", fi.ModTime())
	}
}

func TestInitReflectsSourceRemoval(t *testing.T) {
	e := newEnv(t)
	e.source(t, "a.yaml", "a", "https://a")
	e.source(t, "b.yaml", "b", "https://b")
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("первый запуск")
	}
	if got := len(e.load(t).Contexts); got != 2 {
		t.Fatalf("контекстов %d, ожидалось 2", got)
	}

	if err := os.Remove(filepath.Join(e.kube, "b.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("запуск после удаления источника")
	}
	got := e.load(t)
	if _, ok := got.Contexts["kc:b:b.yaml"]; ok {
		t.Error("контекст удалённого источника остался в kc.yaml")
	}
	if _, ok := got.Contexts["kc:a:a.yaml"]; !ok || len(got.Contexts) != 1 {
		t.Errorf("ожидался один контекст kc:a:a.yaml, получено %d", len(got.Contexts))
	}
}

func TestInitBrokenSourceWithExistingOutputKeepsGoodContext(t *testing.T) {
	e := newEnv(t)
	e.source(t, "dev.yaml", "dev", "https://dev")
	if _, _, code := e.run("init"); code != 0 {
		t.Fatal("первый запуск")
	}

	if err := os.WriteFile(filepath.Join(e.kube, "bad.yaml"), []byte("apiVersion: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := e.run("init")
	if code != 0 || !strings.HasPrefix(stdout, "export KUBECONFIG=") || !strings.Contains(stderr, "bad.yaml") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, ok := e.load(t).Contexts["kc:dev:dev.yaml"]; !ok {
		t.Error("хороший контекст пропал из kc.yaml")
	}
}
