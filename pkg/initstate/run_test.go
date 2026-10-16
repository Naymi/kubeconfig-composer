package initstate

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type fixture struct {
	root, dir, output, state string
	opts                     Options
	log                      *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		root:  root,
		dir:   filepath.Join(root, ".kube"),
		state: filepath.Join(root, "state"),
		log:   &bytes.Buffer{},
	}
	f.output = filepath.Join(f.dir, "kc.yaml")
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.opts = Options{
		Dir: f.dir, Output: f.output, Prefix: "kc", MaxSizeMB: 5,
		Version: "test", StateDir: f.state, Log: f.log,
	}
	return f
}

// source пишет источник с контекстом ctx; имя файла = file.
func (f *fixture) source(t *testing.T, file, ctx, server string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[ctx+"-c"] = &clientcmdapi.Cluster{Server: server}
	cfg.AuthInfos[ctx+"-u"] = &clientcmdapi.AuthInfo{Token: "t-" + ctx}
	cfg.Contexts[ctx] = &clientcmdapi.Context{Cluster: ctx + "-c", AuthInfo: ctx + "-u"}
	cfg.CurrentContext = ctx
	path := filepath.Join(f.dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) run(t *testing.T) Result {
	t.Helper()
	res, err := Run(f.opts)
	if err != nil {
		t.Fatalf("Run: %v\nlog: %s", err, f.log)
	}
	return res
}

func (f *fixture) load(t *testing.T) *clientcmdapi.Config {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(f.output)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func contextNames(cfg *clientcmdapi.Config) []string {
	var names []string
	for n := range cfg.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestRunFirstBuildThenNoop(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")

	res := f.run(t)
	if !res.ExportOK || !res.Rebuilt || res.Output != f.output {
		t.Fatalf("первый запуск: %+v", res)
	}
	if got := contextNames(f.load(t)); len(got) != 1 || got[0] != "kc:dev:dev.yaml" {
		t.Fatalf("контексты: %v", got)
	}

	// Помечаем kc.yaml старым mtime: если второй запуск его перепишет, метка сменится.
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(f.output, old, old); err != nil {
		t.Fatal(err)
	}
	res = f.run(t)
	if res.Rebuilt || !res.ExportOK {
		t.Fatalf("повторный запуск без изменений: %+v", res)
	}
	if info, _ := os.Stat(f.output); !info.ModTime().Equal(old) {
		t.Error("kc.yaml переписан без изменений источников")
	}
	if f.log.Len() != 0 {
		t.Errorf("в штатном случае stderr должен быть пуст, получено: %s", f.log)
	}
}

func TestRunAddAndRemoveSource(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	f.source(t, "prod.yaml", "prod", "https://prod")
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("новый источник должен вызвать пересборку")
	}
	// kc.yaml лежит внутри каталога источников и сам источником не считается.
	if got := contextNames(f.load(t)); len(got) != 2 {
		t.Fatalf("ожидалось 2 контекста, есть %v", got)
	}

	if err := os.Remove(filepath.Join(f.dir, "dev.yaml")); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if got := contextNames(f.load(t)); len(got) != 1 || got[0] != "kc:prod:prod.yaml" {
		t.Fatalf("после удаления источника: %v", got)
	}
	if f.log.Len() != 0 {
		t.Errorf("удаление источника — штатно, stderr должен быть пуст: %s", f.log)
	}
}

func TestRunCarriesSessionState(t *testing.T) {
	f := newFixture(t)
	f.source(t, "a.yaml", "a", "https://a")
	f.source(t, "b.yaml", "b", "https://b")
	f.run(t)

	// Как будто пользователь сделал `kubectl config use-context` и `kubens`.
	cfg := f.load(t)
	cfg.CurrentContext = "kc:b:b.yaml"
	cfg.Contexts["kc:a:a.yaml"].Namespace = "team-a"
	if err := clientcmd.WriteToFile(*cfg, f.output); err != nil {
		t.Fatal(err)
	}

	f.source(t, "c.yaml", "c", "https://c")
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("ожидалась пересборка")
	}

	got := f.load(t)
	if got.CurrentContext != "kc:b:b.yaml" {
		t.Errorf("current-context = %q", got.CurrentContext)
	}
	if got.Contexts["kc:a:a.yaml"].Namespace != "team-a" {
		t.Errorf("namespace = %q", got.Contexts["kc:a:a.yaml"].Namespace)
	}
}

func TestRunSizeThreshold(t *testing.T) {
	f := newFixture(t)
	f.opts.MaxSizeMB = 1
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	// Большой источник: валидный kubeconfig с длинным комментарием.
	f.source(t, "big.yaml", "big", "https://big")
	big, err := os.OpenFile(filepath.Join(f.dir, "big.yaml"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	big.WriteString("# " + strings.Repeat("x", 2<<20) + "\n")
	big.Close()

	res := f.run(t)
	if res.Rebuilt || !res.ExportOK {
		t.Fatalf("порог превышен, пересборки быть не должно: %+v", res)
	}
	if !strings.Contains(f.log.String(), "--refresh") {
		t.Errorf("в stderr нет подсказки про --refresh: %q", f.log)
	}
	if got := contextNames(f.load(t)); len(got) != 1 {
		t.Errorf("kc.yaml не должен был измениться: %v", got)
	}

	f.opts.Refresh = true
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("--refresh должен игнорировать порог")
	}
	if got := contextNames(f.load(t)); len(got) != 2 {
		t.Errorf("после --refresh ожидалось 2 контекста: %v", got)
	}
}

func TestRunFirstBuildIgnoresThreshold(t *testing.T) {
	f := newFixture(t)
	f.opts.MaxSizeMB = 1
	f.source(t, "big.yaml", "big", "https://big")
	big, _ := os.OpenFile(filepath.Join(f.dir, "big.yaml"), os.O_APPEND|os.O_WRONLY, 0o600)
	big.WriteString("# " + strings.Repeat("x", 2<<20) + "\n")
	big.Close()

	if res := f.run(t); !res.Rebuilt || !res.ExportOK {
		t.Fatalf("без kc.yaml порог не действует: %+v", res)
	}
}

func TestRunZeroMaxSizeDisablesThreshold(t *testing.T) {
	f := newFixture(t)
	f.opts.MaxSizeMB = 0
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)
	f.source(t, "big.yaml", "big", "https://big")
	big, _ := os.OpenFile(filepath.Join(f.dir, "big.yaml"), os.O_APPEND|os.O_WRONLY, 0o600)
	big.WriteString("# " + strings.Repeat("x", 2<<20) + "\n")
	big.Close()

	if res := f.run(t); !res.Rebuilt {
		t.Fatal("при MaxSizeMB=0 порог выключен, пересборка ожидалась")
	}
}

func TestRunReportsLostForeignEntriesAndBacksUp(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	// Инструмент дописал запись прямо в kc.yaml.
	cfg := f.load(t)
	cfg.Clusters["mine-c"] = &clientcmdapi.Cluster{Server: "https://mine"}
	cfg.AuthInfos["mine-u"] = &clientcmdapi.AuthInfo{Token: "x"}
	cfg.Contexts["mine"] = &clientcmdapi.Context{Cluster: "mine-c", AuthInfo: "mine-u"}
	if err := clientcmd.WriteToFile(*cfg, f.output); err != nil {
		t.Fatal(err)
	}

	f.source(t, "prod.yaml", "prod", "https://prod")
	f.run(t)

	if !strings.Contains(f.log.String(), "mine") {
		t.Errorf("в stderr нет имени пропавшей записи: %q", f.log)
	}
	backups, _ := filepath.Glob(filepath.Join(f.state, "backups", "kc.yaml.backup.*"))
	if len(backups) != 1 {
		t.Fatalf("ожидался 1 бэкап, найдено %v", backups)
	}
	if _, ok := f.load(t).Contexts["mine"]; ok {
		t.Error("чужой контекст должен пропасть из пересобранного файла")
	}
	kept, err := clientcmd.LoadFromFile(backups[0])
	if err != nil || kept.Contexts["mine"] == nil {
		t.Errorf("бэкап должен содержать прежний файл с чужой записью: err=%v", err)
	}
}

func TestRunSameContextNameInTwoFiles(t *testing.T) {
	f := newFixture(t)
	f.source(t, "a.yaml", "prod", "https://one")
	f.source(t, "b.yaml", "prod", "https://two")

	f.run(t) // не должно читать stdin и спрашивать про конфликт
	got := contextNames(f.load(t))
	if len(got) != 2 || got[0] != "kc:prod:a.yaml" || got[1] != "kc:prod:b.yaml" {
		t.Fatalf("контексты: %v", got)
	}
}

func TestRunRebuildsWhenOutputDeletedOrCorrupt(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	if err := os.Remove(f.output); err != nil {
		t.Fatal(err)
	}
	if res := f.run(t); !res.Rebuilt || !res.ExportOK {
		t.Fatalf("после удаления kc.yaml: %+v", res)
	}

	// Повреждённый файл с изменившимися источниками собирается заново.
	if err := os.WriteFile(f.output, []byte("::: not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.source(t, "prod.yaml", "prod", "https://prod")
	res, err := Run(f.opts)
	if err != nil || !res.Rebuilt {
		t.Fatalf("повреждённый kc.yaml: res=%+v err=%v", res, err)
	}
	if got := contextNames(f.load(t)); len(got) != 2 {
		t.Errorf("контексты: %v", got)
	}
}

func TestRunEmptyDirGivesValidEmptyConfig(t *testing.T) {
	f := newFixture(t)
	res := f.run(t)
	if !res.ExportOK {
		t.Fatalf("пустой каталог должен давать валидный kc.yaml: %+v", res)
	}
	if got := contextNames(f.load(t)); len(got) != 0 {
		t.Errorf("контексты: %v", got)
	}
}

func TestRunBrokenSourceWarnsAndContinues(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	if err := os.WriteFile(filepath.Join(f.dir, "bad.yaml"), []byte("apiVersion: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.run(t)
	if !strings.Contains(f.log.String(), "bad.yaml") {
		t.Errorf("в stderr нет предупреждения про bad.yaml: %q", f.log)
	}
	if got := contextNames(f.load(t)); len(got) != 1 {
		t.Errorf("контексты: %v", got)
	}
}

func TestRunMissingDir(t *testing.T) {
	f := newFixture(t)
	f.opts.Dir = filepath.Join(f.root, "nope")
	f.opts.Output = filepath.Join(f.root, "out", "kc.yaml")

	res, err := Run(f.opts)
	if err == nil || res.ExportOK {
		t.Fatalf("без каталога и без kc.yaml ожидалась ошибка и ExportOK=false: res=%+v err=%v", res, err)
	}

	// Файл уже есть: ошибка остаётся, но export печатать можно.
	if err := os.MkdirAll(filepath.Dir(f.opts.Output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.opts.Output, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = Run(f.opts)
	if err == nil || !res.ExportOK {
		t.Fatalf("с готовым kc.yaml ожидалась ошибка и ExportOK=true: res=%+v err=%v", res, err)
	}
}

func TestRunRelativeDir(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	t.Chdir(f.root)
	f.opts.Dir = ".kube"
	f.opts.Output = filepath.Join(".kube", "kc.yaml")

	res := f.run(t)
	if res.Output != f.output {
		t.Errorf("Output = %q, ожидался абсолютный путь %q", res.Output, f.output)
	}
	if got := contextNames(f.load(t)); len(got) != 1 {
		t.Errorf("контексты: %v", got)
	}
}
