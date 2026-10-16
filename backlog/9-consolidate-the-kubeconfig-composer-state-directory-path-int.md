---
github_issue: 9
url: https://github.com/Naymi/kubeconfig-composer/issues/9
status: null
priority: P3
blocked_by: []
milestone: null
updated_at: 2026-10-16T11:07:09Z
---

# Consolidate the ~/.kubeconfig-composer state directory path into one helper

## Problem

The state directory path `~/.kubeconfig-composer` is built by hand as `filepath.Join(os.Getenv("HOME"), ".kubeconfig-composer", ...)` in five places in `cmd/`:

- `cmd/merge.go:148` (backups) — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/merge.go#L148](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/merge.go#L148)
- `cmd/merge.go:236` (trash) — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/merge.go#L236](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/merge.go#L236)
- `cmd/dedupe.go:122` (backups) — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/dedupe.go#L122](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/dedupe.go#L122)
- `cmd/revert.go:126` (backups) — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/revert.go#L126](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/revert.go#L126)
- `cmd/init.go:64-66` (`initStateDir()`) — [https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/init.go#L62-L66](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/cmd/init.go#L62-L66)

Any change to the location (or to how `HOME` is resolved) has to be made in all five, and a missed one splits backups between two directories that `revert` would not both see.

## Suggested fix

Have a single helper (e.g. promote `initStateDir()` to a shared `stateDir()` in `cmd/`, with `backupsDir()`/`trashDir()` built from it) and use it in `merge`, `dedupe`, `revert` and `init`.

## Origin

Found by the thermo-nuclear reviews of PR #5 (`init` command); review artifacts are in `.reviews/init-command/`, e.g. `.reviews/init-command/round-1-2026-10-16/reports/worker-d.md` (out-of-list notes). The code is pre-existing and was outside that PR's scope, so it was left as is.
