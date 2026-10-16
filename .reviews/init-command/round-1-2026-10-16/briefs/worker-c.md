# Worker C — SEC-01, SEC-02, SEC-04, SEC-06, SEC-08

Контекст: ветка `feat/init-command`, команда `kubeconfig-composer init` (AGENTS.md;
спека `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`). Отчёт ревью:
`.reviews/init-command/review-security-2026-10-16.md` (разделы SEC-01, 02, 04, 06, 08);
план раунда с принятыми решениями: `.reviews/init-command/round-1-2026-10-16/plan.md`,
обоснования — `decisions.md` рядом. Решения ниже приняты координатором, не пересматривай их;
если решение невыполнимо — остановись и опиши причину в отчёте.

## Rule

- **SEC-01.** Если каталог источников задан символической ссылкой, init обходит её цель, а не собирает пустой конфиг молча.
- **SEC-02.** init не перезаписывает без бэкапа файл, который он сам не собирал, и не теряет молча записи, которые пришли не из его источников.
- **SEC-04.** Правка namespace в источнике доходит до kc.yaml, а не перекрывается навсегда старым значением из kc.yaml.
- **SEC-06.** Сохранённый отпечаток привязан к паре (`--dir`, `--output`), а не один на всех.
- **SEC-08.** Пересборка не теряет параллельную запись kubectl в kc.yaml.

## Sites and the compliant example

- SEC-01: `pkg/initstate/run.go:42-47` (`filepath.Abs` без разворота ссылок). Образец: `cleaner.WriteFileAtomic` уже пишет в цель, если output — ссылка на файл.
- SEC-02: `pkg/initstate/carry.go:73-92` (`LostForeign`, проверка префикса), `run.go:143-151`.
- SEC-04: `carry.go:61-70` (`ApplyNamespaces`), `run.go:140`.
- SEC-06: `run.go:58` (`init-state.json`), `run.go:72` (`init.lock`).
- SEC-08: `run.go:110-163` — окно от `ReadSession` до `WriteOutputAtomic`. Образец протокола: client-go `clientcmd` (`lockFile`/`unlockFile` в `k8s.io/client-go/tools/clientcmd/loader.go`, посмотри в module cache: `go env GOMODCACHE`) — `os.OpenFile(filename+".lock", O_CREATE|O_EXCL, 0)`, снятие — `os.Remove`.

## Shape of the fix — SEC-01

В `Run`:
- `dir` = `filepath.EvalSymlinks(abs(opts.Dir))`, если разворот удался; если путь не существует — `abs` как сейчас (ошибка случится в `Scan`, поведение «нет каталога источников» не меняется).
- Для output: `abs(opts.Output)`; если его родительский каталог существует — `EvalSymlinks(Dir)` + `Base`. Этот развёрнутый путь используется везде внутри (Scan, ScanDirectory exclude, ReadSession, запись, бэкап, ключ пары SEC-06, соль отпечатка).
- `Result.Output` = `abs(opts.Output)` **без** разворота: это путь, который печатается в `export KUBECONFIG`.
- Если при пересборке (то есть дошли до `rebuild`) `snap.Files == 0` — в лог `⚠ в <dir> не найдено ни одного файла-источника` (сборка продолжается, пустой kc.yaml валиден).

## What closes the invariant — SEC-01

Тесты в `run_test.go` (или `run_unix_test.go`, если нужны symlink-специфичные вещи): (1) `home/.kube` — ссылка на `dotfiles/kube` с источником → контексты источника в output, `Result.Output` — путь через ссылку; (2) output лежит внутри каталога-ссылки (`--dir` = ссылка, `--output` = `<ссылка>/kc.yaml`) → output не становится источником сам себе (после двух прогонов контекстов столько же, нет `kc:kc:…`); (3) пустой каталог → в Log есть предупреждение про ноль источников.

## Comments this change makes false — SEC-01

Комментарий к `Options.Dir`/`Output` и к `Run`, если там сказано про абсолютный путь. Комментарий к `Scan` в `fingerprint.go` («корень обхода не проверяется — ~/.kube скрытый») остаётся верным; `fingerprint.go` правит воркер A — не трогай.

## Shape of the fix — SEC-06

