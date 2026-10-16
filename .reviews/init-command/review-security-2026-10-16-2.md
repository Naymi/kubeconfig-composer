---
skills: [thermo-nuclear-review, thermo-nuclear-review-upstream, persisting-review-artifacts]
skills-repo: naymi/skills @ 621a953
agent: claude-opus-5-5 (subagent)
date: 2026-10-16
approval: changes-required
findings: {blockers: 0, majors: 1, minors: 2}
---

# Security & correctness review (повтор после раунда 1): `feat/init-command` (base `main`)

| ID | Severity | Rule | First site | Why this severity |
|---|---|---|---|---|
| SEC-10 | major | Пересборка не падает из-за того, что каталога `--output` ещё нет, а ошибка lock-файла не выдаётся за «lock занят» | `pkg/initstate/clientlock.go:22` | регрессия раунда 1: init с `--output` в новом каталоге не работает ни разу, а stderr советует удалить несуществующий файл; конфигурация по умолчанию не затронута |
| SEC-11 | minor | Прежний output, который init не смог прочитать, не перезаписывается без бэкапа (продолжение SEC-02) | `pkg/initstate/run.go:195` | нужен битый kc.yaml (опечатка при ручной правке) плюс изменение источника; предупреждение в stderr есть, копии нет |
| SEC-12 | minor | Пересборка не теряет параллельную запись kubectx/kubens в kc.yaml (SEC-08 закрыт только для kubectl) | `pkg/initstate/run.go:188` | окно сузилось до миллисекунд (операции в памяти + rename), теряется только переключение контекста или namespace |

## Scope и как проверялось

- Diff: `git diff main...HEAD` без `.claude/skills/`, `skills-lock.json`, `docs/superpowers/plans/`, `.reviews/`. Спека `docs/superpowers/specs/2026-10-09-init-shell-integration-design.md`, `decisions.md` и `resolution.md` раунда 1 прочитаны как описание намеренного поведения.
- Коммиты раунда 1 (`e4f54e2`, `d4d89f4`, `70b1736`, `6ab2e86`) прочитаны целиком; кроме того, весь код `pkg/initstate`, `pkg/scanner`, изменённые части `pkg/composer`, `pkg/cleaner`, `cmd/init.go`, `cmd/list.go` и вызывающий код (`cmd/merge.go`, `cmd/revert.go`, `cmd/dedupe.go`, `cmd/status.go`, `cmd/cleanup.go`).
- `go test ./...` на хосте (тесты работают только в `t.TempDir()`) — зелёный. `task test:container` (gofmt, vet linux+windows, unit, e2e, `scripts/init_shell_test.sh` в bash и zsh) — код выхода 0.
- SEC-10 и SEC-11 подтверждены harness'ом в `.tmp/secreview2/` (отдельный модуль с `replace` на репозиторий, `initstate.Run` на `t.TempDir()`, явный `StateDir`). Настоящие `~/.kube` и `~/.kubeconfig-composer` не трогались. Harness удалён.
- SEC-12: поведение kubectx проверено по исходнику `ahmetb/kubectx` (`internal/kubeconfig/kubeconfig.go`: `Save()` делает `Reset()` — truncate + seek — и пишет в тот же дескриптор, без lock-файла).
- PR для ветки нет, поэтому шаг с обсуждением PR/BugBot пропущен.

## Проверка исправлений SEC-01..SEC-09

