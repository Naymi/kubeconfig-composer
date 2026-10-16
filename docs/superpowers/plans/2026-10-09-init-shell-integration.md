# `kubeconfig-composer init` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Команда `kubeconfig-composer init`, которую `.bashrc`/`.zshrc` вызывает через `eval`: собирает `~/.kube/kc.yaml` из источников только когда они изменились и печатает `export KUBECONFIG=...`.

**Architecture:** Тонкая команда `cmd/init.go` вызывает `pkg/initstate.Run`. Пакет считает отпечаток источников через `stat`, сравнивает его с сохранённым, проверяет порог размера и при необходимости пересобирает файл через существующий `Composer` (без вопросов, `cache/` пропускается), перенося `current-context` и `namespace` из прежнего `kc.yaml`. Запись атомарная, параллельные запуски разводит `flock`.

**Tech Stack:** Go 1.24.1, cobra, `k8s.io/client-go/tools/clientcmd`, pterm; тесты в Docker (`golang:1.24.1-bookworm`).

**Spec:** `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`

## Global Constraints

- Тесты и проверки запускаются только в контейнерах и не пишут на хост: `task test:docker -- <аргументы go test>` (исходники монтируются read-only) и `task test:container`. Настоящие `~/.kube` и `~/.kubeconfig-composer` не читаются и не пишутся ни тестами, ни ручными проверками.
- Все пути в тестах — только `t.TempDir()`; `HOME` в тестах не используется (`StateDir` передаётся явно).
- Docker-образы фиксируются точным тегом: `golang:1.24.1-bookworm`.
- stdout команды `init` содержит только строку `export KUBECONFIG='...'`; всё остальное в stderr (stdout уходит в `eval`).
- Сообщения для пользователя и комментарии в коде на русском, как в остальном коде; commit-сообщения на английском, без строки об AI-соавторстве.
- Код форматируется `gofmt`: перед каждым коммитом выполнить `task fmt` (правит только исходники проекта); новые файлы собираются под windows (`GOOS=windows go vet ./...`).
- Права файлов `0600` для `kc.yaml` и `init-state.json`, каталоги `0755`.
- Состояние хранится в `~/.kubeconfig-composer/` (`init-state.json`, `init.lock`, `backups/`).
- Каждый коммит запускает pre-commit `task check` (fmt, vet, `go test ./...`, shellcheck, `test:install`) локально; он пишет только в кэш Go и во временный каталог `.tmp/` проекта. Не обходить его через `--no-verify`.

## Review Focus

Не покрыто спекой, но может задеть пользователя (каждая строка получает тест в задаче-владельце):

1. Корень `--dir` сам скрытый (`~/.kube`), `--dir` относительный, путь с пробелом или `'` в `export` — не должно ничего пропасть и не должен сломаться `eval` (Task 3, 5, 7).
2. `kc.yaml` удалён вручную или не собирается: следующий запуск пересобирает его, пустой каталог даёт валидный пустой `kc.yaml` (Task 7).
3. Одинаковые имена контекстов в разных файлах: собираются без вопросов, stdin не читается (Task 7, e2e).
4. Оборванная предыдущая запись: временный файл `.kc.yaml.tmp-*` остаётся, но не попадает в отпечаток и не считается источником (Task 2, 3).
5. Файл подменён с тем же размером и mtime (`cp -p`) не обнаруживается; это известное ограничение, обход — `--refresh` (Task 10, README).

---

### Task 0: Ветка и запуск тестов в контейнере

**Files:**
- Modify: `Taskfile.yml`

**Interfaces:**
- Produces: `task test:docker -- <аргументы go test>` — `go test` в контейнере, исходники смонтированы read-only, на хост ничего не пишется; `task setup:docker`. Остальные задачи плана используют `test:docker`.

- [ ] **Step 1: Создать ветку**

Run: `git switch -c feat/init-command`
Expected: `Switched to a new branch 'feat/init-command'`

- [ ] **Step 2: Добавить задачи в `Taskfile.yml`**

В блок `vars` добавить:

```yaml
  GO_IMAGE: golang:1.24.1-bookworm
```

В задачу `setup` добавить третьей строкой `- task: setup:docker`, и после `setup:hooks` новую задачу:

```yaml
  setup:docker:
    desc: Скачать образ для контейнерных тестов (пропускается, если Docker не установлен)
    cmds:
      - |
        if ! command -v docker >/dev/null 2>&1; then
          echo "docker не установлен, пропускаю (нужен для task test:docker и task test:container)"
          exit 0
        fi
        docker pull {{.GO_IMAGE}}
```

После `test:install` добавить:

```yaml
  test:docker:
    desc: 'go test в контейнере, исходники read-only, на хост ничего не пишется: task test:docker -- ./pkg/initstate/... -run Name -v'
    cmds:
      - >-
        docker run --rm
        -v "{{.ROOT_DIR}}":/src:ro
        -v kubeconfig-composer-gomod:/go/pkg/mod
        -w /src
        -e GOCACHE=/tmp/gocache
        -e GOFLAGS=-buildvcs=false
        {{.GO_IMAGE}} go test {{.CLI_ARGS}}
```

- [ ] **Step 3: Проверить на существующих тестах**

Run: `task setup:docker && task test:docker -- ./...`
Expected: образ скачан, `ok` для `pkg/cleaner`, `pkg/composer`.

- [ ] **Step 4: Commit**

```bash
git add Taskfile.yml
git commit -m "build: add containerized go test task"
```

---

### Task 1: Сканер пропускает служебные каталоги, порядок источников детерминирован

**Files:**
- Modify: `pkg/scanner/scanner.go`
- Create: `pkg/scanner/scanner_test.go`
- Modify: `pkg/composer/composer.go:205-290` (`ScanDirectory`)
- Modify: `pkg/composer/composer_test.go` (добавить тесты в конец файла)
- Modify: `cmd/list.go:36-52` (обход)

**Interfaces:**
- Produces: `scanner.IsSkippedDir(name string) bool` — true для `cache`, `http-cache` и имён, начинающихся с `.`. Корень обхода вызывающий код не проверяет. Используется в Task 3.

- [ ] **Step 1: Написать падающие тесты сканера**

Создать `pkg/scanner/scanner_test.go`:

```go
package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func writeKubeconfig(t *testing.T, path string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["c"] = &clientcmdapi.Cluster{Server: "https://example.invalid"}
	cfg.AuthInfos["u"] = &clientcmdapi.AuthInfo{Token: "t"}
	cfg.Contexts["ctx"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
}

func TestIsSkippedDir(t *testing.T) {
	cases := map[string]bool{
		"cache":      true,
		"http-cache": true,
		".git":       true,
		".hidden":    true,
		"prod":       false,
		"caches":     false,
		"kubens":     false,
	}
	for name, want := range cases {
		if got := IsSkippedDir(name); got != want {
			t.Errorf("IsSkippedDir(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFindKubeconfigFilesSkipsServiceDirs(t *testing.T) {
	// Корень скрытый, как настоящий ~/.kube: его пропускать нельзя.
	root := filepath.Join(t.TempDir(), ".kube")
	writeKubeconfig(t, filepath.Join(root, "config"))
	writeKubeconfig(t, filepath.Join(root, "nested", "ok.yaml"))
	writeKubeconfig(t, filepath.Join(root, "cache", "http", "x.yaml"))
	writeKubeconfig(t, filepath.Join(root, "http-cache", "y.yaml"))
	writeKubeconfig(t, filepath.Join(root, ".git", "z.yaml"))

	files, err := FindKubeconfigFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var rel []string
	for _, f := range files {
		r, _ := filepath.Rel(root, f)
		rel = append(rel, r)
	}
	sort.Strings(rel)
	want := []string{"config", filepath.Join("nested", "ok.yaml")}
	if len(rel) != len(want) || rel[0] != want[0] || rel[1] != want[1] {
		t.Fatalf("got %v, want %v", rel, want)
	}
}
```

- [ ] **Step 2: Запустить тесты, убедиться что падают**

