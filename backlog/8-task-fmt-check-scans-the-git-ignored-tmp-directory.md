---
github_issue: 8
url: https://github.com/Naymi/kubeconfig-composer/issues/8
status: Todo
priority: P3
blocked_by: []
milestone: null
updated_at: 2026-10-16T11:07:03Z
---

# task fmt:check scans the git-ignored .tmp/ directory

## Problem

`fmt:check` in `Taskfile.yml:144-147` ([https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/Taskfile.yml#L144-L147](https://github.com/Naymi/kubeconfig-composer/blob/e00b62340ed1bc7934cffb853027eaa8e4f4ff4a/Taskfile.yml#L144-L147)) runs `gofmt -l .`, which descends into the git-ignored `.tmp/` scratch directory. Any unformatted scratch `.go` file there fails `task fmt:check`, therefore `task check` and the pre-commit hook that runs it, even though the file is not part of the repository. This happened during review of PR #5: a reviewer harness at `.tmp/secreview/main.go` broke `task check` until it was moved out. `fmt` (`Taskfile.yml:139-142`, `gofmt -w .`) has the same reach and would rewrite scratch files.

## Suggested fix

Limit both tasks to tracked Go files, e.g. `gofmt -l $(git ls-files '*.go')` for `fmt:check` and `gofmt -w $(git ls-files '*.go')` for `fmt`, or an equivalent that excludes `.tmp/`.

## Origin

Found by the thermo-nuclear reviews of PR #5 (`init` command); review artifacts are in `.reviews/init-command/`, e.g. `.reviews/init-command/round-1-2026-10-16/reports/worker-d.md` (out-of-list notes). The code is pre-existing and was outside that PR's scope, so it was left as is.
