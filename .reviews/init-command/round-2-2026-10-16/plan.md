---
skills: [running-review-fix-rounds, persisting-review-artifacts]
skills-repo: naymi/skills @ 621a953
agent: claude-opus-5-5 (coordinator)
date: 2026-10-16
round: 2
findings:
  taken: [SEC-10, SEC-11, SEC-12]
  excluded: {}
---

# Round 2 — init-command, review-security-2026-10-16-2

Открытые находки: SEC-10 (major), SEC-11, SEC-12 (minor). Из раунда 1 ничего не
перенесено (9 из 9 `fixed`).

## id → rule → sites → worker → wave

| ID | Rule | Sites (search) | Worker | Wave |
|---|---|---|---|---|
| SEC-10 | Пересборка не падает из-за того, что каталога `--output` ещё нет, а ошибка lock-файла не выдаётся за «lock занят» | `pkg/initstate/clientlock.go:22`, `run.go:188-192`. Поиск по ресурсу «создание файлов рядом с output/в StateDir»: `rg -n 'os.OpenFile\|os.WriteFile\|os.Create' pkg/initstate` — `backupFile` (`MkdirAll` есть), `lock_unix.go` `TryLock` (`MkdirAll` есть), `cleaner.WriteFileAtomic` (`MkdirAll` есть), `clientlock.go` — нет | E | 1 |
| SEC-11 | Прежний output, который init не смог прочитать, не перезаписывается без бэкапа | `run.go:195-199` (ветка `serr != nil`), `run.go:207-227` (бэкап только при `sess != nil`). Других мест, где init перезаписывает существующий файл, нет: `rg -n 'WriteOutputAtomic\|WriteFileAtomic' pkg/initstate` → `rebuild` и `SaveState` (свой файл состояния) | E | 1 |
| SEC-12 | Пересборка не теряет параллельную запись kubectx/kubens в kc.yaml | `run.go:183-239` — окно `ReadSession` → rename; `carry.go` `ReadSession` (пустой файл = пустая сессия) | E | 1 |

Already compliant: `backupFile` и `TryLock` создают каталоги до записи — образец для SEC-10.

## Принятые решения

- **SEC-10.** `lockKubeconfig`: `os.MkdirAll(filepath.Dir(lockPath), 0o755)` до цикла (как `clientcmd.lockFile`). Тип ошибки различается: таймаут на занятом lock — отдельная ошибка-значение (`errKubeconfigLockBusy`, проверка через `errors.Is`); предупреждение «занят; если kubectl не запущен, удалите его» — только для неё. Прочие ошибки — как есть, через общий путь ошибки `Run`.
- **SEC-11.** В `rebuild`: если `ReadSession` вернул ошибку, а файл существует — `backupFile` до записи; предупреждение одно и с путём копии (или с причиной, почему копия не сделана). Сессии нет (`sess = nil`), остальное как сейчас.
- **SEC-12.** Оптимистичная перепроверка: под lock client-go запомнить `(mtime, size)` output перед `ReadSession`; после переноса сессии и непосредственно перед `OutputUnchanged`/`WriteOutputAtomic` — `stat` ещё раз; при расхождении — повторить шаги от `ReadSession` (не более 3 попыток всего; после третьей — писать с последним прочитанным и не предупреждать). Перенос сессии должен быть повторяемым: Composer-результат перед повтором не должен нести current-context/namespace предыдущей попытки — сохраняй собранный из источников конфиг (после `Merge`) и применяй сессию к его глубокой копии (`DeepCopy()` у `clientcmdapi.Config` есть) на каждой попытке, `Dedupe` — на копии. Пустой output (`size == 0`) при чтении — вероятно, середина записи kubectx (truncate + write): подождать 10 мс и перечитать, не более тех же 3 попыток; если пустой и после них — сессии нет. Окно между последним `stat` и rename остаётся: kubectx/kubens не берут никакого lock, закрыть его нельзя — это `wont-fix` для остатка (decisions.md), критерий успеха в спеке переписывается под реальную гарантию.

## file × worker

| File | E |
|---|---|
| pkg/initstate/clientlock.go, clientlock_test.go | ✔ |
| pkg/initstate/run.go, run_test.go, run_unix_test.go | ✔ |
| pkg/initstate/carry.go, carry_test.go | ✔ |
| docs/superpowers/specs/2026-10-09-init-shell-integration-design.md, AGENTS.md, README.md (только места про эти три правила) | ✔ |

Один воркер, одна волна: все три правила живут в `rebuild`.

## Excluded sites

Нет.

## Diff review before merging

Координатор: повторяемость попыток SEC-12 (нет ли утечки состояния предыдущей попытки в результат — ключевой риск), что `SourceNamespaces`/`Names` для State берутся из последней попытки, что предупреждения не дублируются на попытках, что SEC-11 бэкап делается ровно один раз, что сообщение «занят» не печатается для ENOENT/EACCES.
