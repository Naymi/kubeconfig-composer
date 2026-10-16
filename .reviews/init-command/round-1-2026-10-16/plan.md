---
skills: [running-review-fix-rounds, persisting-review-artifacts]
skills-repo: naymi/skills @ 621a953
agent: claude-opus-5-5 (coordinator)
date: 2026-10-16
round: 1
findings:
  taken: [SEC-01, SEC-02, SEC-03, SEC-04, SEC-05, SEC-06, SEC-07, SEC-08, SEC-09]
  excluded: {}
---

# Round 1 — init-command, review-security-2026-10-16

Все 9 находок берутся в раунд (3 major, 6 minor). Init ещё не выходил ни в одном
релизе (`v0.1.0-alpha.0` собран с `test-release`, где init нет), поэтому формат
`init-state.json`, путь `kc.yaml` по умолчанию и семантику «своих» записей можно
менять без миграции.

## id → rule → sites → worker → wave

| ID | Rule | Sites (search) | Worker | Wave |
|---|---|---|---|---|
| SEC-01 | Если каталог источников задан символической ссылкой, init обходит её цель | `pkg/initstate/run.go:42` (dir), там же output; обходы `fingerprint.go` `Scan`, `composer.ScanDirectory` получают уже развёрнутый путь. Поиск: `rg -n 'filepath.Abs\(opts' pkg/initstate` | C | 1 |
| SEC-02 | init не перезаписывает без бэкапа файл, который он сам не собирал, и не теряет молча записи, которые пришли не из его источников | `pkg/initstate/carry.go:80` (`LostForeign` по префиксу), `run.go:143-151`. Поиск: `rg -n 'prefix\+":"' pkg/` — других мест, где «своё» определяется по префиксу в init, нет; `composer.effectiveName` использует префикс для идемпотентности `merge`, это другое правило | C | 1 |
| SEC-03 | В отпечаток попадают только файлы, которые могут быть источниками; служебные файлы kubectx/kubens/kubectl его не меняют | `pkg/initstate/fingerprint.go:51-63`, `pkg/scanner/scanner.go:14` (`skippedDirs`), `scanner.IsKubeconfigFile` (через него идут `ScanDirectory`, `FindKubeconfigFiles`, `list`). Поиск по ресурсу «перечисление файлов в каталоге источников»: `rg -n 'filepath\.(Walk|WalkDir)\(' --type go` → `fingerprint.go`, `composer.go`, `scanner.go` | A | 1 |
| SEC-04 | Правка namespace в источнике доходит до kc.yaml, а не перекрывается навсегда старым значением из kc.yaml | `pkg/initstate/carry.go:61-70` (`ApplyNamespaces`), `run.go:140` | C | 1 |
| SEC-05 | Обход для отпечатка и обход для сборки одинаково реагируют на нечитаемый подкаталог | те же три обхода, что в SEC-03: `fingerprint.go:39-44` (пропускает), `composer.go:219-221` (падает), `scanner.go` `FindKubeconfigFiles` (падает) | A | 1 |
| SEC-06 | Сохранённый отпечаток привязан к паре (`--dir`, `--output`), а не один на всех | `run.go:58` (`init-state.json`), `run.go:72` (`init.lock` — тот же ресурс «состояние init», тоже глобальный) | C | 1 |
| SEC-07 | Производный kc.yaml не становится источником для `merge` и не уезжает в trash через `merge --cleanup` | `cmd/init.go:55` (output по умолчанию внутри `--dir`), `cmd/merge.go:103`, `:229-279` | D | 2 |
| SEC-08 | Пересборка не теряет параллельную запись kubectl в kc.yaml | `run.go:113` → `:159` (окно между `ReadSession` и `rename`). Поиск по ресурсу «запись в output»: `rg -n 'WriteOutputAtomic\|WriteFileAtomic\|ReadSession' pkg/initstate` | C | 1 |
| SEC-09 | `task setup` не падает, когда Docker CLI есть, а daemon не запущен или нет сети | `Taskfile.yml:45-53` (`setup:docker`); других задач `setup:*` с сетью нет (`rg -n 'docker pull\|go mod download' Taskfile.yml`) | D | 2 |

### Already compliant (образцы)

- SEC-03/05: `Scan` уже пропускает ошибки не-корня (`fingerprint.go:39-44`) — образец поведения для двух других обходов.
- SEC-08: client-go сам сериализует свои записи через `<file>.lock` (`clientcmd.lockFile`, `O_CREATE|O_EXCL`, снимается удалением) — init встраивается в тот же протокол.
- SEC-01: `cleaner.WriteFileAtomic` уже обновляет цель, если output — ссылка на файл.

## Принятые решения по форме (обязательны для воркеров)