| ID | Итог | Как проверено |
|---|---|---|
| SEC-01 | closed | `resolvePaths` разворачивает `--dir` и каталог `--output`; дальше `Scan`, `SetBaseDir`, `ScanDirectory`, `pairKey`, соль отпечатка и исключение output работают с развёрнутыми путями, `export` печатает путь как задан. Пути из обхода и исключённый output совпадают и для output внутри каталога-ссылки (`TestRunOutputInsideSymlinkedDirIsNotASource`). При нуле источников есть предупреждение. |
| SEC-02 | closed, с оговоркой → SEC-11 | «Своё» теперь — `State.Names` прошлой сборки, префикс не учитывается, без состояния чужими считаются все пропавшие. Сценарий из первой находки (`--output ~/.kube/config` после `merge --cleanup`) даёт бэкап и предупреждение. Не закрыт путь, где прежний файл не читается: `ReadSession` с ошибкой → `sess = nil` → `LostForeign` не вызывается → файл перезаписан без копии (SEC-11). |
| SEC-03 | closed | `scanner.IsServiceFile` (`kubectx`, `*.lock`) и `kubens` в `skippedDirs` используют и отпечаток, и `IsKubeconfigFile`. Проверил другой похожий случай: кэш `gke-gcloud-auth-plugin` (`gke_gcloud_auth_plugin_cache`) кладётся рядом с файлом по умолчанию из `KUBECONFIG`, то есть после `init` в `~/.kubeconfig-composer/`, а не в `~/.kube`, и в отпечаток не попадает. |
| SEC-04 | closed | `ApplyNamespaces` переносит прежний namespace, только если он отличается от `State.SourceNamespaces` прошлой сборки; `SourceNamespaces` берётся до переноса, поэтому значение, выбранное пользователем, держится стабильно. `configsEqual` сравнивает namespace, так что правка из источника реально записывается. |
| SEC-05 | closed | `scanner.WalkKubeconfigFiles` — единственный обход для `ScanDirectory`, `FindKubeconfigFiles` и `list`; ошибкой заканчивается только корень, `SkipDir` для нечитаемого подкаталога корректно обрабатывается `filepath.Walk`. `Scan` ведёт себя так же. |
| SEC-06 | closed | `pairKey` по развёрнутым путям, файлы `init-state-<key>.json` и `init-<key>.lock`, вычисляются только в `statePaths`. |
| SEC-07 | closed | Output по умолчанию — `~/.kubeconfig-composer/kc.yaml`, его нет в обходе `merge`/`list`/`status`/`cleanup` по умолчанию. `revert --output ~/.kubeconfig-composer/kc.yaml` находит `kc.yaml.backup.*` в `backups/` (префикс по базовому имени). Явный `--output` внутри `--dir` описан в help, README и спеке как выбор пользователя. |
| SEC-08 | closed для kubectl, не закрыт для kubectx/kubens → SEC-12 | `lockKubeconfig` повторяет протокол client-go (`<path>.lock`, `O_CREATE|O_EXCL`) на том же пути, что `KUBECONFIG` (`exported`); `ModifyConfig` блокирует ровно этот путь. Под lock остаются чтение сессии, перенос, бэкап, `OutputUnchanged` и rename. kubectx и kubens этот lock не берут. |
| SEC-09 | closed | `setup:docker` пропускается без `docker`, при неуспешном `docker info` и не падает на неуспешном `docker pull`. |

## Что проверено и проблем не найдено

