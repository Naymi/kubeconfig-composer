---
name: thermo-nuclear-code-quality-review
description: Run an extremely strict maintainability review for abstraction quality, giant files, and spaghetti-condition growth. Use for a thermo-nuclear code quality review, thermonuclear review, deep code quality audit, or especially harsh maintainability review.
disable-model-invocation: true
---

# Thermo-Nuclear Code Quality Review

**REQUIRED SUB-SKILL:** Use thermo-nuclear-code-quality-review-upstream.
That skill is the full audit prompt, vendored verbatim from its upstream repo. Follow
its instructions completely as the review prompt — this wrapper only adds what's
local to this repo and does not restate or modify the upstream content, so upstream
updates sync there without touching this file.

# Persisting the Review
**REQUIRED SUB-SKILL:** Use persisting-review-artifacts.
Before you finish, write this review's full findings to `.reviews/<slug>/review-quality-<date>.md` in the current worktree, and run that skill's before-commit signal step. A review that only exists in this response is invisible to later retrospective analysis.

# Report contract

persisting-review-artifacts owns the schema: the YAML frontmatter, the computed
`approval`, the `findings` counters, finding ids, the three severities, the ledger table
and the `codebase-health` mechanism. This section fills in only what belongs to this
review.

**Finding id prefix: `CQ`** — `CQ-01`, `CQ-02`, ….

**Severity comes from the upstream Approval Bar, not from a second scale.** Every item the
upstream prompt lists under "Treat these as presumptive blockers" is a `blocker` when it
fires — that list is what this review's approval bar already is, and restating it in other
words here would give the same branch two different answers depending on which sentence
the reviewer read last. The upstream grants one escape, "unless the author can justify them
clearly": taking it means writing the justification next to the finding, not lowering the
severity quietly. Of what remains, a finding is `major` when it makes the code meaningfully
harder to change and `minor` when it is worth fixing and nothing more.

**This report carries `codebase-health`,** on these six axes, written in this order:

| Axis | What it judges |
|---|---|
| `complexity` | how many concepts, branches and layers a reader must hold to change this next |
| `coupling` | how far a change here reaches — feature logic in a shared path, logic in the wrong layer, a leak across a boundary |
| `abstraction` | whether the abstractions earn their keep, or are wrappers, magic, and cast-heavy contracts |
| `duplication` | the eighth copy instead of the canonical helper |
| `brittleness` | how easily this breaks unnoticed — ad-hoc special cases, non-atomic updates |
| `debt` | a compromise knowingly taken on as temporary |

```yaml
approval: approved
findings: {blockers: 0, majors: 0, minors: 3}
codebase-health:
  overall: degraded
  complexity: much-worse
  coupling: worse
  abstraction: same
  duplication: worse
  brittleness: same
  debt: better
```

The pair above is the reason this block exists: nothing in the branch holds the merge, and
the branch still leaves the codebase harder to work in than it found it. Today that
observation has nowhere to go, so it is either inflated into a finding that isn't one or
dropped entirely.

Both failures have the same cure, and it is a rule rather than a preference: **anything you
name under an axis that can be pinned to a site is filed as a finding and cited there by
id.** What cannot be pinned — a claim about the shape of the whole increment — stays on the
axis and says, in that entry, why it doesn't localize. An axis entry that is neither is
grumbling, and it is the reason scores like this stop being read.

| Statement | What's really going on |
|---|---|
| "It's a presumptive blocker, but only a small one — `major`" | The upstream list has no small entries. It's a `blocker`, or the author's justification is written down next to it. |
| A `codebase-health` axis argues a point that no finding mentions | Either it pins to a site — then it's a finding with an id — or the entry says why it doesn't. |
| `overall` written by hand because the axes "don't feel like" that | `overall` is computed, by the rule `persisting-review-artifacts` gives. Change an axis and defend that, or leave the computation alone. |
| The branch is clean, so the block is skipped | `same` on all six with `overall: unchanged` is the result. A missing block is indistinguishable from a forgotten one. |
