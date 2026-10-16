# Worker F — SEC-13

Отчёт: `.reviews/init-command/review-security-2026-10-16-3.md` (раздел SEC-13, там же два сценария harness'а).
План и решения: `.reviews/init-command/round-3-2026-10-16/plan.md`, `decisions.md`. Контекст цикла
попыток — `round-2-2026-10-16/` (brief worker-e, resolution). Основной checkout, ветка `feat/init-command`.

## Rule

Бэкап, на который ссылается предупреждение о пропавших чужих записях, содержит ту версию прежнего файла, по которой эти записи посчитаны, и не затирается другим бэкапом той же пересборки.

## Sites and the compliant example

`pkg/initstate/run.go` — бэкап нечитаемого (`unreadableSaved`), бэкап `lost` (`lostSaved`), итоговое предупреждение, `backupFile` (имя до секунды, `os.WriteFile`). Образец: `cmd/revert.go` `findBackups` — по префиксу `<name>.backup.` и mtime, суффикс в имени примет.

## Shape of the fix

- `carry.go`: `sessionFromBytes(data []byte) (*Session, error)` через `clientcmd.Load`; `ReadSession(path)` — `os.ReadFile` (`ErrNotExist` → `nil, nil`) + `sessionFromBytes`. Других читателей нет — проверь `rg -n ReadSession`.
- `run.go`, на каждой попытке: `before := stampOf(output)`; `data, rerr := os.ReadFile(output)` (нет файла → сессии нет); пустой — как сейчас; `sess, serr := sessionFromBytes(data)`.
- `backupFile(name string, data []byte, backupDir string) (string, error)`: `<name>.backup.<20060102-150405>`, `os.OpenFile(O_CREATE|O_EXCL|O_WRONLY, 0o600)`; при `ErrExist` — `…-2`, `…-3`, … (разумный предел, например 100, затем ошибка). Права 0600, каталог 0755 как сейчас.
- `saved := map[fileStamp]string{}` за `rebuild`: нужен бэкап (нечитаемый файл или `len(lost) > 0`) и `before` ещё не в `saved` → `backupFile(data)` → `saved[before] = path` (или ошибку запомнить для предупреждения). Флаги `unreadableSaved`/`lostSaved` удаляются.
- Нечитаемый: предупреждение при каждом новом бэкапе («⚠ не удалось прочитать прежний …: …; прежний файл сохранён: …» / «сохранить не удалось»).
- `lost`: одно предупреждение после цикла, по `lost` последней попытки и с `saved[stamp последней попытки]` (или ошибкой бэкапа этой версии).

## What closes the invariant

Тесты в `run_test.go` через `beforeWriteHook`:
1. Сценарий 1 отчёта: в output чужая `f1`; хук на попытке 1 дописывает чужую `f2` → предупреждение перечисляет `f1` и `f2`, и бэкап, путь которого в предупреждении, содержит обе.
2. Сценарий 2: хук на попытке 1 делает output нечитаемым, на попытке 2 возвращает версию с `f1` → в `backups/` есть копия с `f1`, и она — та, на которую ссылается итоговое предупреждение; ни один бэкап не перезаписан (число файлов в `backups/` = число разных версий).
3. `backupFile` дважды за одну секунду с разными байтами → два файла, оба содержимых сохранены, второй с суффиксом `-2`.
4. Существующие тесты бэкапов (`TestRunReportsLostForeignEntriesAndBacksUp`, `TestRunBacksUp*`, `TestRunRebuildsWhenOutputDeletedOrCorrupt`, e2e `TestInitForeignEntryWarnsBacksUpAndRevertSeesBackup`) зелёные.

## Comments this change makes false

Комментарий к `rebuild` («нечитаемый файл сохраняется в бэкап», про «не больше одного раза»), к `backupFile`, к `ReadSession`. Спека, шаг 8 («Бэкапы шагов 4 и 8 делаются не больше одного раза за пересборку») — переписать: не больше одного раза на версию прежнего файла; бэкап — те же байты, по которым посчитана сессия; существующий бэкап не перезаписывается. AGENTS.md — только если там то же утверждение (`rg -n 'бэкап|backup' AGENTS.md`).

## Files (closed list)

- `pkg/initstate/run.go`, `run_test.go`
- `pkg/initstate/carry.go`, `carry_test.go`
- `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`, `AGENTS.md` — только места про бэкапы init

## If the rule is broken outside these files

Опиши в отчёте, не правь.

## Verification commands and the report file

- `gofmt -l $(git ls-files '*.go')` пусто; `go vet ./...`, `GOOS=windows GOARCH=amd64 go vet ./...`.
- `go test ./pkg/... -race -count=1` (хост, только `t.TempDir()`), `task test:container` — 0, `task check` — 0 (pre-commit hook).
- Настоящие `~/.kube`, `~/.kubeconfig-composer` не трогать; scratch в `.tmp/`, `.go`-файлы оттуда удалить до коммита.
- Один коммит явными путями (не `-A`; не трогать `.reviews/`, `.serena/`), английское сообщение, например `fix: back up each version of the previous output from the bytes read, never overwrite backups`. **Без** строк атрибуции (`Co-Authored-By` и подобных) — так требуют правила пользователя. Не пушить.

Отчёт — `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer/.reviews/init-command/round-3-2026-10-16/reports/worker-f.md`:
```
# Worker F report
commits: <sha>
## SEC-13 — что сделано, какой тест закрывает
## Найдено вне списка файлов
## Вывод проверок (кратко, с кодами возврата)
```
