---
github_issue: 6
url: https://github.com/Naymi/kubeconfig-composer/issues/6
status: Todo
priority: P2
blocked_by: []
milestone: null
updated_at: 2026-10-16T11:06:47Z
---

# merge and dedupe backups can overwrite each other within the same second

## Problem

`merge` and `dedupe` name backups `<name>.backup.<20060102-150405>` (one-second resolution) and write them through `copyFile`, which calls `os.WriteFile` and silently replaces an existing file:

- `cmd/merge.go:145` builds the timestamp, `cmd/merge.go:153-154` the backup path, `cmd/merge.go:189` copies — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/merge.go#L145-L189](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/merge.go#L145-L189)
- `cmd/dedupe.go:121-131` (`backupKubeconfig`): same name format at `:126-127`, copy at `:128` — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/dedupe.go#L121-L131](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/dedupe.go#L121-L131)
- `cmd/merge.go:294-299` (`copyFile`): `os.WriteFile(dst, data, 0600)` truncates an existing destination

Two runs against the same output within one second (e.g. `merge -y` followed by `dedupe -y` in a script) produce the same backup name, and the second run overwrites the first backup. The pre-merge state is then lost, and `revert` can no longer restore it.

## Suggested fix

`init` already solved this: `backupFile` in `pkg/initstate/run.go:377` ([https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/pkg/initstate/run.go#L368-L400](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/pkg/initstate/run.go#L368-L400)) opens the destination with `O_CREATE|O_EXCL` and, if the name is taken, retries with `-2`, `-3`, … (up to `maxBackupSuffix`). Move that helper somewhere `cmd/` can share (or export it) and use it in `merge` and `dedupe` instead of `copyFile`, so no backup is ever overwritten. Add a test with two backups of the same file in the same second.

## Origin

Found by the thermo-nuclear reviews of PR #5 (`init` command); review artifacts are in `.reviews/init-command/`, e.g. `.reviews/init-command/round-3-2026-10-16/resolution.md` (section "Вне находок") and `round-3-2026-10-16/plan.md` (SEC-13 row). The code is pre-existing and was outside that PR's scope, so it was left as is.
