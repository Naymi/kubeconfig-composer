# Worker E — SEC-10, SEC-11, SEC-12

Отчёт ревью: `.reviews/init-command/review-security-2026-10-16-2.md` (разделы SEC-10..12).
План и решения: `.reviews/init-command/round-2-2026-10-16/plan.md`, `decisions.md`.
Предыдущий раунд (контекст, как появились `clientlock.go` и порядок `rebuild`):
`.reviews/init-command/round-1-2026-10-16/` (plan, resolution, reports/worker-c.md).
Работаешь в основном checkout `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer`,
ветка `feat/init-command`. Решения ниже приняты координатором; если какое-то невыполнимо —
остановись и опиши причину в отчёте, не выбирай другое молча.

## Rule

- **SEC-10.** Пересборка не падает из-за того, что каталога `--output` ещё нет, а ошибка lock-файла не выдаётся за «lock занят».
- **SEC-11.** Прежний output, который init не смог прочитать, не перезаписывается без бэкапа.
- **SEC-12.** Пересборка не теряет параллельную запись kubectx/kubens в kc.yaml (насколько это вообще возможно без их участия — см. decisions.md).

## Sites and the compliant example

- SEC-10: `pkg/initstate/clientlock.go:22` (нет `MkdirAll`), `pkg/initstate/run.go:188-192` (сообщение «занят» на любую ошибку). Образец: `backupFile` в `run.go` и `TryLock` в `lock_unix.go` создают каталог до записи; client-go `clientcmd.lockFile` тоже делает `MkdirAll`.
- SEC-11: `run.go:195-199` (ветка `serr != nil`), `run.go:207-227` (бэкап только при `sess != nil`).
- SEC-12: `run.go:183-239` (окно `ReadSession` → rename), `carry.go` `ReadSession` (пустой файл = пустая сессия).

## Shape of the fix — SEC-10

- `lockKubeconfig`: `os.MkdirAll(filepath.Dir(lockPath), 0o755)` до цикла; ошибка `MkdirAll` возвращается как есть.
- Пакетная ошибка-значение `errKubeconfigLockBusy`; таймаут на `fs.ErrExist` возвращает её (обёрнутую с путём и таймаутом через `%w`).
- `rebuild` печатает «⚠ <path>.lock занят; если kubectl не запущен, удалите его» только при `errors.Is(err, errKubeconfigLockBusy)`. Прочие ошибки — просто возвращаются (их напечатает `cmd/init.go` через путь ошибки `Run`).

## What closes the invariant — SEC-10

Тесты: (1) `Run` с `Output` в несуществующем подкаталоге `t.TempDir()` → файл собран, `ExportOK`, `Rebuilt`; (2) `lockKubeconfig` для пути в несуществующем каталоге создаёт каталог и lock; (3) ошибка lock, отличная от «занят» (например, каталог output — это файл: `Output = <file>/kc.yaml`), — в Log нет текста «занят», `Run` возвращает ошибку. Существующий `TestRunBusyKubectlLockLeavesOutputAndState` продолжает проверять текст «занят».

## Comments this change makes false — SEC-10

Комментарий к `lockKubeconfig` (про то, что делает функция и когда ошибка).

## Shape of the fix — SEC-11

В `rebuild`: если `ReadSession` вернул ошибку и output существует — `backupFile(output, <StateDir>/backups)` до записи. Предупреждение одно: «⚠ не удалось прочитать прежний <output>: <err>; прежний файл сохранён: <backup>» (или «…; сохранить прежний файл не удалось: <berr>»). Бэкап делается один раз за `rebuild`, даже если попыток SEC-12 несколько (см. ниже).

## What closes the invariant — SEC-11

Тест: собранный output, затем в него дописан мусор (невалидный YAML), меняется источник → ровно один файл `kc.yaml.backup.*` в `backups/`, его содержимое = битый файл, в Log путь бэкапа. `TestRunRebuildsWhenOutputDeletedOrCorrupt` дополни проверкой бэкапа для битого случая (для удалённого — бэкапа нет).

## Comments this change makes false — SEC-11

Комментарий к `rebuild` (список шагов под lock).

## Shape of the fix — SEC-12

