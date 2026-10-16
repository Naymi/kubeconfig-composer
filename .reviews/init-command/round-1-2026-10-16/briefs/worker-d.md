# Worker D — SEC-07, SEC-09, SEC-05 (последнее место), документация раунда

Волна 2. Волна 1 уже в ветке `feat/init-command`: коммиты `e4f54e2` (воркер A: SEC-03, SEC-05)
и `d4d89f4` (воркер C: SEC-01, 02, 04, 06, 08). Прочитай их отчёты:
`.reviews/init-command/round-1-2026-10-16/reports/worker-a.md` и `worker-c.md`; план и
решения — `plan.md`, `decisions.md` там же; находки — `.reviews/init-command/review-security-2026-10-16.md`.
Работаешь прямо в основном checkout `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer`
на ветке `feat/init-command`.

## Rule

- **SEC-07.** Производный kc.yaml не становится источником для `merge` и не уезжает в trash через `merge --cleanup`.
- **SEC-09.** `task setup` не падает, когда Docker CLI есть, а daemon не запущен или нет сети.
- **SEC-05 (оставшееся место).** Обход для отпечатка и обход для сборки (и все прочие обходы каталога источников) одинаково реагируют на нечитаемый подкаталог.

## Sites and the compliant example

- SEC-07: `cmd/init.go:55` — output по умолчанию `~/.kube/kc.yaml`, внутри `--dir`, который `merge` сканирует по умолчанию (`cmd/merge.go:103`) и чистит (`cmd/merge.go:229-279`). `cmd/init.go:43` — `Example` кладёт output внутрь `--dir`.
- SEC-09: `Taskfile.yml` задача `setup:docker`.
- SEC-05: `cmd/list.go:37-41` — четвёртый обход, на ошибке не-корня прерывается. Образец правила — `scanner.FindKubeconfigFiles` и `composer.ScanDirectory` после коммита `e4f54e2`.

## Shape of the fix — SEC-07

- Output init по умолчанию — `filepath.Join(home, ".kubeconfig-composer", "kc.yaml")`. Чтобы не было двух мест, где собирается путь каталога состояния, введи в `cmd/init.go` одну переменную/функцию для `~/.kubeconfig-composer` и используй её и для `StateDir`, и для output по умолчанию. (Если в `cmd/` уже есть хелпер для этого каталога — например, в `merge.go`/`revert.go` для `backups/`, — используй его, не заводи второй.)
- `Example`: второй пример — `--dir ~/work/kube --output ~/work/kc.yaml` (output вне `--dir`).
- `merge` не меняется.

## What closes the invariant — SEC-07

e2e (`e2e/init_test.go`): `env.output()` — новый путь по умолчанию (`<home>/.kubeconfig-composer/kc.yaml`), все тесты, полагающиеся на старый путь, обновлены. Новый e2e-тест: после `init` запустить `merge` с параметрами по умолчанию (`HOME` — временный, как в остальных e2e; вопросы merge закрываются `< /dev/null` или `-y`, если флаг есть — посмотри `cmd/merge.go`) → в `~/.kube/config` нет записей, пришедших из kc.yaml повторно, и `merge --cleanup` (если без интерактива) не трогает kc.yaml. Если `merge --cleanup` интерактивен и в e2e неудобен — достаточно проверить, что kc.yaml не попал в список загруженных файлов (вывод `merge`).
`scripts/init_shell_test.sh` — обнови ожидаемые пути.

## Comments this change makes false — SEC-07

Всё, что говорит `~/.kube/kc.yaml`: `cmd/init.go` (Short/Long/Example, help флага), README, AGENTS.md, спека, комментарии в `pkg/initstate` (`rg -n 'kube/kc.yaml|\.kube.*kc\.yaml' --type go`). В `pkg/initstate` правь только комментарии, не код.

## Shape of the fix — SEC-09

```
if ! command -v docker >/dev/null 2>&1; then echo "docker не установлен, пропускаю (...)"; exit 0; fi
if ! docker info >/dev/null 2>&1; then echo "docker daemon недоступен, пропускаю (...)"; exit 0; fi
docker pull {{.GO_IMAGE}} || echo "не удалось скачать {{.GO_IMAGE}}; task test:docker скачает его при первом запуске"
```
Сообщения в стиле соседних задач. `desc` обнови.

## What closes the invariant — SEC-09

Теста на Taskfile в проекте нет и заводить его не нужно; проверка — ручная: `PATH` без docker → код 0; `DOCKER_HOST=unix:///nonexistent task setup:docker` → код 0 и сообщение про daemon. Приведи вывод в отчёте. Это осознанно не закрыто тестом — так и напиши в отчёте.

## Comments this change makes false — SEC-09

`desc` задачи, упоминание `setup` в AGENTS.md (если там про docker pull).

## Shape of the fix — SEC-05 (list)

