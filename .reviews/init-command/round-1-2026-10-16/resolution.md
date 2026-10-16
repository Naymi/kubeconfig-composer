---
skills: [running-review-fix-rounds, persisting-review-artifacts]
skills-repo: naymi/skills @ 621a953
agent: claude-opus-5-5 (coordinator)
date: 2026-10-16
round: 1
approval: approved
findings: {blockers: 0, majors: 0, minors: 0}
resolutions:
  SEC-01: {resolution: fixed, closed-by: "tests TestRunSymlinkedDirIsWalked, TestRunOutputInsideSymlinkedDirIsNotASource, TestRunEmptyDirWarnsAboutZeroSources; shape: resolvePaths is the only path normalisation in Run"}
  SEC-02: {resolution: fixed, closed-by: "tests TestRunBacksUpOutputItDidNotBuild, TestRunBacksUpForeignEntryWithPrefix, TestRunReportsLostForeignEntriesAndBacksUp, TestLostForeign; shape: ownership comes from State.Names, prefix check removed"}
  SEC-03: {resolution: fixed, closed-by: "tests TestScanIgnoresContextSwitcherState, TestIsServiceFile, TestIsKubeconfigFileRejectsServiceFiles, TestWalkKubeconfigFilesSkipsServiceEntries; shape: single predicate scanner.IsServiceFile + kubens in skippedDirs"}
  SEC-04: {resolution: fixed, closed-by: "tests TestRunSourceNamespaceChangeReachesOutput, TestRunUserNamespaceSurvivesSourceChange, TestRunWithoutStateCarriesNamespace, TestApplyNamespacesSourceWinsWhenUserDidNotChangeIt"}
  SEC-05: {resolution: fixed, closed-by: "shape: scanner.WalkKubeconfigFiles is the only source walk (FindKubeconfigFiles, ScanDirectory, list); tests TestWalkKubeconfigFilesReportsUnreadableSubdir, TestFindKubeconfigFilesSkipsUnreadableSubdir, TestScanDirectorySkipsUnreadableSubdir", note: "unix tests skip under root, so they run in task check on the host, not in the container"}
  SEC-06: {resolution: fixed, closed-by: "tests TestRunStateIsPerPair, TestRunBusyLockOfOtherPairDoesNotBlock, TestPairKeyDependsOnBothPaths; shape: statePaths/pairKey single entry point"}
  SEC-07: {resolution: fixed, closed-by: "e2e TestInitOutputIsNotAMergeSource (merge -y --cleanup after init); shape: default output ~/.kubeconfig-composer/kc.yaml", note: "an explicit --output inside --dir still becomes a merge source; documented in init --help, README and spec as user choice"}
  SEC-08: {resolution: fixed, closed-by: "tests TestRunWaitsForKubectlLock, TestRunBusyKubectlLockLeavesOutputAndState, TestRunRemovesKubectlLock, TestLockKubeconfig*; shape: rebuild reads session and renames only under client-go <output>.lock"}
  SEC-09: {resolution: fixed, closed-by: "shape: setup:docker checks docker info and tolerates failed pull", note: "not covered by a test (project has no Taskfile tests); verified by hand without docker in PATH and with DOCKER_HOST=unix:///nonexistent, see reports/worker-d.md"}
---

# Resolution — round 1

9 of 9 findings accounted for (taken: 9, carried in: 0). All 9 `fixed`.

Коммиты в `feat/init-command`:
- `e4f54e2` — воркер A (SEC-03, SEC-05: три обхода, `IsServiceFile`);
- `d4d89f4` — воркер C (SEC-01, 02, 04, 06, 08);
- `70b1736` — воркер D (SEC-07, SEC-09, SEC-05: `WalkKubeconfigFiles`, `list`);
- `6ab2e86` — воркер D, документация (README, AGENTS.md, спека).

Проверки после волны 2 (из отчёта D и повторно координатором после волны 1):
`task test:container` — 0, `task check` — 0.

## Diff review

- **A.** Предикат служебных файлов один (`scanner.IsServiceFile`), его используют `IsKubeconfigFile` и `initstate.Scan`. Правило ошибок обхода было скопировано в два места — это устранено в волне 2 выносом `WalkKubeconfigFiles`.
- **C.** Ключ пары вычисляется в одном месте (`pairKey` → `statePaths`). Отступление от брифа: lock client-go берётся по неразвёрнутому пути (`exported`), а не по развёрнутому output, — верно, kubectl блокирует путь из `KUBECONFIG`; принято. Опасение C про `<output>.lock` в отпечатке закрыто у A (`*.lock` в `IsServiceFile`). Состояние старого формата (`init-state.json`) не читается — init не выходил в релиз, миграция не нужна (decisions.md).
- **D.** Три копии правила обхода сведены в `WalkKubeconfigFiles`, комментарии сведены к одному. `initStateDir()` — пятое место, где собирается `~/.kubeconfig-composer` (`cmd/merge.go:148,236`, `cmd/revert.go:126`, `cmd/dedupe.go:122`); это дублирование существовало до ветки и к находкам раунда не относится — отмечено здесь, не исправлялось.
- **Ложные комментарии.** Проверены: `LostForeign`, `ApplyNamespaces`, `State`, `Scan`, `skippedDirs`, help `init`, README/AGENTS/спека обновлены D.

## Вне находок

- `fmt:check` (`gofmt -l .`) заходит в `.tmp/`; scratch-harness ревьюера `.tmp/secreview/` ронял `task check`. Harness удалён координатором; сама задача не менялась (не находка ревью).
- `docs/superpowers/plans/2026-10-09-init-shell-integration.md` — исторический план, упоминает старые пути; не обновляется намеренно.