- Функция `pairKey(dir, output string) string` = первые 12 hex `sha256(dir + "\x00" + output)` по развёрнутым путям. Единственное место вычисления ключа.
- Файлы: `<StateDir>/init-state-<key>.json`, `<StateDir>/init-<key>.lock`. Старый `init-state.json` не читается и не удаляется.

## What closes the invariant — SEC-06

Тест: две пары (`k1`/`o1`, `k2`/`o2`) с одним `StateDir`, попеременно `Run` для каждой по 2 раза после первой сборки → ни один повторный запуск не пересобирает (`Rebuilt == false`, mtime файла состояния своей пары не меняется), файлов состояния два. Плюс: занятый lock пары A не мешает пересборке пары B.

## Comments this change makes false — SEC-06

Всё, что упоминает `init-state.json`/`init.lock` в `pkg/initstate` (в том числе тесты `run_unix_test.go`, которые берут `filepath.Join(f.state, "init.lock")` — переведи их на хелпер). Документацию (AGENTS.md, README, спека) **не трогай** — её правит воркер D во второй волне по твоему отчёту.

## Shape of the fix — SEC-02

- `State` получает `Names []string` — отсортированные имена кластеров, пользователей и контекстов того конфига, который init записал или подтвердил (`OutputUnchanged`) на этой сборке. Сохраняется вместе с отпечатком.
- `LostForeign(cfg, owned map[string]bool / []string)` — пропавшая из прежнего файла запись считается чужой, если её нет среди имён прошлой записи. Если состояния для пары нет (первый запуск, файл повреждён) — `owned` пуст, и чужими считаются все пропавшие. Параметр `prefix` и проверка `strings.HasPrefix(name, prefix+":")` удаляются.
- Бэкап и предупреждение — как сейчас (`backupFile`, `formatLost`).
- Обрати внимание на порядок: состояние прошлой сборки нужно прочитать до пересборки (оно уже читается в `upToDate`; не читай файл трижды — загрузи один раз и передай).

## What closes the invariant — SEC-02

Тесты: (1) существующий output с записями `kc:prod:prod.yaml` (как от `merge`), состояния нет, источников, дающих эти записи, нет → бэкап в `<StateDir>/backups/` и предупреждение в Log; (2) после собственной сборки удалить источник → его записи пропадают **без** бэкапа и предупреждения (как сейчас); (3) после собственной сборки дописать в output чужую запись без префикса (как `aws eks update-kubeconfig`), изменить источник → бэкап и предупреждение (существующий тест, если есть, должен продолжать проходить); (4) чужая запись **с** префиксом `kc:` дописана в output после сборки → бэкап и предупреждение (это и есть суть SEC-02).

## Comments this change makes false — SEC-02

Комментарий к `LostForeign` (весь абзац про префикс), к `Session.Names`, к полю `State` («Version и Updated нужны только человеку»), комментарии в `rebuild` около бэкапа.

## Shape of the fix — SEC-04

- `State` получает `SourceNamespaces map[string]string` — namespace каждого контекста в собранном из источников конфиге (после `Dedupe`, **до** переноса namespace из прежнего kc.yaml).
- `ApplyNamespaces(cfg, prevSource map[string]string)`: для каждого контекста прежнего файла с непустым namespace — применить, если `prevSource == nil`, или контекста нет в `prevSource`, или `prevSource[ctx] != старый namespace` (значит, его поменял пользователь). Иначе (старый namespace совпадает с тем, что прошлый раз дал источник) — побеждает источник.

## What closes the invariant — SEC-04

Тесты: (1) источник с `namespace: old-ns` → сборка → источник меняется на `new-ns` → в output `new-ns`; (2) после сборки пользователь меняет namespace прямо в output (как kubens) на `mine` и меняется другой источник → `mine` сохраняется (существующий тест переноса namespace должен продолжать проходить); (3) нет состояния → прежнее поведение (перенос).

## Comments this change makes false — SEC-04

Комментарий к `ApplyNamespaces` («переносит непустые прежние namespace…»).

## Shape of the fix — SEC-08