Четвёртую копию правила не заводи. В `pkg/scanner` введи один обход:
`WalkKubeconfigFiles(dir string, onSkip func(path string, err error), visit func(path string) error) error` — ошибка корня возвращается; ошибка не-корня → `onSkip` (если не nil) и пропуск (`SkipDir` для каталога); `IsSkippedDir` для подкаталогов; `visit` только для `IsKubeconfigFile`. Через него переписываются `scanner.FindKubeconfigFiles` (onSkip = nil), `composer.ScanDirectory` (onSkip печатает `⚠ пропускаю <path>: <err>` как сейчас; exclude-проверка остаётся в visit) и `cmd/list.go`. `initstate.Scan` не трогай — он намеренно обходит все файлы через stat, без `IsKubeconfigFile` (не читает содержимое).

## What closes the invariant — SEC-05

Существующие тесты воркера A (`pkg/scanner/scanner_unix_test.go`, `pkg/composer/composer_unix_test.go`) должны проходить без изменений; добавь в `scanner_test.go` тест `WalkKubeconfigFiles` на пропуск `cache/`, `kubens/`, скрытого каталога и служебного файла, и в `scanner_unix_test.go` — на вызов `onSkip` для нечитаемого каталога (с тем же `Geteuid() == 0 → Skip`).

## Comments this change makes false — SEC-05

Комментарии над тремя обходами (`ScanDirectory`, `FindKubeconfigFiles`, `list`), которые воркер A дописал, — сведи к одному комментарию на `WalkKubeconfigFiles`.

## Документация раунда (для всех находок)

Обнови под итоговое поведение (опирайся на отчёты A и C, а не на старый текст):
- **README.md** — раздел про init: путь по умолчанию `~/.kubeconfig-composer/kc.yaml`; что output внутри `--dir` становится источником для `merge`; бэкап при пропаже записей, которые init не записывал (в том числе при первом запуске поверх файла, собранного `merge`, и для дописанных записей с префиксом `kc:`); правило namespace (правка в источнике доходит, если пользователь не менял namespace в kc.yaml); `--dir` может быть ссылкой; kubectx/kubens/`*.lock` не вызывают пересборку; после обновления первая пересборка может сделать один бэкап с предупреждением (старое состояние не читается) — init ещё не выходил в релиз, так что формулируй коротко или опусти, если README не описывает версии.
- **AGENTS.md** — описание `cmd/init.go` и `pkg/initstate` (файлы `init-state-<key>.json`, `init-<key>.lock`, `<output>.lock` client-go, `LostForeign`/`ApplyNamespaces` по состоянию, `resolvePaths`), раздел State Directory, Notes про пропускаемые каталоги/файлы (`kubens/`, `kubectx`, `*.lock`, `scanner.WalkKubeconfigFiles` как единственный обход).
- **Спека** `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md` — места, которые назвал воркер C (`:56`, `:65`, `:67-79`, `:94`, `:153`), плюс путь по умолчанию и SEC-03/05/09. Спека описывает итоговую реализацию, а не историю.

## Files (closed list)

- `cmd/init.go`, `cmd/list.go`
- `pkg/scanner/scanner.go`, `scanner_test.go`, `scanner_unix_test.go`
- `pkg/composer/composer.go` (только `ScanDirectory`)
- `pkg/initstate/*.go` — **только комментарии**
- `e2e/init_test.go`, `scripts/init_shell_test.sh`
- `Taskfile.yml`
- `README.md`, `AGENTS.md`, `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`

## If the rule is broken outside these files

Опиши в отчёте (файл:строка, что не так) и не правь — это уйдёт в повторное ревью.

## Verification commands and the report file

- `task test:container` — код 0 (gofmt, vet linux+windows, unit, e2e, bash/zsh).
- `task check` — код 0 (в том числе unix-тесты на нечитаемые каталоги, которые в контейнере под root пропускаются).
- Ручная проверка SEC-09 (см. выше).
- Настоящие `~/.kube` и `~/.kubeconfig-composer` не трогать.
- Один коммит (pre-commit hook запускает `task check`), английское сообщение, например `fix: move init output out of the source dir and unify source walks`, плюс `docs:`-часть можно вторым коммитом `docs: describe init hardening from review round 1`. В конце каждого сообщения — `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. `.reviews/` не коммить и не добавляй в индекс; `git add` только явными путями (не `-A`), `.serena/project.yml` не трогать.

Отчёт — `/Users/anatolijfedorov/prjcts/github/naymi/kubeconfig-composer/.reviews/init-command/round-1-2026-10-16/reports/worker-d.md`:
```
# Worker D report
commits: <sha> ...
## SEC-07 / SEC-09 / SEC-05 — что сделано, что закрывает
## Документация — что обновлено где
## Найдено вне списка файлов
## Вывод проверок (кратко, с кодами возврата)
```