- **Порядок пересборки.** Отпечаток снимается до разбора источников, разбор — вне lock client-go, под lock только операции в памяти и rename. `ApplyCurrentContext` до `Dedupe`, `contextNamespaces` и `configNames` после. Состояние сохраняется только после успешной пересборки; при занятом lock client-go — ошибка, `export` печатается, состояние не меняется.
- **Формат состояния.** `Names` берутся из результата сборки, в который чужие записи не попадают, поэтому чужая запись не может стать «своей». Повреждённый или старый файл состояния → `hasPrev=false` → все пропавшие считаются чужими, то есть ошибка уходит в сторону лишнего бэкапа.
- **Lock-файлы.** `init-<key>.lock` и `kc.yaml.lock` лежат в каталоге состояния, а не в `--dir`; в отпечаток не попадают. Чужой `kc.yaml.lock` init не удаляет. Windows: `TryLock`-заглушка, `O_EXCL` работает.
- **Изменения `WalkKubeconfigFiles` для `merge`/`list`/`status`/`cleanup`.** Нечитаемый подкаталог теперь пропускается (раньше прерывал команду), `status`/`cleanup` пропускают молча (`onSkip == nil`), `merge`/`list` печатают «⚠ пропускаю». Пропуск `cache/`, `http-cache/`, `kubens/`, скрытых подкаталогов, `kubectx` и `*.lock` описан в спеке и AGENTS.md. Сортировка добавленных конфигов не трогает базу `merge`.
- **stdout.** Предупреждение `onSkip` в `ScanDirectory` идёт через `pterm` внутри `CaptureStdout` и возвращается в stderr; `Dedupe`, `OutputUnchanged`, `WriteOutputAtomic`, `backupFile` в stdout не пишут.
- **Права.** `kc.yaml`, состояние, бэкапы и lock-файлы — 0600; каталог состояния 0755, как у `merge`.
- **Документация.** README, AGENTS.md, help `init` и спека соответствуют коду, кроме утверждения спеки, что kubectx/kubens не теряют результат при пересборке (см. SEC-12).

## Findings

### SEC-10 — Пересборка не падает из-за того, что каталога `--output` ещё нет, а ошибка lock-файла не выдаётся за «lock занят»

**Суть.** В `d4d89f4` перед rename появился `lockKubeconfig(exported, …)`. Он сразу делает `os.OpenFile("<output>.lock", O_CREATE|O_EXCL)` и не создаёт каталог. Если каталога `--output` ещё нет, `OpenFile` возвращает `ENOENT`, и `rebuild` завершается ошибкой до `WriteOutputAtomic` — хотя `cleaner.WriteFileAtomic` специально делает `MkdirAll` («Недостающие каталоги создаются»), и до раунда 1 первая сборка в новый каталог работала. Сам client-go в `lockFile` тоже сначала делает `MkdirAll`.

Вторая половина: `rebuild` на любую ошибку `lockKubeconfig` печатает «⚠ `<output>.lock` занят; если kubectl не запущен, удалите его». На `ENOENT` (или `EACCES`) это неверный диагноз: удалять нечего.

**Места:**
- `pkg/initstate/clientlock.go:22` — `os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)` без `MkdirAll(filepath.Dir(lockPath))`.
- `pkg/initstate/run.go:188-192` — сообщение «занят» на любую ошибку.

**Почему не задевает конфигурацию по умолчанию.** Output по умолчанию лежит в `StateDir`, а `TryLock` создаёт `StateDir` раньше (`lock_unix.go:20`).

**Сценарий.** `eval "$(kubeconfig-composer init --output ~/.config/kc/kc.yaml)"`, каталога `~/.config/kc` нет. Каждый старт терминала: stdout пуст, код 1, в stderr «⚠ …/kc.yaml.lock занят; если kubectl не запущен, удалите его» и «не удалось создать …/kc.yaml.lock: … no such file or directory». `KUBECONFIG` не выставляется никогда.

Harness: `Output = <tmp>/newdir/kc.yaml` → `res={ExportOK:false Rebuilt:false} err=не удалось создать …/newdir/kc.yaml.lock: open …: no such file or directory log="⚠ …/newdir/kc.yaml.lock занят; если kubectl не запущен, удалите его"`.

**Почему major, а не minor.** Это регрессия, которую внёс раунд 1, и для документированного флага (`--output`, пример в help и README) фича ломается полностью и без самовосстановления; к тому же диагностика ведёт пользователя не туда. Не blocker: по умолчанию всё работает, и обход очевиден (`mkdir`).

**Как чинить.** В `lockKubeconfig` делать `os.MkdirAll(filepath.Dir(lockPath), 0o755)` до цикла, как client-go. Сообщение «занят» печатать только для таймаута на `fs.ErrExist`, остальные ошибки показывать как есть. Тест: `Run` с output в несуществующем каталоге собирает файл; тест на текст предупреждения при `ENOENT`.

