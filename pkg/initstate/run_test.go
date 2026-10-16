package initstate

import (
	"bytes"
	"fmt"
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

// paths возвращает файлы состояния и блокировки init для пары этой фикстуры.
func (f *fixture) paths(t *testing.T) (statePath, lockPath string) {
	t.Helper()
	dir, output, _, err := resolvePaths(f.opts.Dir, f.opts.Output)
	if err != nil {
		t.Fatal(err)
	}
	return statePaths(f.opts.StateDir, dir, output)
}

func (f *fixture) backups(t *testing.T) []string {
	t.Helper()
	b, _ := filepath.Glob(filepath.Join(f.state, "backups", "kc.yaml.backup.*"))
	return b
}

// edit загружает kc.yaml, даёт его поправить и записывает обратно — как это
// делают kubectl, kubens или aws eks update-kubeconfig.
func (f *fixture) edit(t *testing.T, fn func(cfg *clientcmdapi.Config)) {
	t.Helper()
	cfg := f.load(t)
	fn(cfg)
	if err := clientcmd.WriteToFile(*cfg, f.output); err != nil {
		t.Fatal(err)
	}
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

func TestRunLostForeignWarningLimitedToTen(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	// Инструмент дописал в kc.yaml 12 чужих записей (имя общее у кластера, пользователя и контекста).
	cfg := f.load(t)
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("foreign%02d", i)
		cfg.Clusters[name] = &clientcmdapi.Cluster{Server: "https://" + name}
		cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{Token: "x"}
		cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	if err := clientcmd.WriteToFile(*cfg, f.output); err != nil {
		t.Fatal(err)
	}

	f.source(t, "prod.yaml", "prod", "https://prod")
	f.run(t)

	msg := f.log.String()
	if got := strings.Count(msg, "foreign"); got != 10 {
		t.Errorf("в предупреждении должно быть ровно 10 имён, найдено %d: %q", got, msg)
	}
	if !strings.Contains(msg, "foreign10") || strings.Contains(msg, "foreign11") {
		t.Errorf("ожидались первые 10 имён по алфавиту: %q", msg)
	}
	if !strings.Contains(msg, "и ещё 2") {
		t.Errorf("в предупреждении нет числа оставшихся: %q", msg)
	}
	backups, _ := filepath.Glob(filepath.Join(f.state, "backups", "kc.yaml.backup.*"))
	if len(backups) != 1 {
		t.Fatalf("ожидался 1 бэкап, найдено %v", backups)
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
	// Нужен именно невалидный YAML: строку вроде "::: not yaml" client-go
	// читает без ошибки как пустой конфиг.
	if err := os.WriteFile(f.output, []byte("clusters: [{\n"), 0o600); err != nil {
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
	if b := f.backups(t); len(b) != 1 {
		t.Errorf("повреждённый kc.yaml должен попасть в бэкап ровно один раз, бэкапы: %v", b)
	}
}

func TestRunDeletedOutputIsNotBackedUp(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)
	if err := os.Remove(f.output); err != nil {
		t.Fatal(err)
	}
	f.source(t, "prod.yaml", "prod", "https://prod")
	f.run(t)
	if b := f.backups(t); len(b) != 0 {
		t.Errorf("удалённый kc.yaml сохранять нечего, бэкапы: %v", b)
	}
}

func TestRunBacksUpUnreadableOutput(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	// Ручная правка с опечаткой: файл больше не разбирается.
	data, err := os.ReadFile(f.output)
	if err != nil {
		t.Fatal(err)
	}
	broken := append(data, []byte("\nclusters: [{\n")...)
	if err := os.WriteFile(f.output, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	f.source(t, "prod.yaml", "prod", "https://prod")
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("ожидалась пересборка")
	}

	b := f.backups(t)
	if len(b) != 1 {
		t.Fatalf("ожидался ровно один бэкап, есть %v", b)
	}
	if got, _ := os.ReadFile(b[0]); !bytes.Equal(got, broken) {
		t.Errorf("в бэкапе не прежний битый файл:\n%s", got)
	}
	if !strings.Contains(f.log.String(), b[0]) {
		t.Errorf("в stderr нет пути бэкапа %s: %q", b[0], f.log)
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

func TestRunEmptyDirWarnsAboutZeroSources(t *testing.T) {
	f := newFixture(t)
	f.run(t)
	if !strings.Contains(f.log.String(), "не найдено ни одного файла-источника") {
		t.Errorf("в stderr нет предупреждения про пустой каталог: %q", f.log)
	}
}

func TestRunBacksUpOutputItDidNotBuild(t *testing.T) {
	f := newFixture(t)
	// kc.yaml собран не init (например, merge), состояния init для пары нет.
	if err := clientcmd.WriteToFile(*cfgWith("kc:prod:prod.yaml"), f.output); err != nil {
		t.Fatal(err)
	}
	f.run(t)

	if !strings.Contains(f.log.String(), "kc:prod:prod.yaml") {
		t.Errorf("в stderr нет имени пропавшей записи: %q", f.log)
	}
	if b := f.backups(t); len(b) != 1 {
		t.Fatalf("ожидался 1 бэкап, найдено %v", b)
	}
}

func TestRunBacksUpForeignEntryWithPrefix(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	// Запись с префиксом kc:, но дописанная в kc.yaml не init.
	const name = "kc:other:x.yaml"
	f.edit(t, func(cfg *clientcmdapi.Config) {
		cfg.Clusters[name] = &clientcmdapi.Cluster{Server: "https://other"}
		cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{Token: "x"}
		cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	})
	f.source(t, "prod.yaml", "prod", "https://prod")
	f.run(t)

	if !strings.Contains(f.log.String(), name) {
		t.Errorf("в stderr нет имени пропавшей записи: %q", f.log)
	}
	if b := f.backups(t); len(b) != 1 {
		t.Fatalf("ожидался 1 бэкап, найдено %v", b)
	}
}

// setSourceNamespace меняет namespace контекста ctx в источнике file.
func (f *fixture) setSourceNamespace(t *testing.T, file, ctx, ns string) {
	t.Helper()
	path := filepath.Join(f.dir, file)
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts[ctx].Namespace = ns
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
}

func TestRunSourceNamespaceChangeReachesOutput(t *testing.T) {
	f := newFixture(t)
	f.source(t, "a.yaml", "a", "https://a")
	f.setSourceNamespace(t, "a.yaml", "a", "old-ns")
	f.run(t)
	if got := f.load(t).Contexts["kc:a:a.yaml"].Namespace; got != "old-ns" {
		t.Fatalf("namespace после первой сборки = %q", got)
	}

	f.setSourceNamespace(t, "a.yaml", "a", "new-ns")
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("ожидалась пересборка")
	}
	if got := f.load(t).Contexts["kc:a:a.yaml"].Namespace; got != "new-ns" {
		t.Errorf("правка namespace в источнике не дошла до kc.yaml: %q", got)
	}
}

func TestRunUserNamespaceSurvivesSourceChange(t *testing.T) {
	f := newFixture(t)
	f.source(t, "a.yaml", "a", "https://a")
	f.setSourceNamespace(t, "a.yaml", "a", "src-ns")
	f.run(t)

	// kubens прямо в kc.yaml, затем меняется другой источник.
	f.edit(t, func(cfg *clientcmdapi.Config) { cfg.Contexts["kc:a:a.yaml"].Namespace = "mine" })
	f.source(t, "b.yaml", "b", "https://b")
	f.run(t)

	if got := f.load(t).Contexts["kc:a:a.yaml"].Namespace; got != "mine" {
		t.Errorf("namespace, выбранный пользователем, = %q, ожидалось mine", got)
	}
}

func TestRunWithoutStateCarriesNamespace(t *testing.T) {
	f := newFixture(t)
	f.source(t, "a.yaml", "a", "https://a")
	f.setSourceNamespace(t, "a.yaml", "a", "src-ns")
	f.run(t)
	f.edit(t, func(cfg *clientcmdapi.Config) { cfg.Contexts["kc:a:a.yaml"].Namespace = "mine" })

	statePath, _ := f.paths(t)
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if got := f.load(t).Contexts["kc:a:a.yaml"].Namespace; got != "mine" {
		t.Errorf("без состояния namespace переносится из прежнего файла, got %q", got)
	}
}

func TestRunStateIsPerPair(t *testing.T) {
	f1 := newFixture(t)
	f2 := newFixture(t)
	// Общий каталог состояния у двух пар.
	f2.opts.StateDir = f1.state
	f2.state = f1.state
	f1.source(t, "a.yaml", "a", "https://a")
	f2.source(t, "b.yaml", "b", "https://b")
	f1.run(t)
	f2.run(t)

	s1, _ := f1.paths(t)
	s2, _ := f2.paths(t)
	if s1 == s2 {
		t.Fatalf("у разных пар один файл состояния %s", s1)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{s1, s2} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		for _, f := range []*fixture{f1, f2} {
			if res := f.run(t); res.Rebuilt {
				t.Fatalf("повтор %d: пересборка без изменений источников", i)
			}
		}
	}
	for _, p := range []string{s1, s2} {
		if info, err := os.Stat(p); err != nil || !info.ModTime().Equal(old) {
			t.Errorf("файл состояния %s переписан без изменений: err=%v", p, err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(f1.state, "init-state-*.json"))
	if len(files) != 2 {
		t.Errorf("ожидалось 2 файла состояния, найдено %v", files)
	}
}

func TestRunWaitsForKubectlLock(t *testing.T) {
	f := newFixture(t)
	f.opts.KubeconfigLockTimeout = 5 * time.Second
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)

	f.source(t, "prod.yaml", "prod", "https://prod")
	lock := f.output + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Remove(lock)
	}()
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("после снятия lock ожидалась пересборка")
	}
	if got := contextNames(f.load(t)); len(got) != 2 {
		t.Errorf("контексты: %v", got)
	}
}

func TestRunBusyKubectlLockLeavesOutputAndState(t *testing.T) {
	f := newFixture(t)
	f.opts.KubeconfigLockTimeout = 50 * time.Millisecond
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)
	statePath, _ := f.paths(t)
	before, _ := os.ReadFile(f.output)
	stateBefore, _ := os.ReadFile(statePath)

	f.source(t, "prod.yaml", "prod", "https://prod")
	lock := f.output + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Run(f.opts)
	if err == nil || res.Rebuilt || !res.ExportOK {
		t.Fatalf("занятый lock: ожидалась ошибка без пересборки, res=%+v err=%v", res, err)
	}
	if !strings.Contains(f.log.String(), lock) || !strings.Contains(f.log.String(), "занят") {
		t.Errorf("в stderr нет сообщения, что lock-файл занят: %q", f.log)
	}
	if after, _ := os.ReadFile(f.output); !bytes.Equal(before, after) {
		t.Error("kc.yaml изменён при занятом lock")
	}
	if stateAfter, _ := os.ReadFile(statePath); !bytes.Equal(stateBefore, stateAfter) {
		t.Error("состояние сохранено, хотя сборка не записана")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("чужой lock-файл удалён: %v", err)
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if res := f.run(t); !res.Rebuilt {
		t.Fatal("после снятия lock следующий запуск должен пересобрать")
	}
	if got := contextNames(f.load(t)); len(got) != 2 {
		t.Errorf("контексты: %v", got)
	}
}

func TestRunRemovesKubectlLock(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)
	if _, err := os.Stat(f.output + ".lock"); !os.IsNotExist(err) {
		t.Errorf("после Run lock-файл должен быть удалён, stat: %v", err)
	}
}

func TestRunOutputInMissingDir(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.output = filepath.Join(f.root, "new", "sub", "kc.yaml")
	f.opts.Output = f.output

	res := f.run(t)
	if !res.Rebuilt || !res.ExportOK {
		t.Fatalf("сборка в новый каталог: %+v, log: %s", res, f.log)
	}
	if got := contextNames(f.load(t)); len(got) != 1 {
		t.Errorf("контексты: %v", got)
	}
}

func TestRunKubectlLockErrorIsNotReportedAsBusy(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	// Каталог output — обычный файл: lock-файл не создать, но он и не занят.
	plain := filepath.Join(f.root, "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.output = filepath.Join(plain, "kc.yaml")
	f.opts.Output = f.output

	if _, err := Run(f.opts); err == nil {
		t.Fatal("ожидалась ошибка: каталог output — файл")
	}
	if strings.Contains(f.log.String(), "занят") {
		t.Errorf("ошибка создания lock-файла выдана за «занят»: %q", f.log)
	}
}

// setBeforeWriteHook ставит beforeWriteHook на время теста; fn получает номер
// вызова, начиная с 1. Возвращает счётчик вызовов.
func setBeforeWriteHook(t *testing.T, fn func(call int)) *int {
	t.Helper()
	calls := 0
	beforeWriteHook = func() {
		calls++
		fn(calls)
	}
	t.Cleanup(func() { beforeWriteHook = nil })
	return &calls
}

// touch ставит kc.yaml заметно другой mtime, чтобы перепроверка точно увидела
// чужую запись даже на файловой системе с грубым mtime.
func (f *fixture) touch(t *testing.T, hours int) {
	t.Helper()
	ts := time.Now().Add(time.Duration(hours) * time.Hour)
	if err := os.Chtimes(f.output, ts, ts); err != nil {
		t.Fatal(err)
	}
}

// twoSourcesBuilt собирает kc.yaml из a и b с current-context a и добавляет
// источник c, чтобы следующий Run пересобирал файл.
func twoSourcesBuilt(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.source(t, "a.yaml", "a", "https://a")
	f.source(t, "b.yaml", "b", "https://b")
	f.run(t)
	f.edit(t, func(cfg *clientcmdapi.Config) { cfg.CurrentContext = "kc:a:a.yaml" })
	f.source(t, "c.yaml", "c", "https://c")
	return f
}

func TestRunRecheckPicksUpConcurrentContextSwitch(t *testing.T) {
	f := twoSourcesBuilt(t)
	calls := setBeforeWriteHook(t, func(call int) {
		if call == 1 { // kubectx b в соседнем терминале
			f.edit(t, func(cfg *clientcmdapi.Config) { cfg.CurrentContext = "kc:b:b.yaml" })
			f.touch(t, 1)
		}
	})

	if res := f.run(t); !res.Rebuilt {
		t.Fatal("ожидалась пересборка")
	}
	got := f.load(t)
	if got.CurrentContext != "kc:b:b.yaml" {
		t.Errorf("current-context = %q, переключение kubectx потеряно", got.CurrentContext)
	}
	if len(got.Contexts) != 3 {
		t.Errorf("контексты: %v", contextNames(got))
	}
	if *calls != 2 {
		t.Errorf("попыток %d, ожидалось 2", *calls)
	}
}

func TestRunEmptyOutputIsTreatedAsWriteInProgress(t *testing.T) {
	f := twoSourcesBuilt(t)
	f.edit(t, func(cfg *clientcmdapi.Config) { cfg.CurrentContext = "kc:b:b.yaml" })
	saved, err := os.ReadFile(f.output)
	if err != nil {
		t.Fatal(err)
	}
	// kubectx сделал truncate и ещё не дописал файл...
	setBeforeWriteHook(t, func(call int) {
		if call == 1 {
			if err := os.WriteFile(f.output, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	// ...и дописал, пока init ждал.
	slept := 0
	retrySleep = func(time.Duration) {
		slept++
		if slept == 1 {
			if err := os.WriteFile(f.output, saved, 0o600); err != nil {
				t.Fatal(err)
			}
			f.touch(t, 1)
		}
	}
	t.Cleanup(func() { retrySleep = time.Sleep })

	f.run(t)
	if got := f.load(t); got.CurrentContext != "kc:b:b.yaml" {
		t.Errorf("current-context = %q: пустой файл принят за пустую сессию", got.CurrentContext)
	}
	if slept == 0 {
		t.Error("на пустом файле init должен был подождать")
	}
}

func TestRunRetryStartsFromSourceBuild(t *testing.T) {
	for _, tc := range []struct{ name, second, want string }{
		{"namespace сменили", "b", "b"},
		{"namespace сбросили", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := twoSourcesBuilt(t)
			f.edit(t, func(cfg *clientcmdapi.Config) { cfg.Contexts["kc:a:a.yaml"].Namespace = "a" })
			setBeforeWriteHook(t, func(call int) {
				if call == 1 { // kubens в соседнем терминале
					f.edit(t, func(cfg *clientcmdapi.Config) { cfg.Contexts["kc:a:a.yaml"].Namespace = tc.second })
					f.touch(t, 1)
				}
			})

			f.run(t)
			if got := f.load(t).Contexts["kc:a:a.yaml"].Namespace; got != tc.want {
				t.Errorf("namespace = %q, ожидалось %q: повтор унаследовал первую попытку", got, tc.want)
			}
		})
	}
}

func TestRunRecheckGivesUpAfterThreeAttempts(t *testing.T) {
	f := twoSourcesBuilt(t)
	calls := setBeforeWriteHook(t, func(call int) {
		f.edit(t, func(cfg *clientcmdapi.Config) {
			if call%2 == 1 {
				cfg.CurrentContext = "kc:b:b.yaml"
			} else {
				cfg.CurrentContext = "kc:a:a.yaml"
			}
		})
		f.touch(t, call)
	})

	if res := f.run(t); !res.Rebuilt {
		t.Fatal("после исчерпания попыток файл всё равно должен быть записан")
	}
	if *calls != 3 {
		t.Errorf("попыток %d, ожидалось 3", *calls)
	}
	if got := contextNames(f.load(t)); len(got) != 3 {
		t.Errorf("контексты: %v", got)
	}
}

// addForeign дописывает в cfg кластер, пользователя и контекст с именем name —
// запись, которую в kc.yaml внёс не init.
func addForeign(cfg *clientcmdapi.Config, name string) {
	cfg.Clusters[name] = &clientcmdapi.Cluster{Server: "https://" + name}
	cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{Token: "t-" + name}
	cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
}

// lostBackupPath возвращает путь бэкапа из предупреждения о пропавших чужих записях.
func (f *fixture) lostBackupPath(t *testing.T) string {
	t.Helper()
	const marker = "прежний файл сохранён: "
	var found []string
	for _, line := range strings.Split(f.log.String(), "\n") {
		if !strings.Contains(line, "нет записей, которых нет среди источников") {
			continue
		}
		i := strings.Index(line, marker)
		if i < 0 {
			t.Fatalf("в предупреждении нет пути бэкапа: %q", line)
		}
		found = append(found, line[i+len(marker):])
	}
	if len(found) != 1 {
		t.Fatalf("ожидалось одно предупреждение о пропавших записях, есть %d: %q", len(found), f.log)
	}
	return found[0]
}

func TestRunLostBackupMatchesLastAttempt(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)
	f.edit(t, func(cfg *clientcmdapi.Config) { addForeign(cfg, "f1") })
	f.source(t, "prod.yaml", "prod", "https://prod")

	// Между попытками aws eks update-kubeconfig дописал в kc.yaml ещё одну запись.
	setBeforeWriteHook(t, func(call int) {
		if call == 1 {
			f.edit(t, func(cfg *clientcmdapi.Config) { addForeign(cfg, "f2") })
			f.touch(t, 1)
		}
	})
	f.run(t)

	if msg := f.log.String(); !strings.Contains(msg, "f1") || !strings.Contains(msg, "f2") {
		t.Errorf("в предупреждении должны быть f1 и f2: %q", msg)
	}
	kept, err := clientcmd.LoadFromFile(f.lostBackupPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if kept.Contexts["f1"] == nil || kept.Contexts["f2"] == nil {
		t.Errorf("в бэкапе из предупреждения должны быть f1 и f2, есть %v", contextNames(kept))
	}
}

func TestRunUnreadableBackupDoesNotOverwriteLostBackup(t *testing.T) {
	f := newFixture(t)
	f.source(t, "dev.yaml", "dev", "https://dev")
	f.run(t)
	f.edit(t, func(cfg *clientcmdapi.Config) { addForeign(cfg, "f1") })
	withF1, err := os.ReadFile(f.output)
	if err != nil {
		t.Fatal(err)
	}
	f.source(t, "prod.yaml", "prod", "https://prod")

	// Попытка 2 читает файл посреди чужой неатомарной записи, попытка 3 — уже дописанным.
	broken := []byte("clusters: [{\n")
	setBeforeWriteHook(t, func(call int) {
		data := withF1
		if call == 1 {
			data = broken
		}
		if err := os.WriteFile(f.output, data, 0o600); err != nil {
			t.Fatal(err)
		}
		f.touch(t, call)
	})
	f.run(t)

	b := f.backups(t)
	if len(b) != 3 {
		t.Fatalf("ожидалось 3 бэкапа (по одному на версию файла), есть %v", b)
	}
	var brokenCopies, f1Copies int
	for _, p := range b {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case bytes.Equal(data, broken):
			brokenCopies++
		case bytes.Equal(data, withF1):
			f1Copies++
		}
	}
	if brokenCopies != 1 || f1Copies != 2 {
		t.Errorf("бэкапы перезаписаны: битых копий %d, копий с f1 %d", brokenCopies, f1Copies)
	}
	kept, err := clientcmd.LoadFromFile(f.lostBackupPath(t))
	if err != nil || kept.Contexts["f1"] == nil {
		t.Errorf("бэкап из предупреждения должен содержать f1: err=%v", err)
	}
}

func TestBackupFileNeverOverwrites(t *testing.T) {
	now := time.Date(2026, 10, 16, 13, 19, 18, 0, time.Local)
	backupNow = func() time.Time { return now }
	t.Cleanup(func() { backupNow = time.Now })

	dir := filepath.Join(t.TempDir(), "backups")
	first, err := backupFile("kc.yaml", []byte("one"), dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := backupFile("kc.yaml", []byte("two"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "kc.yaml.backup.20261016-131918"); first != want {
		t.Errorf("первый бэкап %s, ожидался %s", first, want)
	}
	if second != first+"-2" {
		t.Errorf("второй бэкап %s, ожидался %s-2", second, first)
	}
	for path, want := range map[string]string{first: "one", second: "two"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s: %q, err=%v; ожидалось %q", path, got, err, want)
		}
		if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("%s: права %v, ожидались 0600", path, info.Mode().Perm())
		}
	}
}