Run: `task test:docker -- ./pkg/scanner/... -run 'TestIsSkippedDir|TestFindKubeconfigFilesSkipsServiceDirs' -v`
Expected: FAIL, `undefined: IsSkippedDir`


- [ ] **Step 3: Реализовать `IsSkippedDir` и применить в `FindKubeconfigFiles`**

В `pkg/scanner/scanner.go` добавить после импортов:

```go
// skippedDirs — служебные каталоги kubectl в ~/.kube: kubeconfig'ов там нет, а
// файлов тысячи (кэш discovery и HTTP-кэш, у автора ~10 тысяч файлов и 1,5 ГБ).
var skippedDirs = map[string]bool{"cache": true, "http-cache": true}

// IsSkippedDir сообщает, нужно ли пропустить каталог name при поиске
// kubeconfig: служебные каталоги kubectl и скрытые каталоги. Корень обхода
// вызывающий код не проверяет — он сам может быть скрытым (~/.kube).
func IsSkippedDir(name string) bool {
	return skippedDirs[name] || strings.HasPrefix(name, ".")
}
```

В `FindKubeconfigFiles` заменить блок `if info.IsDir() { return nil }` на:

```go
		if info.IsDir() {
			if path != dir && IsSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
```

- [ ] **Step 4: Написать падающие тесты для `ScanDirectory`**

В конец `pkg/composer/composer_test.go` добавить (импорты `os`, `path/filepath`, `clientcmd` там уже есть; добавить `sort`, если его нет):

```go
func writeCfgFile(t *testing.T, path, ctx, server string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*makeConfig(ctx, ctx+"-c", ctx+"-u", server, "t"), path); err != nil {
		t.Fatal(err)
	}
}

func TestScanDirectorySkipsServiceDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kube")
	writeCfgFile(t, filepath.Join(root, "config"), "main", "https://a")
	writeCfgFile(t, filepath.Join(root, "cache", "http", "x.yaml"), "cached", "https://b")
	writeCfgFile(t, filepath.Join(root, "http-cache", "y.yaml"), "cached2", "https://c")

	c := New(true)
	if err := c.ScanDirectory(root); err != nil {
		t.Fatal(err)
	}
	if got := len(c.GetLoadedFiles()); got != 1 {
		t.Fatalf("ожидался 1 файл, найдено %d", got)
	}
}

func TestScanDirectoryOrderIsDeterministic(t *testing.T) {
	root := t.TempDir()
	names := []string{"e.yaml", "b.yaml", "d.yaml", "a.yaml", "c.yaml"}
	for _, n := range names {
		writeCfgFile(t, filepath.Join(root, n), n[:1], "https://"+n)
	}
	var first []string
	for i := 0; i < 20; i++ {
		c := New(true)
		if err := c.ScanDirectory(root); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range c.GetLoadedFiles() {
			got = append(got, filepath.Base(f.GetFullPath()))
		}
		if !sort.StringsAreSorted(got) {
			t.Fatalf("порядок не отсортирован: %v", got)
		}
		if i == 0 {
			first = got
		} else if len(got) != len(first) {
			t.Fatalf("число файлов меняется: %v vs %v", got, first)
		}
	}
}
```

- [ ] **Step 5: Запустить, убедиться что падают**

Run: `task test:docker -- ./pkg/composer/... -run 'TestScanDirectory' -v`
Expected: `TestScanDirectorySkipsServiceDirs` FAIL (найдено 3), `TestScanDirectoryOrderIsDeterministic` FAIL (порядок не отсортирован) хотя бы на одной итерации; если пройдёт случайно, это нормально — шаг 7 всё равно нужен.

- [ ] **Step 6: Реализовать пропуск каталогов и сортировку в `ScanDirectory`**

В `pkg/composer/composer.go` в `ScanDirectory` заменить блок `if info.IsDir() { return nil }` в `filepath.Walk` на:

```go
		if info.IsDir() {
			if path != dir && scanner.IsSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
```

Перед циклом «Собираем результаты» запомнить `start := len(c.configs)`, а после цикла (перед `close(resultChan)`) добавить:

```go
	// Горутины завершаются в произвольном порядке; сортируем добавленные этим
	// сканированием конфиги по пути, чтобы результат merge (в том числе
	// current-context «первого файла») не зависел от планировщика. Ранее
	// загруженная база (start) остаётся первой.
	added := c.configs[start:]
	sort.Slice(added, func(i, j int) bool { return added[i].fullPath < added[j].fullPath })
```

(`sort` уже импортирован в `composer.go`.)

- [ ] **Step 7: Применить пропуск в `cmd/list.go`**

В `runList` заменить `if info.IsDir() { return nil }` на:

```go
		if info.IsDir() {
			if path != listDir && scanner.IsSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
```

- [ ] **Step 8: Прогнать тесты**

Run: `task test:docker -- ./...`
Expected: PASS для всех пакетов.

- [ ] **Step 9: Commit**

```bash
git add pkg/scanner/scanner.go pkg/scanner/scanner_test.go pkg/composer/composer.go pkg/composer/composer_test.go cmd/list.go
git commit -m "feat: skip kubectl cache dirs and sort scanned sources"
```

---

### Task 2: Атомарная запись файла

**Files:**
- Modify: `pkg/cleaner/cleaner.go` (добавить функцию)
- Create: `pkg/cleaner/atomic_test.go`
- Modify: `pkg/composer/composer.go` (методы `Result`, `WriteOutputAtomic`)
- Modify: `pkg/composer/composer_test.go`

**Interfaces:**
- Produces:
  - `cleaner.WriteFileAtomic(path string, data []byte, perm os.FileMode) error` — пишет во временный файл `.<имя>.tmp-*` рядом, затем `rename`; если `path` — символическая ссылка, пишет в её цель; создаёт каталог.
  - `(*Composer).Result() *clientcmdapi.Config` — итоговый конфиг после `Merge`/`Dedupe` (тот же указатель, что внутри Composer, правки видны `OutputUnchanged`/`WriteOutputAtomic`).
  - `(*Composer).WriteOutputAtomic(path string) error`.

- [ ] **Step 1: Написать падающие тесты**

Создать `pkg/cleaner/atomic_test.go`:

```go
package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomicReplacesContentAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "kc.yaml")

	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two" {
		t.Fatalf("содержимое %q, ошибка %v", data, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права %v, ожидались 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("остался временный файл %s", e.Name())
		}
	}
}

func TestWriteFileAtomicKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	link := filepath.Join(dir, "link.yaml")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink недоступен: %v", err)
	}

	if err := WriteFileAtomic(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Error("ссылка заменена обычным файлом")
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Errorf("цель содержит %q, ожидалось new", data)
	}
}
```

В конец `pkg/composer/composer_test.go` добавить:

```go
func TestWriteOutputAtomicWritesMergedConfig(t *testing.T) {
	c := New(true)
	c.configs = []configWithSource{
		{config: makeConfig("dev", "dev-c", "dev-u", "https://dev", "t"), source: "dev", fullPath: "/d/dev.yaml"},
	}
	if err := c.Merge(); err != nil {
		t.Fatal(err)
	}
	if c.Result() != c.mergedConfig {
		t.Fatal("Result должен возвращать внутренний итоговый конфиг")
	}

	out := filepath.Join(t.TempDir(), "nested", "kc.yaml")
	if err := c.WriteOutputAtomic(out); err != nil {
		t.Fatal(err)
	}
	same, err := c.OutputUnchanged(out)
	if err != nil || !same {
		t.Fatalf("записанный файл должен совпадать с результатом: same=%v err=%v", same, err)
	}
}
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `task test:docker -- ./pkg/cleaner/... ./pkg/composer/... -run 'Atomic' -v`
Expected: FAIL, `undefined: WriteFileAtomic`, `c.Result undefined`.

- [ ] **Step 3: Реализовать**

В `pkg/cleaner/cleaner.go` добавить импорт `path/filepath` и функцию:

```go
// WriteFileAtomic записывает data в path так, что читатель (например, kubectl
// в другом терминале) видит либо старое, либо новое содержимое целиком:
// сначала временный файл рядом, потом rename. Если path — символическая
// ссылка, обновляется её цель, а не сама ссылка. Недостающие каталоги создаются.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать каталог %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("не удалось создать временный файл: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("не удалось записать временный файл: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("не удалось выставить права: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("не удалось закрыть временный файл: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("не удалось заменить %s: %w", path, err)
	}
	return nil
}
```

В `pkg/composer/composer.go` рядом с `WriteOutput` добавить:

```go
// Result возвращает итоговый конфиг после Merge (и Dedupe). Возвращается тот же
// указатель, что хранит Composer: правки вызывающего видны в OutputUnchanged и
// WriteOutputAtomic. После Dedupe указатель меняется — вызывать Result заново.
func (c *Composer) Result() *clientcmdapi.Config {
	return c.mergedConfig
}

