# Worker A — SEC-03, SEC-05

Контекст: ветка `feat/init-command`, команда `kubeconfig-composer init` (см. AGENTS.md,
спека `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`). Отчёт ревью:
`.reviews/init-command/review-security-2026-10-16.md` (разделы SEC-03 и SEC-05),
план раунда: `.reviews/init-command/round-1-2026-10-16/plan.md`.

## Rule

- **SEC-03.** В отпечаток попадают только файлы, которые могут быть источниками; служебные файлы kubectx/kubens/kubectl его не меняют.
- **SEC-05.** Обход для отпечатка и обход для сборки одинаково реагируют на нечитаемый подкаталог.

## Sites and the compliant example

Все обходы каталога источников в проекте (`rg -n 'filepath\.(Walk|WalkDir)\(' --type go`):
- `pkg/initstate/fingerprint.go` `Scan` — отпечаток;
- `pkg/composer/composer.go` `ScanDirectory` — сборка (`merge` и `init`);
- `pkg/scanner/scanner.go` `FindKubeconfigFiles` — `list`, `status`, `cleanup`.

SEC-03: фильтр по имени — `fingerprint.go:51-63` (только «скрытое» и output), `scanner.go:14` (`skippedDirs`), `scanner.IsKubeconfigFile`.
SEC-05: `fingerprint.go:39-44` пропускает ошибки не-корня — **это образец**; `composer.go:219-221` и `FindKubeconfigFiles` на той же ошибке прерывают весь обход.

## Shape of the fix — SEC-03

- `skippedDirs` в `pkg/scanner/scanner.go` получает `"kubens"` (kubens хранит там `~/.kube/kubens/<context>`). Обнови комментарий к `skippedDirs`.
- Новая экспортируемая функция в `pkg/scanner`: `IsServiceFile(name string) bool` — `name == "kubectx" || strings.HasSuffix(name, ".lock")`, с комментарием: kubectx пишет `~/.kube/kubectx`, client-go на время записи создаёт `<kubeconfig>.lock`.
- `IsKubeconfigFile` возвращает `false` для `IsServiceFile(name)` — до проверки расширения.
- `Scan` в `fingerprint.go` пропускает файл, если `scanner.IsServiceFile(d.Name())`. Других копий этого предиката быть не должно.

## What closes the invariant — SEC-03

Тесты:
- `fingerprint_test.go`: после первого `Scan` создать `kubectx`, `kubens/ctx`, `config.lock`, `kc.yaml.lock` → `Snapshot.Hash` и `Files` не изменились. Плюс контрольная проверка, что новый `x.yaml` хеш меняет (если такой уже есть — не дублировать).
- `scanner_test.go`: `IsServiceFile` на таблице имён; `IsKubeconfigFile` false для `kubectx` и `config.lock` даже с валидным kubeconfig-содержимым; `IsSkippedDir("kubens")` true.

## Comments this change makes false — SEC-03

- Комментарий к `Scan` в `fingerprint.go` («Пропускаются скрытые файлы … сам output и каталоги из scanner.IsSkippedDir») — дополнить служебными файлами.
- Комментарий к `skippedDirs`/`IsSkippedDir` в `scanner.go`.
- AGENTS.md и README **не трогай** — их правит воркер D во второй волне (он получит твой отчёт).

## Shape of the fix — SEC-05

Все три обхода ведут себя одинаково: ошибка на корне — фатальна (возвращается как сейчас); ошибка на не-корне — пропуск: для каталога `filepath.SkipDir`, для файла `nil`.
- `ScanDirectory`: при пропуске печатает предупреждение `⚠ пропускаю <path>: <err>` тем же способом, каким Composer печатает остальные предупреждения в `ScanDirectory` (посмотри, как сейчас сообщается о файле, который не загрузился, и повтори этот стиль; в init это перехватывает `CaptureStdout`).
- `FindKubeconfigFiles`: молча пропускает (она уже молча пропускает файлы, которые не грузятся).
- `Scan`: уже так, не менять.
- Обрати внимание: у `filepath.Walk` при ошибке чтения каталога callback вызывается второй раз для того же каталога с ошибкой — `SkipDir` на нём корректен; убедись тестом, что содержимое остальных каталогов попадает в результат.

## What closes the invariant — SEC-05

Тесты (в `composer_test.go` и `scanner_test.go`): каталог `locked/` с правами `0o000` рядом с хорошим `a.yaml` → `ScanDirectory` без ошибки загружает `a.yaml`; `FindKubeconfigFiles` возвращает `a.yaml` без ошибки. Права восстанови в `t.Cleanup` (иначе `t.TempDir` не удалится). Под root права не действуют, поэтому тест делает `t.Skip`, если `os.Geteuid() == 0`, и кладётся в файл с `//go:build !windows` (например `composer_unix_test.go`, `scanner_unix_test.go`). Контейнер тестов работает под root — значит, эти тесты реально выполнятся только в `go test ./...` на хосте; это допустимо (они пишут только в `t.TempDir()`).

## Comments this change makes false — SEC-05

Комментарий у `Scan` про «файл или подкаталог исчез или недоступен — пропускаем» остаётся верным. Если в `ScanDirectory`/`FindKubeconfigFiles` есть комментарии про остановку обхода — поправь.

## Files (closed list)

- `pkg/scanner/scanner.go`, `pkg/scanner/scanner_test.go`, новый `pkg/scanner/scanner_unix_test.go`
- `pkg/composer/composer.go` (только `ScanDirectory`), `pkg/composer/composer_test.go`, новый `pkg/composer/composer_unix_test.go`
- `pkg/initstate/fingerprint.go`, `pkg/initstate/fingerprint_test.go`

Не трогай `pkg/initstate/run.go`, `state.go`, `carry.go` и их тесты — их параллельно правит воркер C.

## If the rule is broken outside these files

Если найдёшь ещё один обход каталога источников или ещё один фильтр «служебных» имён вне списка — не правь, опиши в отчёте (файл:строка, что не так). Координатор назначит его воркеру D во второй волне.

## Verification commands and the report file

Ты работаешь в изолированном git worktree. Все команды — из его корня.
- `gofmt -l .` пусто; `go vet ./...` и `GOOS=windows GOARCH=amd64 go vet ./...` чистые.
- `go test ./pkg/scanner/... ./pkg/composer/... ./pkg/initstate/...` на хосте (тесты пишут только в `t.TempDir()`; настоящие `~/.kube` и `~/.kubeconfig-composer` не трогать).
- `task test:docker -- ./pkg/... -count=1` в контейнере.
- Закоммить правки в ветку worktree одним коммитом с английским сообщением в стиле conventional commits (например `fix: skip kubectx/kubens service files and unreadable subdirs in scans`), в конце сообщения строка `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Если pre-commit hook (`task check`) не установлен в worktree — всё равно прогони команды выше.

Отчёт — файл `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer/.reviews/init-command/round-1-2026-10-16/reports/worker-a.md` (абсолютный путь, в основной checkout, не в worktree). Формат:
```
# Worker A report
branch: <ветка worktree>, commit: <sha>
## SEC-03 — что сделано, какие места, какой тест закрывает
## SEC-05 — то же
## Найдено вне списка файлов
## Вывод проверок (кратко, с кодами возврата)
```