- **SEC-01.** В `Run`: `dir` = `filepath.EvalSymlinks(abs(dir))`, если путь существует (иначе `abs`, ошибка случится ниже, как сейчас). Для output разворачивается его родительский каталог (`EvalSymlinks(filepath.Dir(abs))` + `Base`), если каталог существует. Развёрнутые пути используются для `Scan`, `ScanDirectory`, `SetBaseDir`, `ReadSession`, записи и соли отпечатка. `Result.Output` остаётся `abs(opts.Output)` без разворота — это путь, который пользователь видит в `KUBECONFIG`. Дополнительно: если при пересборке `snap.Files == 0`, в лог `⚠ в <dir> не найдено ни одного файла-источника`.
- **SEC-02.** «Своё» определяется не префиксом, а состоянием: `State.Names []string` — отсортированные имена кластеров, пользователей и контекстов, которые init записал (или подтвердил через `OutputUnchanged`) в прошлый раз. Пропавшая из прежнего файла запись, которой нет в `State.Names`, — чужая → бэкап + предупреждение (как сейчас). Если состояния нет (первый запуск для этой пары или файл состояния повреждён), а output существует, чужими считаются все пропавшие записи. Проверка префикса в `LostForeign` удаляется; параметр `prefix` у `LostForeign` уходит.
- **SEC-04.** `State.SourceNamespaces map[string]string` — namespace каждого контекста, каким его дал собранный из источников конфиг на прошлой сборке (после `Dedupe`, до переноса). При переносе старый непустой namespace из kc.yaml применяется, только если он отличается от `SourceNamespaces[ctx]` прошлой сборки (значит, его поменял пользователь через kubens/kubectl), или если записи о контексте в состоянии нет. Иначе побеждает источник.
- **SEC-06.** Ключ пары = первые 12 hex sha256(`dir` + `\x00` + `output`) по развёрнутым путям из SEC-01. Файлы: `<StateDir>/init-state-<key>.json` и `<StateDir>/init-<key>.lock`. Старый `init-state.json` не читается и не удаляется.
- **SEC-08.** `rebuild` перестраивается: сначала без всяких блокировок `ScanDirectory`+`Merge`; затем берётся lock client-go `<output>.lock` (`os.OpenFile(..., O_CREATE|O_EXCL, 0o600)`, повтор каждые 10 мс до 1 с, снятие — `os.Remove`), и под ним: `ReadSession` → `ApplyCurrentContext` → `Dedupe` → `ApplyNamespaces` → `LostForeign`/бэкап → `OutputUnchanged`/`WriteOutputAtomic`. Если lock не взят за 1 с: в лог `⚠ <output>.lock занят; если kubectl не запущен, удалите его`, запись пропускается, `Run` возвращает ошибку (состояние не сохраняется → следующий старт повторит).
- **SEC-03.** В `pkg/scanner`: `kubens` добавляется в `skippedDirs`; новая `IsServiceFile(name string) bool` — `name == "kubectx" || strings.HasSuffix(name, ".lock")`. `IsKubeconfigFile` возвращает false для служебных файлов; `Scan` в `fingerprint.go` пропускает их тем же предикатом.
- **SEC-05.** Все три обхода (`Scan`, `ScanDirectory`, `FindKubeconfigFiles`): ошибка на корне — фатальна, ошибка на не-корне — пропуск (`SkipDir` для каталога, `nil` для файла). `ScanDirectory` при этом печатает `⚠ пропускаю <path>: <err>` тем же способом, что и прочие предупреждения Composer (их перехватывает `CaptureStdout`).
- **SEC-07.** Output init по умолчанию — `~/.kubeconfig-composer/kc.yaml` (каталог состояния утилиты, `merge` его не сканирует). `Example` в `cmd/init.go` перестаёт класть output внутрь `--dir`. README/AGENTS/спека говорят, что output внутри `--dir` превращает его в источник для `merge`.
- **SEC-09.** `setup:docker`: пропуск с сообщением, если `docker info` неуспешен; неудачный `docker pull` — сообщение, код 0.

## file × worker

| File | A (w1) | C (w1) | D (w2) |
|---|---|---|---|
| pkg/scanner/scanner.go, scanner_test.go | ✔ | | |
| pkg/composer/composer.go, composer_test.go (только `ScanDirectory`) | ✔ | | |
| pkg/initstate/fingerprint.go, fingerprint_test.go | ✔ | | |
| pkg/initstate/run.go, run_test.go, run_unix_test.go | | ✔ | |
| pkg/initstate/state.go, state_test.go | | ✔ | |
| pkg/initstate/carry.go, carry_test.go | | ✔ | |
| pkg/initstate/clientlock.go (+_test) — новый | | ✔ | |
| cmd/init.go, cmd/merge.go (только если понадобится) | | | ✔ |
| e2e/init_test.go, scripts/init_shell_test.sh | | | ✔ |
| Taskfile.yml | | | ✔ |
| README.md, AGENTS.md, docs/superpowers/specs/2026-10-09-init-shell-integration-design.md | | | ✔ |

Волна 1 — A и C параллельно, каждый в своём git worktree (иначе `task test:docker`
одного видит полуготовые правки другого). Пересечений по файлам нет. Волна 2 (D)
стартует после мёржа A и C: e2e и документация описывают итоговое поведение обоих,
а смена output по умолчанию и имени файла состояния ломает e2e, который D и правит.

### Дополнение после волны 1

- SEC-05: воркер A нашёл четвёртый обход — `cmd/list.go:37-41` (поиск по `filepath.Walk(` его пропустил: в плане перечислены только три). Назначен воркеру D (волна 2) вместе с выносом единого обхода `scanner.WalkKubeconfigFiles`, чтобы правило не копировалось в четвёртый раз.
- SEC-03: замечание воркера C про `<output>.lock` в отпечатке уже закрыто у A (`IsServiceFile` покрывает `*.lock`).

## Excluded sites

Нет. Все находки и все найденные места берутся в этот раунд.

## Diff review before merging

Шаг координатора. Для каждой ветки: все ли места правила закрыты (по таблице выше),
нет ли новой копии уже существующего хелпера (в частности, второго способа
вычислять ключ пары или второго предиката служебных файлов), не остались ли
комментарии, ставшие ложными (`LostForeign` про префикс, `Scan` про «каждый файл»,
`init-state.json` в AGENTS.md), закрывает ли инвариант тест, а не комментарий.
Итог — `resolution.md`.
