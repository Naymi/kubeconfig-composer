---
github_issue: 7
url: https://github.com/Naymi/kubeconfig-composer/issues/7
status: null
priority: P3
blocked_by: []
milestone: null
updated_at: 2026-10-16T11:06:55Z
---

# revert picks the latest backup by mtime only, ties are arbitrary

## Problem

`findBackups` in `cmd/revert.go:124-158` ([https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/revert.go#L123-L158](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/revert.go#L123-L158)) orders backups only by modification time (`sort.Slice` with `ModTime().After`, `cmd/revert.go:152-156`). On filesystems with coarse mtime resolution, or for backups written within one mtime tick (e.g. `init`'s `-2`/`-3` backups from one rebuild), the comparison is a tie and `sort.Slice` is not stable, so which backup `revert` treats as "latest" is arbitrary. The `os.Stat` errors are also ignored there, so a backup removed between `ReadDir` and the sort would panic on a nil `FileInfo`.

## Suggested fix

Add a stable tiebreak: when mtimes are equal, compare by file name. The name embeds the `20060102-150405` timestamp and, for `init` backups (and for `merge`/`dedupe` once they share `backupFile`), the `-N` suffix — note that `-10` sorts before `-2` lexically, so parse the suffix numerically. Alternatively sort primarily by the timestamp parsed from the name and use mtime only as a fallback. Use `sort.SliceStable` or a total order, and handle the `os.Stat` error instead of discarding it.

## Origin

Found by the thermo-nuclear reviews of PR #5 (`init` command); review artifacts are in `.reviews/init-command/`, e.g. `.reviews/init-command/round-3-2026-10-16/resolution.md` (section "Вне находок"). The code is pre-existing and was outside that PR's scope, so it was left as is.