// WriteOutputAtomic — как WriteOutput, но через временный файл и rename
// (см. cleaner.WriteFileAtomic).
func (c *Composer) WriteOutputAtomic(path string) error {
	data, err := clientcmd.Write(*c.mergedConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}
	return cleaner.WriteFileAtomic(path, data, 0o600)
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `task test:docker -- ./pkg/cleaner/... ./pkg/composer/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cleaner/cleaner.go pkg/cleaner/atomic_test.go pkg/composer/composer.go pkg/composer/composer_test.go
git commit -m "feat: add atomic file write and Composer.Result/WriteOutputAtomic"
```

---

### Task 3: Отпечаток источников и файл состояния

**Files:**
- Create: `pkg/initstate/fingerprint.go`
- Create: `pkg/initstate/state.go`
- Create: `pkg/initstate/fingerprint_test.go`
- Create: `pkg/initstate/state_test.go`

**Interfaces:**
- Consumes: `scanner.IsSkippedDir(name string) bool` (Task 1), `cleaner.WriteFileAtomic` (Task 2).
- Produces:
  - `type Snapshot struct { Hash string; TotalSize int64; Files int }`
  - `Scan(dir, output string, salt ...string) (*Snapshot, error)`
  - `type State struct { Fingerprint, Version, Updated string }` (json-теги `fingerprint`, `version`, `updated`)
  - `LoadState(path string) (State, bool)` — `false`, если файла нет или он повреждён
  - `SaveState(path string, s State) error`

- [ ] **Step 1: Написать падающие тесты отпечатка**

Создать `pkg/initstate/fingerprint_test.go`:

```go
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
```

Создать `pkg/initstate/state_test.go`:

```go
package initstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "init-state.json")
	if _, ok := LoadState(path); ok {
		t.Fatal("несуществующий файл не должен читаться как состояние")
	}
	want := State{Fingerprint: "abc", Version: "v1", Updated: "2026-10-09T00:00:00Z"}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadState(path)
	if !ok || got != want {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestLoadStateCorruptIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadState(path); ok {
		t.Fatal("повреждённый файл должен считаться отсутствующим состоянием")
	}
}
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `task test:docker -- ./pkg/initstate/... -v`
Expected: FAIL, `undefined: Scan`, `undefined: LoadState`.

- [ ] **Step 3: Реализовать `fingerprint.go`**

```go
package initstate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/naymi/kubeconfig-composer/pkg/scanner"
)

// Snapshot — результат обхода каталога источников: отпечаток для сравнения с
// прошлой сборкой и суммарный размер для проверки порога.
type Snapshot struct {
	Hash      string
	TotalSize int64
	Files     int
}

// Scan обходит dir через stat, не читая содержимое файлов. Пропускаются
// скрытые файлы (в том числе временные .kc.yaml.tmp-*), сам output и каталоги
// из scanner.IsSkippedDir; корень обхода не проверяется — ~/.kube скрытый.
// Символические ссылки на файлы учитываются по цели (os.Stat). salt
// подмешивается в хеш: версия бинарника и параметры сборки, чтобы их смена
// тоже вызывала пересборку.
func Scan(dir, output string, salt ...string) (*Snapshot, error) {
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}

	var lines []string
	snap := &Snapshot{}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // файл или подкаталог исчез или недоступен — пропускаем
		}
		if d.IsDir() {
			if path != dir && scanner.IsSkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if abs, aerr := filepath.Abs(path); aerr == nil && abs == absOutput {
			return nil
		}
		info, serr := os.Stat(path)
		if serr != nil || !info.Mode().IsRegular() {
			return nil
		}
		lines = append(lines, fmt.Sprintf("%s\t%d\t%d", path, info.ModTime().UnixNano(), info.Size()))
		snap.TotalSize += info.Size()
		snap.Files++
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(lines)
	h := sha256.New()
	for _, s := range salt {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	h.Write([]byte(strings.Join(lines, "\n")))
	snap.Hash = hex.EncodeToString(h.Sum(nil))
	return snap, nil
}
```

- [ ] **Step 4: Реализовать `state.go`**

```go
package initstate

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/naymi/kubeconfig-composer/pkg/cleaner"
)

// State — то, что init помнит между запусками: отпечаток источников, по
// которым собран kc.yaml. Version и Updated нужны только человеку, который
// открыл файл.
type State struct {
	Fingerprint string `json:"fingerprint"`
	Version     string `json:"version"`
	Updated     string `json:"updated"`
}

// LoadState читает состояние. Отсутствующий или повреждённый файл возвращает
// false: init тогда просто пересоберёт kc.yaml.
func LoadState(path string) (State, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil || s.Fingerprint == "" {
		return State{}, false
	}
	return s, true
}

// SaveState атомарно записывает состояние, создавая каталог при необходимости.
func SaveState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось сериализовать состояние: %w", err)
	}
	return cleaner.WriteFileAtomic(path, data, 0o600)
}
```

- [ ] **Step 5: Прогнать тесты**

Run: `task test:docker -- ./pkg/initstate/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/initstate/fingerprint.go pkg/initstate/state.go pkg/initstate/fingerprint_test.go pkg/initstate/state_test.go
git commit -m "feat: add source fingerprint and init state storage"
```

---

### Task 4: Межпроцессная блокировка

**Files:**
- Create: `pkg/initstate/lock.go`
- Create: `pkg/initstate/lock_unix.go`
- Create: `pkg/initstate/lock_windows.go`
- Create: `pkg/initstate/lock_unix_test.go`

**Interfaces:**
- Produces:
  - `type Lock` с методом `Unlock()`
  - `TryLock(path string) (*Lock, error)` — `(nil, nil)`, если блокировку держит другой процесс
  - `WaitLock(path string, timeout time.Duration) (*Lock, error)` — опрашивает `TryLock` каждые 50 мс, ошибка по таймауту

- [ ] **Step 1: Написать падающий тест**

Создать `pkg/initstate/lock_unix_test.go`:

```go
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
```

- [ ] **Step 2: Запустить, убедиться что падает**

Run: `task test:docker -- ./pkg/initstate/... -run 'Lock' -v`
Expected: FAIL, `undefined: TryLock`.

- [ ] **Step 3: Реализовать**

`pkg/initstate/lock_unix.go`:

```go
//go:build !windows

package initstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock — эксклюзивная блокировка файла (flock). Освобождается Unlock или
// автоматически при завершении процесса.
type Lock struct{ f *os.File }

// TryLock берёт блокировку без ожидания. Если её держит другой процесс,
// возвращает (nil, nil).
func TryLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("не удалось создать каталог блокировки: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл блокировки: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	return &Lock{f: f}, nil
}