`rebuild` перестраивается так:
1. Без блокировок: `composer.New(true)`, `SetBaseDir`, `SetNamePrefix`, `CaptureStdout(ScanDirectory+Merge)`, предупреждения в лог.
2. Берётся lock client-go: новый файл `pkg/initstate/clientlock.go` — `lockKubeconfig(path string, timeout time.Duration) (unlock func(), err error)`: `os.OpenFile(path+".lock", O_CREATE|O_EXCL|O_WRONLY, 0o600)`, повтор каждые 10 мс до `timeout` (1 с), снятие — `os.Remove`. Работает и на windows (O_EXCL поддерживается), build-тегов не нужно.
3. Под ним: `ReadSession` → `ApplyCurrentContext` (до `Dedupe`, как сейчас) → `Dedupe` → запомнить `SourceNamespaces` → `ApplyNamespaces` → `LostForeign`/бэкап → `OutputUnchanged` / `WriteOutputAtomic` → снятие lock.
4. Если lock не взят за 1 с: в лог `⚠ <output>.lock занят; если kubectl не запущен, удалите его`, `rebuild` возвращает ошибку, `Run` не сохраняет состояние (следующий старт повторит), `ExportOK` по-прежнему определяется наличием файла.
`rebuild` должна вернуть то, что нужно сохранить в состоянии (`Names`, `SourceNamespaces`), — состояние пишет `Run`, как сейчас.

## What closes the invariant — SEC-08

Тесты: (1) `<output>.lock` существует (создан тестом) и снимается через 200 мс в горутине → `Run` дожидается и пересобирает; (2) lock не снимается → `Run` возвращает ошибку, output не изменён, состояние не сохранено (следующий `Run` после снятия пересобирает); (3) после успешного `Run` файла `<output>.lock` нет. Тест на lock-файл — с `LockTimeout`-подобным параметром, чтобы не ждать секунду: допускается поле `Options.KubeconfigLockTimeout time.Duration` (0 → 1 с).

## Comments this change makes false — SEC-08

Комментарии в `rebuild` про порядок шагов, комментарий к `Run`, если там описан порядок.

## Files (closed list)

- `pkg/initstate/run.go`, `run_test.go`, `run_unix_test.go`
- `pkg/initstate/state.go`, `state_test.go`
- `pkg/initstate/carry.go`, `carry_test.go`
- новые `pkg/initstate/clientlock.go`, `pkg/initstate/clientlock_test.go`

Не трогай `pkg/initstate/fingerprint.go`, `pkg/scanner`, `pkg/composer` (воркер A), `cmd/`, `e2e/`, `scripts/`, `Taskfile.yml`, документацию (воркер D, волна 2). `Scan` вызывай с прежней сигнатурой.

## If the rule is broken outside these files

Если правило нарушено в файлах вне списка (например, e2e опирается на старое имя `init-state.json` или на `init.lock`, или `cmd/init.go` нужен другой контракт) — не правь, опиши в отчёте точно: файл:строка, что сломается, что нужно поменять. Это передаётся воркеру D во второй волне. e2e (`go test -tags e2e`) после твоих правок может падать — это ожидаемо, D его чинит; укажи в отчёте, что именно падает.

## Verification commands and the report file

Ты работаешь в изолированном git worktree. Все команды — из его корня.
- `gofmt -l .` пусто; `go vet ./...` и `GOOS=windows GOARCH=amd64 go vet ./...` чистые.
- `go test ./pkg/... -count=1` на хосте (тесты пишут только в `t.TempDir()`; настоящие `~/.kube` и `~/.kubeconfig-composer` не трогать, `HOME` в тестах не использовать).
- `task test:docker -- ./pkg/... -count=1` в контейнере.
- Закоммить правки в ветку worktree одним коммитом с английским сообщением в стиле conventional commits (например `fix: harden init rebuild against symlinks, foreign entries and kubectl writes`), в конце сообщения строка `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Если pre-commit hook падает только на e2e/вне твоих файлов — опиши в отчёте, коммит без hook (`--no-verify`) допускается только в этом случае.

Отчёт — файл `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer/.reviews/init-command/round-1-2026-10-16/reports/worker-c.md` (абсолютный путь, в основной checkout, не в worktree). Формат:
```
# Worker C report
branch: <ветка worktree>, commit: <sha>
## SEC-01 / SEC-02 / SEC-04 / SEC-06 / SEC-08 — по разделу: что сделано, какой тест закрывает
## Что нужно воркеру D (e2e, cmd, документация) — файл:строка, что поменялось в контракте
## Найдено вне списка файлов
## Вывод проверок (кратко, с кодами возврата)
```