Под lock client-go (после `lockKubeconfig`), в цикле не более 3 попыток:
1. `before := stat(output)` (mtime+size; отсутствие файла — тоже значение).
2. Если файл существует и `size == 0` — это, вероятно, середина записи kubectx (truncate + write): если попытки остались — `sleep 10ms`, следующая попытка; иначе считать, что сессии нет.
3. `ReadSession` (+ SEC-11 бэкап, но только на первой попытке, где он понадобился — не повторять).
4. Восстановить собранный из источников конфиг: сразу после `Merge` (до цикла) сохрани `base := c.Result().DeepCopy()`; в начале каждой попытки `*c.Result() = *base.DeepCopy()`. (Это нужно, чтобы current-context/namespace первой попытки не протекли во вторую; `Dedupe` подменяет внутренний указатель Composer, поэтому присваивай именно через `*c.Result() =` в начале каждой попытки, а не один раз.)
5. `ApplyCurrentContext` → `c.Dedupe()` → `sourceNamespaces := contextNamespaces(...)` → `ApplyNamespaces` → `LostForeign`/бэкап (бэкап LostForeign — тоже не чаще одного раза за `rebuild`; предупреждение — один раз, по итогам последней попытки).
6. `after := stat(output)`; если `after != before` и попытки остались — следующая попытка; иначе — `OutputUnchanged`/`WriteOutputAtomic` с текущим результатом.
`Names`/`SourceNamespaces` для State — из попытки, которая записала (или подтвердила) файл.
Если структура получается проще с выделенной функцией одной попытки (`applySession(...)`), выдели её — но не добавляй новых экспортируемых API в `pkg/composer`.

## What closes the invariant — SEC-12

Тесты (детерминированные, без настоящих гонок): нужна точка внедрения между шагами 5 и 6 — допускается неэкспортируемое поле/хук в пакете (`var beforeWriteHook func()` в `run.go`, nil в проде), которое тест ставит и снимает. (1) Хук на первой попытке переписывает output с другим `current-context` (как kubectx) → в итоговом output этот `current-context`, попыток было 2; (2) хук превращает output в пустой файл на первой попытке и восстанавливает на второй → сессия перенесена, не сброшена; (3) повтор не тащит состояние первой попытки: на первой попытке в output namespace `a`, хук меняет на `b` → в результате `b`, не `a`; (4) хук меняет файл на каждой попытке → после 3 попыток запись всё равно происходит, без бесконечного цикла. Плюс все существующие тесты `TestRun*` зелёные.

## Comments this change makes false — SEC-12

Комментарий к `rebuild` (порядок шагов), комментарий к `lockKubeconfig` (что он защищает только от kubectl, а kubectx/kubens — перепроверкой). Спека: критерий успеха про kubectx/kubens (ищи в `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md` «kubectx») переписать под реальную гарантию: kubectl (lock client-go) не теряет запись; kubectx/kubens — кроме записи, пришедшей в промежуток между последней проверкой файла и rename (они не берут lock); пустой файл в момент чтения считается чужой записью в процессе. AGENTS.md (описание `pkg/initstate`) и README — только если они утверждают обратное; проверь `rg -n 'kubectx|kubens' README.md AGENTS.md`.

## Files (closed list)

- `pkg/initstate/clientlock.go`, `clientlock_test.go`
- `pkg/initstate/run.go`, `run_test.go`, `run_unix_test.go`
- `pkg/initstate/carry.go`, `carry_test.go`
- `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`, `AGENTS.md`, `README.md` — только места про эти три правила

## If the rule is broken outside these files

Опиши в отчёте (файл:строка, что не так), не правь.

## Verification commands and the report file

- `gofmt -l $(git ls-files '*.go')` пусто; `go vet ./...`, `GOOS=windows GOARCH=amd64 go vet ./...`.
- `go test ./pkg/... -count=1` (хост; только `t.TempDir()`), `task test:container` — код 0, `task check` — код 0 (запускается pre-commit hook'ом).
- Настоящие `~/.kube`, `~/.kubeconfig-composer` не трогать. Scratch — только в `.tmp/` и удалить свои `.go`-файлы оттуда до коммита (иначе `gofmt -l .` в `task check` падает).
- Коммит(ы) явными путями (не `-A`; не трогать `.reviews/`, `.serena/`), английское сообщение, например `fix: create output dir for kubectl lock, back up unreadable output, recheck before rename`, в конце `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Не пушить.

Отчёт — `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer/.reviews/init-command/round-2-2026-10-16/reports/worker-e.md`:
```
# Worker E report
commits: <sha> ...
## SEC-10 / SEC-11 / SEC-12 — что сделано, какой тест закрывает
## Найдено вне списка файлов
## Вывод проверок (кратко, с кодами возврата)
```