// Unlock снимает блокировку.
func (l *Lock) Unlock() {
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
```

`pkg/initstate/lock_windows.go`:

```go
//go:build windows

package initstate

// Lock на windows пустой: init рассчитан на bash/zsh, а релизы под windows
// только должны собираться.
type Lock struct{}

// TryLock всегда успешен.
func TryLock(path string) (*Lock, error) { return &Lock{}, nil }

// Unlock ничего не делает.
func (l *Lock) Unlock() {}
```

`pkg/initstate/lock.go`:

```go
package initstate

import (
	"fmt"
	"time"
)

// WaitLock ждёт блокировку, опрашивая TryLock каждые 50 мс, но не дольше
// timeout — зависший процесс не должен подвесить старт терминала навсегда.
func WaitLock(path string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, err := TryLock(path)
		if err != nil {
			return nil, err
		}
		if l != nil {
			return l, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("не удалось получить блокировку %s за %s", path, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `task test:docker -- ./pkg/initstate/... -run 'Lock' -v`
Expected: PASS.

- [ ] **Step 5: Проверить сборку под windows**

Run: `docker run --rm -v "$PWD":/src:ro -v kubeconfig-composer-gomod:/go/pkg/mod -w /src -e GOCACHE=/tmp/gocache -e GOFLAGS=-buildvcs=false -e GOOS=windows -e GOARCH=amd64 golang:1.24.1-bookworm go vet ./pkg/initstate/...`
Expected: без вывода, код 0.

- [ ] **Step 6: Commit**

```bash
git add pkg/initstate/lock.go pkg/initstate/lock_unix.go pkg/initstate/lock_windows.go pkg/initstate/lock_unix_test.go
git commit -m "feat: add cross-process lock for init rebuilds"
```

---

### Task 5: Перехват stdout и экранирование `export`

**Files:**
- Create: `pkg/initstate/quiet.go`
- Create: `pkg/initstate/export.go`
- Create: `pkg/initstate/quiet_test.go`
- Create: `pkg/initstate/export_test.go`

**Interfaces:**
- Produces:
  - `CaptureStdout(fn func() error) (warnings []string, err error)` — на время `fn` подменяет `os.Stdout` и вывод pterm, возвращает строки, содержащие `⚠` (без ANSI-кодов). Не потокобезопасна: меняет глобальный `os.Stdout`.
  - `ShellExport(path string) string` — `export KUBECONFIG='<path>'` с экранированием `'`.

- [ ] **Step 1: Написать падающие тесты**

`pkg/initstate/export_test.go`:

```go
package initstate

import "testing"

func TestShellExportQuoting(t *testing.T) {
	cases := map[string]string{
		"/home/u/.kube/kc.yaml":    `export KUBECONFIG='/home/u/.kube/kc.yaml'`,
		"/home/my user/kc.yaml":    `export KUBECONFIG='/home/my user/kc.yaml'`,
		"/home/o'brien/kc.yaml":    `export KUBECONFIG='/home/o'\''brien/kc.yaml'`,
		"/home/$(rm -rf x)/kc.yml": `export KUBECONFIG='/home/$(rm -rf x)/kc.yml'`,
	}
	for in, want := range cases {
		if got := ShellExport(in); got != want {
			t.Errorf("ShellExport(%q) = %s, want %s", in, got, want)
		}
	}
}
```

`pkg/initstate/quiet_test.go`:

```go
package initstate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/pterm/pterm"
)

func TestCaptureStdoutSwallowsOutputAndKeepsWarnings(t *testing.T) {
	// Подменяем настоящий stdout на свою трубу и проверяем, что в неё ничего не попало.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	sentinel := errors.New("boom")
	warnings, gotErr := CaptureStdout(func() error {
		fmt.Println("шум через fmt")
		fmt.Printf("→ Автоматически принято: \033[33mx\033[0m\n")
		pterm.FgYellow.Println("⚠ предупреждение pterm")
		return sentinel
	})

	if os.Stdout != w {
		t.Error("os.Stdout не восстановлен")
	}
	w.Close()
	leaked, _ := io.ReadAll(r)
	if len(leaked) != 0 {
		t.Errorf("в настоящий stdout просочилось: %q", leaked)
	}
	if gotErr != sentinel {
		t.Errorf("ошибка fn потеряна: %v", gotErr)
	}
	want := []string{"⚠ предупреждение pterm"}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `task test:docker -- ./pkg/initstate/... -run 'Capture|ShellExport' -v`
Expected: FAIL, `undefined: CaptureStdout`, `undefined: ShellExport`.

- [ ] **Step 3: Реализовать**

`pkg/initstate/export.go`:

```go
package initstate

import "strings"

// ShellExport возвращает строку для eval в bash/zsh. Путь берётся в одинарные
// кавычки, внутренние одинарные кавычки экранируются как '\'' — иначе путь с
// пробелом или кавычкой сломал бы eval при старте терминала.
func ShellExport(path string) string {
	return "export KUBECONFIG='" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
```

`pkg/initstate/quiet.go`:

```go
package initstate

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/pterm/pterm"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// CaptureStdout запускает fn, перехватывая всё, что она печатает в os.Stdout
// (fmt.Print*, tablewriter) и через pterm. Composer печатает в stdout, а stdout
// команды init уходит в eval, так что любой посторонний вывод выполнился бы как
// shell-код. Строки с «⚠» (без ANSI-кодов) возвращаются как предупреждения,
// остальное отбрасывается. Функция меняет глобальный os.Stdout и поэтому не
// потокобезопасна: вызывать из одной горутины.
func CaptureStdout(fn func() error) (warnings []string, err error) {
	r, w, perr := os.Pipe()
	if perr != nil {
		return nil, fmt.Errorf("не удалось перехватить вывод: %w", perr)
	}

	orig := os.Stdout
	os.Stdout = w
	pterm.SetDefaultOutput(w)

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, r)
		close(done)
	}()

	defer func() {
		w.Close()
		<-done
		r.Close()
		os.Stdout = orig
		pterm.SetDefaultOutput(orig)
		for _, line := range strings.Split(buf.String(), "\n") {
			line = strings.TrimSpace(ansiRe.ReplaceAllString(line, ""))
			if strings.Contains(line, "⚠") {
				warnings = append(warnings, line)
			}
		}
	}()

	return nil, fn()
}
```

Примечание для исполнителя: возвращаемые значения заполняются в `defer` через именованные результаты; `return nil, fn()` сначала присваивает `err`, затем `defer` дописывает `warnings`.

- [ ] **Step 4: Прогнать тесты**

Run: `task test:docker -- ./pkg/initstate/... -run 'Capture|ShellExport' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/initstate/quiet.go pkg/initstate/export.go pkg/initstate/quiet_test.go pkg/initstate/export_test.go
git commit -m "feat: add stdout capture and safe shell export for init"
```

---

### Task 6: Перенос состояния сессии

**Files:**
- Create: `pkg/initstate/carry.go`
- Create: `pkg/initstate/carry_test.go`

**Interfaces:**
- Produces:
  - `type Session struct { CurrentContext string; Namespaces map[string]string; Names map[string]bool }`
  - `ReadSession(path string) (*Session, error)` — `(nil, nil)`, если файла нет
  - `(*Session) ApplyCurrentContext(cfg *clientcmdapi.Config)` — ставит прежний `current-context`, только если такой контекст есть в `cfg`
  - `(*Session) ApplyNamespaces(cfg *clientcmdapi.Config)` — переносит непустые `namespace` на контексты, которые есть в `cfg`
  - `(*Session) LostForeign(cfg *clientcmdapi.Config, prefix string) []string` — отсортированные имена кластеров/пользователей/контекстов, которые были в старом файле, пропали в `cfg` и не начинаются с `<prefix>:` (при пустом `prefix` — любые пропавшие)

- [ ] **Step 1: Написать падающие тесты**

`pkg/initstate/carry_test.go`:

```go
package initstate

import (
	"path/filepath"
	"reflect"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func cfgWith(ctxs ...string) *clientcmdapi.Config {
	cfg := clientcmdapi.NewConfig()
	for _, n := range ctxs {
		cfg.Clusters[n+"-c"] = &clientcmdapi.Cluster{Server: "https://" + n}
		cfg.AuthInfos[n+"-u"] = &clientcmdapi.AuthInfo{Token: n}
		cfg.Contexts[n] = &clientcmdapi.Context{Cluster: n + "-c", AuthInfo: n + "-u"}
	}
	return cfg
}

func TestReadSessionMissingFile(t *testing.T) {
	s, err := ReadSession(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || s != nil {
		t.Fatalf("s=%v err=%v, ожидалось (nil, nil)", s, err)
	}
}

func TestReadSessionReadsStateAndNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kc.yaml")
	old := cfgWith("a", "b")
	old.CurrentContext = "b"
	old.Contexts["a"].Namespace = "team-a"
	if err := clientcmd.WriteToFile(*old, path); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSession(path)
	if err != nil || s == nil {
		t.Fatalf("s=%v err=%v", s, err)
	}
	if s.CurrentContext != "b" || s.Namespaces["a"] != "team-a" {
		t.Errorf("состояние прочитано неверно: %+v", s)
	}
	for _, n := range []string{"a", "b", "a-c", "b-u"} {
		if !s.Names[n] {
			t.Errorf("имя %q не попало в Names", n)
		}
	}
}

func TestApplyCurrentContextOnlyWhenContextExists(t *testing.T) {
	s := &Session{CurrentContext: "b"}

	withB := cfgWith("a", "b")
	withB.CurrentContext = "a"
	s.ApplyCurrentContext(withB)
	if withB.CurrentContext != "b" {
		t.Errorf("current-context = %q, ожидалось b", withB.CurrentContext)
	}

	noB := cfgWith("a")
	noB.CurrentContext = "a"
	s.ApplyCurrentContext(noB)
	if noB.CurrentContext != "a" {
		t.Errorf("current-context = %q, должен остаться a: прежнего контекста больше нет", noB.CurrentContext)
	}
}

func TestApplyNamespacesSkipsEmptyAndMissing(t *testing.T) {
	s := &Session{Namespaces: map[string]string{"a": "team-a", "b": "", "gone": "x"}}
	cfg := cfgWith("a", "b")
	cfg.Contexts["b"].Namespace = "from-source"

	s.ApplyNamespaces(cfg)

	if cfg.Contexts["a"].Namespace != "team-a" {
		t.Errorf("namespace a = %q", cfg.Contexts["a"].Namespace)
	}
	if cfg.Contexts["b"].Namespace != "from-source" {
		t.Errorf("пустой прежний namespace не должен затирать значение из источника: %q", cfg.Contexts["b"].Namespace)
	}
}

func TestLostForeign(t *testing.T) {
	s := &Session{Names: map[string]bool{
		"kc:gone:old.yaml": true, // источник удалён: молчим
		"kc:kept:a.yaml":   true,
		"mine":             true, // чужая запись пропала
		"zeta":             true,
	}}
	cfg := clientcmdapi.NewConfig()
	cfg.Contexts["kc:kept:a.yaml"] = &clientcmdapi.Context{}

	got := s.LostForeign(cfg, "kc")
	if want := []string{"mine", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("с префиксом: got %v, want %v", got, want)
	}

	got = s.LostForeign(cfg, "")
	if want := []string{"kc:gone:old.yaml", "mine", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("без префикса: got %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Запустить, убедиться что падает**

Run: `task test:docker -- ./pkg/initstate/... -run 'Session|Apply|LostForeign' -v`
Expected: FAIL, `undefined: ReadSession`.

- [ ] **Step 3: Реализовать `carry.go`**

```go
package initstate

import (
	"errors"
	"io/fs"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Session — состояние прежнего kc.yaml, которое должно пережить пересборку:
// kubectl, kubectx и kubens пишут его прямо в этот файл.
type Session struct {
	CurrentContext string
	Namespaces     map[string]string // имя контекста -> namespace
	Names          map[string]bool   // имена кластеров, пользователей и контекстов
}

// ReadSession читает прежний kc.yaml. Если файла нет, возвращает (nil, nil).
func ReadSession(path string) (*Session, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	s := &Session{
		CurrentContext: cfg.CurrentContext,
		Namespaces:     map[string]string{},
		Names:          map[string]bool{},
	}
	for name := range cfg.Clusters {
		s.Names[name] = true
	}
	for name := range cfg.AuthInfos {
		s.Names[name] = true
	}
	for name, ctx := range cfg.Contexts {
		s.Names[name] = true
		s.Namespaces[name] = ctx.Namespace
	}
	return s, nil
}

// ApplyCurrentContext возвращает прежний current-context, если такой контекст
// есть в cfg. Вызывать до Dedupe: он не схлопывает контекст из current-context.
func (s *Session) ApplyCurrentContext(cfg *clientcmdapi.Config) {
	if s.CurrentContext == "" {
		return
	}
	if _, ok := cfg.Contexts[s.CurrentContext]; ok {
		cfg.CurrentContext = s.CurrentContext
	}
}

// ApplyNamespaces переносит непустые прежние namespace на контексты, которые
// есть в cfg. Пустой прежний namespace значение из источника не затирает.
func (s *Session) ApplyNamespaces(cfg *clientcmdapi.Config) {
	for name, ns := range s.Namespaces {
		if ns == "" {
			continue
		}
		if ctx, ok := cfg.Contexts[name]; ok {
			ctx.Namespace = ns
		}
	}
}

// LostForeign возвращает отсортированные имена, которые были в прежнем файле,
// пропали в cfg и не начинаются с "<prefix>:" — то есть записи, добавленные не
// нами (например, `aws eks update-kubeconfig`). Пропавшие записи с префиксом —
// это удалённые источники, о них не сообщаем. При пустом prefix отличить свои
// записи нельзя, поэтому возвращаются все пропавшие.
func (s *Session) LostForeign(cfg *clientcmdapi.Config, prefix string) []string {
	var lost []string
	for name := range s.Names {
		if prefix != "" && strings.HasPrefix(name, prefix+":") {
			continue
		}
		_, inCluster := cfg.Clusters[name]
		_, inUser := cfg.AuthInfos[name]
		_, inContext := cfg.Contexts[name]
		if !inCluster && !inUser && !inContext {
			lost = append(lost, name)
		}
	}
	sort.Strings(lost)
	return lost
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `task test:docker -- ./pkg/initstate/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/initstate/carry.go pkg/initstate/carry_test.go
git commit -m "feat: carry current-context and namespaces across init rebuilds"
```

---

### Task 7: Алгоритм `Run`

**Files:**
- Create: `pkg/initstate/run.go`
- Create: `pkg/initstate/run_test.go`
- Create: `pkg/initstate/run_unix_test.go`

**Interfaces:**
- Consumes: `Scan`, `LoadState`, `SaveState` (Task 3), `TryLock`, `WaitLock` (Task 4), `CaptureStdout` (Task 5), `ReadSession` и методы `Session` (Task 6), `composer.New`, `SetBaseDir`, `SetNamePrefix`, `ScanDirectory`, `Merge`, `Result`, `Dedupe`, `OutputUnchanged`, `WriteOutputAtomic` (Task 1–2).
- Produces:
  - `type Options struct { Dir, Output, Prefix string; MaxSizeMB int; Refresh bool; Version, StateDir string; Log io.Writer; LockTimeout time.Duration }` (`MaxSizeMB <= 0` отключает порог; `LockTimeout == 0` значит 30 секунд)
  - `type Result struct { Output string; ExportOK bool; Rebuilt bool }`
  - `Run(opts Options) (Result, error)` — `Result.ExportOK` истинно, если `Output` существует, даже при ошибке.

- [ ] **Step 1: Написать падающие тесты**

`pkg/initstate/run_test.go`:

```go
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
```

`pkg/initstate/run_unix_test.go`:

```go
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
```

- [ ] **Step 2: Запустить, убедиться что падают**

Run: `task test:docker -- ./pkg/initstate/... -run 'TestRun' -v`
Expected: FAIL, `undefined: Run`, `undefined: Options`.

- [ ] **Step 3: Реализовать `run.go`**

```go
package initstate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/naymi/kubeconfig-composer/pkg/composer"
)

// Options — параметры Run.
type Options struct {
	Dir         string        // каталог источников (--dir)
	Output      string        // собираемый файл (--output)
	Prefix      string        // префикс имён (--prefix)
	MaxSizeMB   int           // порог суммарного размера источников; <= 0 — без порога
	Refresh     bool          // пересобрать сейчас, порог не учитывается
	Version     string        // версия бинарника, входит в отпечаток
	StateDir    string        // ~/.kubeconfig-composer
	Log         io.Writer     // предупреждения; обычно os.Stderr
	LockTimeout time.Duration // сколько ждать блокировку при первой сборке; 0 — 30 с
}

// Result — итог Run.
type Result struct {
	Output   string // абсолютный путь kc.yaml
	ExportOK bool   // kc.yaml существует, можно печатать export (в том числе при ошибке)
	Rebuilt  bool   // kc.yaml был записан заново
}

// Run выполняет алгоритм init: отпечаток, порог, блокировка, пересборка.
// Подробности и порядок шагов — в спеке docs/superpowers/specs/.
func Run(opts Options) (res Result, err error) {
	logf := func(format string, a ...any) {
		if opts.Log != nil {
			fmt.Fprintf(opts.Log, format+"\n", a...)
		}
	}

	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return res, err
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return res, err
	}
	res.Output = output
	defer func() { res.ExportOK = fileExists(output) }()

	snap, err := Scan(dir, output, opts.Version, opts.Prefix, dir, output)
	if err != nil {
		return res, fmt.Errorf("не удалось обойти %s: %w", dir, err)
	}

	statePath := filepath.Join(opts.StateDir, "init-state.json")
	upToDate := func() bool {
		st, ok := LoadState(statePath)
		return ok && st.Fingerprint == snap.Hash && fileExists(output)
	}
	if !opts.Refresh && upToDate() {
		return res, nil
	}

	if !opts.Refresh && opts.MaxSizeMB > 0 && fileExists(output) && snap.TotalSize > int64(opts.MaxSizeMB)<<20 {
		logf("⚠ kc.yaml устарел: источники весят %.1f МБ при пороге %d МБ; запустите `kubeconfig-composer init --refresh`",
			float64(snap.TotalSize)/(1<<20), opts.MaxSizeMB)
		return res, nil
	}

	lockPath := filepath.Join(opts.StateDir, "init.lock")
	lock, err := TryLock(lockPath)
	if err != nil {
		return res, err
	}
	if lock == nil {
		if fileExists(output) {
			return res, nil // собирает другой терминал; kc.yaml заменяется атомарно
		}
		timeout := opts.LockTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		if lock, err = WaitLock(lockPath, timeout); err != nil {
			return res, err
		}
	}
	defer lock.Unlock()

	// Пока ждали блокировку, другой процесс мог собрать то же самое.
	if !opts.Refresh && upToDate() {
		return res, nil
	}

	wrote, err := rebuild(opts, dir, output, logf)
	if err != nil {
		return res, err
	}
	res.Rebuilt = wrote

	st := State{Fingerprint: snap.Hash, Version: opts.Version, Updated: time.Now().UTC().Format(time.RFC3339)}
	if err := SaveState(statePath, st); err != nil {
		return res, err
	}
	return res, nil
}

// rebuild собирает kc.yaml из источников и записывает его, если содержимое
// изменилось. Возвращает true, если файл был записан.
func rebuild(opts Options, dir, output string, logf func(string, ...any)) (bool, error) {
	sess, serr := ReadSession(output)
	if serr != nil {
		logf("⚠ не удалось прочитать прежний %s, состояние сессии не сохранено: %v", output, serr)
		sess = nil
	}

	c := composer.New(true)
	c.SetBaseDir(dir)
	c.SetNamePrefix(opts.Prefix)
	warnings, err := CaptureStdout(func() error {
		if err := c.ScanDirectory(dir, output); err != nil {
			return err
		}
		return c.Merge()
	})
	for _, w := range warnings {
		logf("%s", w)
	}
	if err != nil {
		return false, fmt.Errorf("не удалось собрать конфиг: %w", err)
	}

	// current-context ставим до Dedupe: он не схлопывает контекст из current-context.
	if sess != nil {
		sess.ApplyCurrentContext(c.Result())
	}
	c.Dedupe()
	if sess != nil {
		sess.ApplyNamespaces(c.Result())

		if lost := sess.LostForeign(c.Result(), opts.Prefix); len(lost) > 0 {
			backup, berr := backupFile(output, filepath.Join(opts.StateDir, "backups"))
			if berr != nil {
				logf("⚠ в новом %s нет записей, которых нет среди источников: %v; сохранить прежний файл не удалось: %v", output, lost, berr)
			} else {
				logf("⚠ в новом %s нет записей, которых нет среди источников: %v; прежний файл сохранён: %s", output, lost, backup)
			}
		}
	}

	if fileExists(output) {
		if same, err := c.OutputUnchanged(output); err == nil && same {
			return false, nil
		}
	}
	if err := c.WriteOutputAtomic(output); err != nil {
		return false, err
	}
	return true, nil
}

// backupFile копирует src в backupDir под именем, которое понимает revert:
// "<имя>.backup.<timestamp>".
func backupFile(src, backupDir string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(backupDir, fmt.Sprintf("%s.backup.%s", filepath.Base(src), time.Now().Format("20060102-150405")))
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

Примечание: в `Run` список пропавших чужих записей печатается как `%v` среза строк (`[mine mine-c mine-u]`), этого достаточно для сообщения.

- [ ] **Step 4: Прогнать тесты**

Run: `task test:docker -- ./pkg/initstate/... -v`
Expected: PASS для всех тестов `TestRun*` и ранее написанных.

- [ ] **Step 5: Commit**

```bash
git add pkg/initstate/run.go pkg/initstate/run_test.go pkg/initstate/run_unix_test.go
git commit -m "feat: add init algorithm (fingerprint, threshold, lock, rebuild)"
```

---

### Task 8: Команда `init`

**Files:**
- Create: `cmd/init.go`

**Interfaces:**
- Consumes: `initstate.Run`, `initstate.Options`, `initstate.Result`, `initstate.ShellExport` (Task 5, 7); `version` из `cmd/root.go`.

Отдельных unit-тестов нет: команда тонкая, её проверяют e2e-тесты в Task 9.

- [ ] **Step 1: Создать `cmd/init.go`**

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/naymi/kubeconfig-composer/pkg/initstate"
	"github.com/spf13/cobra"
)

var (
	initDir     string
	initOutput  string
	initPrefix  string
	initMaxSize int
	initRefresh bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Собрать kubeconfig при старте shell и вывести export KUBECONFIG",
	Long: `Предназначена для .bashrc/.zshrc: собирает ~/.kube/kc.yaml из всех
kubeconfig файлов в ~/.kube и печатает команду export KUBECONFIG=... для eval.

Файлы не парсятся, пока не изменились: по умолчанию init только сверяет
отпечаток (путь, mtime, размер) источников с сохранённым. Если источники
изменились, kc.yaml пересобирается без вопросов. Если они суммарно больше
--max-size МБ, пересборка при старте shell пропускается с предупреждением в
stderr; запустите init --refresh вручную.

kc.yaml — производный файл: current-context и namespace в нём сохраняются при
пересборке, остальные правки перезаписываются. Новые кластеры добавляйте в
отдельные файлы-источники, например:
  alias eks-add='KUBECONFIG=~/.kube/aws.yaml aws eks update-kubeconfig'`,
	Example: `  # В ~/.bashrc или ~/.zshrc
  eval "$(kubeconfig-composer init)"

  # Принудительно пересобрать, порог размера не учитывается
  kubeconfig-composer init --refresh

  # Другая директория источников и другой выходной файл
  eval "$(kubeconfig-composer init --dir ~/work/kube --output ~/work/kube/kc.yaml)"`,
	// Ошибку печатает Execute() один раз; usage в eval-сценарии только мешает.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)

	home := os.Getenv("HOME")
	initCmd.Flags().StringVarP(&initDir, "dir", "d", filepath.Join(home, ".kube"), "Директория с kubeconfig файлами")
	initCmd.Flags().StringVarP(&initOutput, "output", "o", filepath.Join(home, ".kube", "kc.yaml"), "Собираемый файл")
	initCmd.Flags().StringVar(&initPrefix, "prefix", "kc", `Префикс имён, как у merge (пустая строка отключает)`)
	initCmd.Flags().IntVar(&initMaxSize, "max-size", 5, "Порог суммарного размера источников в МБ, выше которого пересборка при старте пропускается (0 — без порога)")
	initCmd.Flags().BoolVar(&initRefresh, "refresh", false, "Пересобрать сейчас, порог размера не учитывается")
}

func runInit(cmd *cobra.Command, args []string) error {
	res, err := initstate.Run(initstate.Options{
		Dir:       initDir,
		Output:    initOutput,
		Prefix:    initPrefix,
		MaxSizeMB: initMaxSize,
		Refresh:   initRefresh,
		Version:   version,
		StateDir:  filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer"),
		Log:       os.Stderr,
	})

	if res.ExportOK {
		// Оболочка не должна ломаться: ошибку показываем, но export отдаём.
		if err != nil {
			fmt.Fprintf(os.Stderr, "kubeconfig-composer init: %v\n", err)
		}
		fmt.Println(initstate.ShellExport(res.Output))
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("kubeconfig-composer init: %s не создан", res.Output)
}
```

- [ ] **Step 2: Проверить сборку и vet в контейнере**

Run: `task test:docker -- ./... && docker run --rm -v "$PWD":/src:ro -v kubeconfig-composer-gomod:/go/pkg/mod -w /src -e GOCACHE=/tmp/gocache -e GOFLAGS=-buildvcs=false golang:1.24.1-bookworm go vet ./...`
Expected: PASS, vet без вывода.

- [ ] **Step 3: Commit**

```bash
git add cmd/init.go
git commit -m "feat: add init command"
```

---

### Task 9: Контейнерные e2e-тесты и задачи Taskfile

**Files:**
- Create: `scripts/Dockerfile.test`
- Create: `.dockerignore`
- Create: `e2e/doc.go`
- Create: `e2e/init_test.go`
- Create: `scripts/init_shell_test.sh`
- Modify: `Taskfile.yml`

**Interfaces:**
- Consumes: готовый бинарник `kubeconfig-composer` (переменная `KC_BIN`).
- Produces: `task test:container`.

- [ ] **Step 1: Добавить задачу `test:container` в `Taskfile.yml`**

В блок `vars` добавить:

```yaml
  TEST_IMAGE: kubeconfig-composer-test:local
```

После `test:docker` добавить:

```yaml
  test:container:
    desc: Полный прогон в контейнере - gofmt, vet (linux и windows), unit-тесты, затем e2e init в bash и zsh
    cmds:
      - docker build --progress=plain -f scripts/Dockerfile.test --build-arg GO_IMAGE={{.GO_IMAGE}} -t {{.TEST_IMAGE}} .
      - docker run --rm {{.TEST_IMAGE}}
```

- [ ] **Step 2: Создать `.dockerignore`**

```
.git
.github
.idea
.serena
.claude
.grepai
.task
.tmp
.mcp.json
kubeconfig-composer
*.test
coverage.out
merged-kubeconfig.yaml
```

- [ ] **Step 3: Создать `scripts/Dockerfile.test`**

```dockerfile
ARG GO_IMAGE=golang:1.24.1-bookworm
FROM ${GO_IMAGE}

RUN apt-get update \
    && apt-get install -y --no-install-recommends zsh procps \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Сборочные проверки: форматирование, vet под linux и windows, unit-тесты.
RUN test -z "$(gofmt -l .)" \
    && go vet ./... \
    && GOOS=windows GOARCH=amd64 go vet ./... \
    && go test ./...

RUN go build -o /usr/local/bin/kubeconfig-composer .

ENV KC_BIN=/usr/local/bin/kubeconfig-composer
CMD ["sh", "-c", "go test -tags e2e -count=1 -v ./e2e/... && sh scripts/init_shell_test.sh"]
```

- [ ] **Step 4: Создать `e2e/doc.go` и `e2e/init_test.go`**

`e2e/doc.go`:

```go
// Package e2e содержит e2e-тесты бинарника kubeconfig-composer (build tag
// e2e). Запускаются в контейнере: task test:container. Бинарник берётся из KC_BIN,
// HOME каждого теста — временный каталог.
package e2e
```

`e2e/init_test.go`:

```go
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
	for i := 0; i < 5000; i++ {
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
		t.Errorf("запуск без изменений занял %s при 5000 файлов в cache/", d)
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
```

- [ ] **Step 5: Создать `scripts/init_shell_test.sh`**

```sh
#!/bin/sh
# Проверка строки из .bashrc/.zshrc в настоящих bash и zsh. Запускается в
# контейнере (task test:container): kubeconfig-composer должен быть в PATH.
set -eu

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export HOME="$WORK/home user" # пробел в пути: export должен выдержать eval
mkdir -p "$HOME/.kube"

cat > "$HOME/.kube/dev.yaml" <<'EOF'
apiVersion: v1
kind: Config
clusters:
- name: dev-c
  cluster:
    server: https://dev.invalid
users:
- name: dev-u
  user:
    token: t
contexts:
- name: dev
  context:
    cluster: dev-c
    user: dev-u
current-context: dev
EOF

FAILED=0
check() { # описание, ожидаемое, фактическое
	if [ "$2" = "$3" ]; then
		echo "ok   $1"
	else
		echo "FAIL $1: ожидалось '$2', получено '$3'"
		FAILED=1
	fi
}

for sh_bin in bash zsh; do
	got="$($sh_bin -c 'eval "$(kubeconfig-composer init)"; printf %s "$KUBECONFIG"')"
	check "$sh_bin выставляет KUBECONFIG" "$HOME/.kube/kc.yaml" "$got"
done

got="$(KUBECONFIG="$HOME/.kube/kc.yaml" kubeconfig-composer list --dir "$HOME/.kube" 2>&1 | grep -c 'kc.yaml' || true)"
check "list видит kc.yaml как обычный файл, шелл не сломан" "1" "$got"

# Сбой init не должен ломать запуск оболочки: eval пустого stdout, дальше всё работает.
for sh_bin in bash zsh; do
	got="$(HOME="$WORK/empty-home" $sh_bin -c 'eval "$(kubeconfig-composer init --dir /nonexistent --output /tmp/none/kc.yaml 2>/dev/null)"; echo alive')"
	check "$sh_bin живёт после сбоя init" "alive" "$got"
done

exit "$FAILED"
```

Примечание: проверка `list` в середине скрипта — быстрая проверка, что `kc.yaml` лежит там, где ожидается; если `list` печатает путь иначе, исполнитель заменяет её на `test -s "$HOME/.kube/kc.yaml"` и сохраняет проверку bash/zsh и проверку сбоя.

- [ ] **Step 6: Прогнать полный контейнерный набор**

Run: `task test:container`
Expected: сборка образа проходит (gofmt, vet, unit-тесты), затем `--- PASS` для всех `TestInit*`, строки `ok   ...` из shell-скрипта, код выхода 0.

Если падает тест, сначала найти причину по выводу (в контейнере), а не ослаблять проверку.

- [ ] **Step 7: Shellcheck**

Run: `task lint:sh`
Expected: без замечаний (или `shellcheck не установлен, пропускаю`). Если `lint:sh` не охватывает новый скрипт, он уже проходит по маске `scripts/*.sh`.

- [ ] **Step 8: Commit**

```bash
git add Taskfile.yml .dockerignore scripts/Dockerfile.test scripts/init_shell_test.sh e2e/doc.go e2e/init_test.go
git commit -m "test: add container e2e tests for init and docker test tasks"
```

---

### Task 10: Документация, правка спеки, итоговая проверка

**Files:**
- Modify: `README.md` (таблица команд и новый раздел `init`)
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`

- [ ] **Step 1: README**

В таблицу «Команды» добавить строку после `merge`:

```
| `init` | Собирает `~/.kube/kc.yaml` при старте shell и печатает `export KUBECONFIG` |
```

После раздела `merge` добавить раздел:

````markdown
### init: сборка при старте shell

Добавьте в `~/.bashrc` или `~/.zshrc`:

```bash
eval "$(kubeconfig-composer init)"
```

При каждом открытии терминала `init` сверяет отпечаток файлов в `~/.kube` (путь, mtime, размер) с прошлым. Ничего не изменилось: он за миллисекунды печатает `export KUBECONFIG=~/.kube/kc.yaml`. Что-то изменилось: пересобирает `kc.yaml` без вопросов. Исходные файлы не меняются и не перемещаются.

```bash
# Принудительно пересобрать
kubeconfig-composer init --refresh

# Порог размера источников в МБ (по умолчанию 5; 0 отключает)
kubeconfig-composer init --max-size 10
```

Если источники изменились и весят больше порога, при старте shell пересборка пропускается: в stderr появляется предупреждение, `KUBECONFIG` указывает на прежний `kc.yaml`. Запустите `kubeconfig-composer init --refresh`. Полная сборка занимает около 0,1 с на мегабайт источников. Если `kc.yaml` ещё нет, порог не действует.

`kc.yaml` — производный файл. `current-context` и `namespace` в нём (их меняют `kubectl config use-context`, `kubectx`, `kubens`) сохраняются при пересборке. Остальные правки `kc.yaml` пропадут: если при пересборке исчезают записи, которых нет в источниках, `init` пишет об этом в stderr и кладёт прежний файл в `~/.kubeconfig-composer/backups/` (восстановить: `kubeconfig-composer revert --output ~/.kube/kc.yaml`).

Инструменты, которые добавляют кластеры, запускайте с отдельным файлом-источником, чтобы запись пережила пересборку:

```bash
alias eks-add='KUBECONFIG=~/.kube/aws.yaml aws eks update-kubeconfig'
```

Ограничение: подмена файла с тем же размером и mtime (например, `cp -p`) не замечается; в этом случае запустите `init --refresh`.
````

- [ ] **Step 2: AGENTS.md**

В список команд (`Command Structure`) добавить:

```
- `init.go` - Для `.bashrc`/`.zshrc` (`eval "$(kubeconfig-composer init)"`): собирает `~/.kube/kc.yaml` из источников, только если изменился отпечаток (путь+mtime+размер); печатает в stdout одну строку `export KUBECONFIG=...` (всё остальное в stderr — stdout уходит в `eval`). Логика в `pkg/initstate`
```

В «Core Packages» добавить:

```
#### `pkg/initstate`
Логика `init`:
- `Run()` (`run.go`) - отпечаток → порог `--max-size` → блокировка → пересборка. Не печатает в stdout; `Result.ExportOK` истинно, если `kc.yaml` существует (даже при ошибке)
- `Scan()` (`fingerprint.go`) - обход через `stat` без чтения файлов; пропускает `scanner.IsSkippedDir`, скрытые файлы и сам output
- `ReadSession()`/`Session` (`carry.go`) - `current-context` и `namespace` прежнего `kc.yaml` переносятся на пересобранный; `LostForeign()` находит пропавшие записи без префикса (предупреждение + бэкап)
- `TryLock()`/`WaitLock()` (`lock*.go`) - `flock`; на windows пустышка
- `CaptureStdout()` (`quiet.go`) - перехватывает вывод Composer (fmt и pterm); меняет глобальный `os.Stdout`, не потокобезопасна
```

В раздел «State Directory» добавить: `init-state.json` (отпечаток), `init.lock`; `backups/` также получает `kc.yaml.backup.<timestamp>` от `init`.

В «Build and Development Commands» добавить:

```bash
task test:docker -- ./pkg/initstate/... -v   # go test в контейнере, исходники read-only, на хост ничего не пишется
task test:container                          # gofmt+vet (linux, windows)+unit+e2e init в bash и zsh
```

В «Testing» добавить: тесты `init` и e2e запускаются в контейнере; `HOME` в тестах не используется (`StateDir` передаётся явно); e2e лежат в `e2e/` под build tag `e2e`.

В «Notes» добавить: `ScanDirectory` и `list` пропускают `cache/`, `http-cache/` и скрытые каталоги (корень обхода не проверяется) и сортируют найденные файлы по пути.

- [ ] **Step 3: Исправить спеку по итогам реализации**

В `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`:
- В «Изменения в коде» заменить фразу про пропуск `cache`/`http-cache` на: «сканер пропускает `cache`, `http-cache` и скрытые каталоги (`scanner.IsSkippedDir`; корень обхода не проверяется, потому что `~/.kube` скрытый) и возвращает файлы отсортированными по пути, чтобы результат не зависел от планировщика».
- В пункте про атомарную запись уточнить: «`cleaner.WriteFileAtomic` обновляет цель, если output — символическая ссылка; для init добавлен `Composer.WriteOutputAtomic`, `merge` по-прежнему пишет через `WriteOutput`».
- В описании `--max-size` добавить: «0 отключает порог».
- В тесте 9 заменить «битый источник при отсутствии `kc.yaml`: пустой stdout, код 1» на «нет каталога источников и нет `kc.yaml`: пустой stdout, код 1; битый источник рядом с хорошими: предупреждение, `export`, код 0».

- [ ] **Step 4: Итоговая проверка**

Run: `task test:container`
Expected: код 0, все `TestInit*` PASS, shell-проверки `ok`.

Run: `task check`
Expected: PASS (fmt, vet, `go test ./...`, shellcheck, `test:install`).

- [ ] **Step 5: Commit**

```bash
git add README.md AGENTS.md docs/superpowers/specs/2026-10-09-init-shell-integration-design.md
git commit -m "docs: document init command and update spec after implementation"
```

---

## Self-Review

**Spec coverage.** Цель и критерии → Task 7–9. Модель `kc.yaml` производный → Task 6, 7, README (Task 10). Интерфейс (флаги, stdout/stderr) → Task 8, 9. Алгоритм: обход и отпечаток → Task 3; актуально/порог/блокировка/пересборка → Task 7; перенос контекста до `Dedupe`, namespace после → Task 6, 7; пропавшие чужие записи с бэкапом → Task 6, 7, e2e (в том числе совместимость с `revert`); `configsEqual`-пропуск записи → Task 7 (`TestRunFirstBuildThenNoop`); атомарная запись → Task 2. Ошибки (export при сбое, код 1 без файла) → Task 7, 8, 9. Пропуск `cache/` в сканере и `merge` → Task 1. Тестирование в контейнере, windows-сборка → Task 4, 9. Документация → Task 10. Расхождения со спекой (скрытые каталоги, сортировка, `WriteOutputAtomic`, `--max-size 0`, формулировка теста 9) внесены в спеку в Task 10.

**Placeholder scan.** TBD/TODO нет; каждый шаг с кодом содержит код. Единственная условная инструкция — запасная проверка `list` в `init_shell_test.sh` (описана явной заменой).

**Type consistency.** `Scan(dir, output, salt...)` одинаково вызывается в Task 3 и Task 7. `Options`/`Result` совпадают в Task 7 и 8. `Session`-методы из Task 6 вызываются в Task 7 с теми же именами. `scanner.IsSkippedDir` используется в Task 1 и 3. `Composer.Result`/`WriteOutputAtomic` из Task 2 используются в Task 7.

**Review Focus.** Строка 1 → Task 1 (скрытый корень), Task 5 (кавычки `export`), Task 7 (`TestRunRelativeDir`), shell-тест (пробел в `HOME`). Строка 2 → Task 7 (`TestRunRebuildsWhenOutputDeletedOrCorrupt`, `TestRunEmptyDirGivesValidEmptyConfig`). Строка 3 → Task 7 и e2e (`SameContextName...`). Строка 4 → Task 2, 3 (`TestScanIgnoresServiceFilesAndOutput`). Строка 5 → README.