### SEC-11 — Прежний output, который init не смог прочитать, не перезаписывается без бэкапа (продолжение SEC-02)

**Суть.** Защита от потери чужих записей (`LostForeign` + `backupFile`) работает, только если `ReadSession` прочитал прежний файл. Если `clientcmd.LoadFromFile` вернул ошибку (не `ErrNotExist`), `rebuild` пишет «не удалось прочитать прежний …, состояние сессии не сохранено», ставит `sess = nil` и перезаписывает файл, не сохранив копию. Init сам невалидный YAML не пишет, поэтому такой файл по определению собирал или правил не init — ровно тот случай, который SEC-02 требует сохранять.

**Места:** `pkg/initstate/run.go:195-199` (ветка `serr != nil`), `pkg/initstate/run.go:207-227` (бэкап только внутри `if sess != nil`). Тест `TestRunRebuildsWhenOutputDeletedOrCorrupt` закрепляет пересборку поверх битого файла, но бэкап не проверяет.

**Сценарий.** Пользователь вручную дописал кластер в `kc.yaml` (или его дописал `kind`/`minikube`/`aws eks update-kubeconfig`, потому что `KUBECONFIG` указывает на `kc.yaml`), потом поправил файл руками и оставил опечатку. При следующем изменении любого источника init печатает одно предупреждение и перезаписывает файл; дописанные креды пропадают, в `backups/` ничего нет.

Harness: собранный `kc.yaml` + дописанный кластер с незакрытой `{` + новый источник → `res={ExportOK:true Rebuilt:true} err=<nil> log="⚠ не удалось прочитать прежний …/kc.yaml, состояние сессии не сохранено: yaml: line 20: …" backups=[]`.

**Почему minor.** Нужны два условия сразу (битый файл и изменение источника), предупреждение в stderr есть.

**Как чинить.** При `serr != nil` и существующем файле делать `backupFile(output, …)` до записи и упоминать путь копии в предупреждении. Тест: битый output → ровно один бэкап.

### SEC-12 — Пересборка не теряет параллельную запись kubectx/kubens в kc.yaml (SEC-08 закрыт только для kubectl)

**Суть.** Исправление SEC-08 опирается на lock-файл client-go `<output>.lock`. Его берёт `kubectl config use-context`/`set-context` (`clientcmd.ModifyConfig`), но не kubectx и kubens: в `ahmetb/kubectx` `Kubeconfig.Save()` делает truncate + seek на открытом файле и пишет YAML в тот же дескриптор, без lock-файла и без rename. Сценарий (a) из SEC-08 был именно про `kubectx prod`.

**Места:** `pkg/initstate/run.go:183-239` (окно от `ReadSession` до `WriteOutputAtomic`), `pkg/initstate/clientlock.go:11-17` (комментарий корректно говорит только про kubectl). Спека в «Критериях успеха» обещает, что kubectx и kubens не теряют результат при пересборке.

**Сценарии.**
1. Пересборка прочитала сессию, в соседнем терминале выполнен `kubectx prod`, init делает rename поверх — переключение пропало.
2. init читает `kc.yaml` между truncate и записью kubectx: пустой файл `LoadFromFile` возвращает без ошибки → `current-context` пуст и молча откатывается к контексту первого источника; на частично записанном файле срабатывает ветка из SEC-11.

**Почему minor.** После раунда 1 окно — только операции в памяти, бэкап и rename (миллисекунды), пересборка идёт только при старте терминала после изменения источников, теряется лишь выбор контекста или namespace.

**Как чинить.** Lock тут не поможет, kubectx его не уважает. Перед rename перечитать `stat` output и сравнить mtime/размер с прочитанными в `ReadSession`; при расхождении повторить перенос сессии (один-два раза). Пустой прежний файл (`size == 0`) считать «сессии нет», а не «сессия пустая». Если это решено не чинить, поправить критерий успеха в спеке.
