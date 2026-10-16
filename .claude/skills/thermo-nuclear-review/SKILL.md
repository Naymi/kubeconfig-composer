---
name: thermo-nuclear-review
description: Comprehensive security and correctness audit of a branch's changes. Use for thermo nuclear, thermonuclear, or deep review requests, or branch/PR diff audits focused on bugs, breaking changes, security issues, devex regressions, and feature-gate leaks.
disable-model-invocation: true
---

# Thermo Nuclear Review

**REQUIRED SUB-SKILL:** Use thermo-nuclear-review-upstream.
That skill is the full audit prompt, vendored verbatim from its upstream repo. Follow
its instructions completely as the review prompt — this wrapper only adds what's
local to this repo and does not restate or modify the upstream content, so upstream
updates sync there without touching this file.

# Persisting the Review
**REQUIRED SUB-SKILL:** Use persisting-review-artifacts.
Before you finish, write this review's full findings to `.reviews/<slug>/review-security-<date>.md` in the current worktree, and run that skill's before-commit signal step. A review that only exists in this response is invisible to later retrospective analysis.

# Report contract

persisting-review-artifacts owns the schema: the YAML frontmatter, the computed
`approval`, the `findings` counters, finding ids, the three severities and the ledger
table. This section fills in only what belongs to this review.

**Finding id prefix: `SEC`** — `SEC-01`, `SEC-02`, ….

**Mapping the upstream priorities onto the three severities.** The upstream prompt speaks
in terms of high, medium-to-high and low priority, and warns that misreporting priority is
exactly how a reviewer loses the author's trust. Map it the same way every time, so the
severity in the ledger means one thing across reports:

| Upstream priority | Severity |
|---|---|
| high — a vulnerability, a break in existing functionality or devex, a feature-gate leak that is live | `blocker` |
| medium — a real defect that does not by itself put the branch beyond merging | `major` |
| low | `minor` |

**The upstream's restraint rules run before the mapping, not after it.** An intended
breakage whose scope is well constrained is not reported at all ("Intended Breakage
Guidelines"), so it has no id and no severity; filing it as a `minor` to stay on the safe
side is precisely the over-reporting the same prompt warns about. A finding you take from
the PR/MR discussion gets an id like any other, and its row says where it came from.

**This report does not carry `codebase-health`.** Those axes are derived from the code
quality prompt, and this review is not asked to judge any of them — an axis scored by a
reviewer who never looked at it is a number with nothing behind it. A structural problem
that genuinely blocks correctness is a `blocker` finding here, on its own merits.

| Statement | What's really going on |
|---|---|
| "High-ish, so I'll write `major` to be safe" | The mapping isn't a dial. High is `blocker`; hedging the severity is the misreporting the upstream prompt calls out by name. |
| An intended, well-constrained breakage filed as `minor` | It isn't reported at all. It has no row and no id. |
| `codebase-health` added anyway, "since the schema supports it" | Not in this review. The axes come from a prompt this reviewer didn't run. |
