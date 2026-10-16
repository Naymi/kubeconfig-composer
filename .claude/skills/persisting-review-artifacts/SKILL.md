---
name: persisting-review-artifacts
description: Use when a code review or fix round produces findings, a coordinator plan, worker briefs, or coordinator decisions — owns where those documents are written on disk, the approval decision and per-finding ledger their frontmatter must carry, and how to flag them before they're committed, so a later retrospective analysis has something to read.
---

# Review and fix-round artifacts on disk

## Overview

The plan, the briefs, and the coordinator's decisions usually live in chat or in
worker prompts — if a round ends without an explicit step to write them down, all
of that material exists only in the conversation and is lost to context compaction
or to the end of the session, even though a retrospective needs it.

Writing the files down is half of it. The other half is that they have to say the same
things in the same places: a review whose decision is a sentence in a summary paragraph
cannot be compared with the next review, and a finding with no name cannot be followed
into the round that was supposed to fix it. This skill owns both — the paths, and the
shape of what goes in them.

## Where to write

The root is `.reviews/` in the **current worktree** (the same one the round or review
runs in) — those files get committed to that worktree's branch and reach the main
history the usual way, along with the branch merge. Don't switch to the main checkout
for this commit, and don't create a separate branch for the artifacts.

```
.reviews/<slug>/
  review-<kind>-<YYYY-MM-DD>.md     # review report, kind = security | quality | ...
  round-<N>-<YYYY-MM-DD>/
    harness.md                       # which skills and which skills-repo commit ran the round
    plan.md                          # coordinator plan
    briefs/worker-<k>.md             # worker briefs
    reports/worker-<k>.md            # worker reports
    decisions.md                     # coordinator decisions: reason, destination (wave/round)
    resolution.md                    # what became of every finding, and the recomputed approval
```

- `slug` — kebab-case of the branch name (minus `feature/`, `fix/`, and the like) or
  of the PR/issue title; pick it once at the start of the review or round — and before
  picking, check `ls .reviews/` for an existing directory under that slug and reuse it
  rather than starting a second one. It stays unchanged from then on, so the review and
  every `round-N` under one slug line up with each other.
- `N` — the number of existing `round-*` directories under this `slug`
  (`ls .reviews/<slug>/`), plus one. Never reuse a number, even if the previous round
  is considered a failure: a failed round is material for the retrospective too.
- `<YYYY-MM-DD>` — the date the round or review started, fixed once when the directory
  is created. If the round stretches across several days, the date doesn't move: the
  directory name stays as it was after the first file.
- `<kind>` — which review skill wrote the report (`security` for thermo-nuclear-review,
  `quality` for thermo-nuclear-code-quality-review, and so on); different `kind` values
  never collide on the same day.
- Several reports of the same `<kind>` on one day — append `-2`, `-3` to the date in
  the filename; never overwrite the previous one.

Write the file as soon as the document is ready — not at the end of the session and not
"if there's time left." A report, plan, or brief that existed only in the conversation
is lost to compaction and cannot be reconstructed.

If the user asks you not to write `plan.md`/`decisions.md` for a particular round
(e.g. "this is a trivial fix, no need for extra files") — don't silently agree, and
don't silently write the files anyway. Ask explicitly whether they mean skipping the
artifacts for this round or dropping the step altogether, and act on the answer.

## Every artifact opens with YAML frontmatter

The report, `harness.md`, `plan.md` and `resolution.md` each open with a `---`-delimited
YAML block. That block is the machine-readable layer: `rg '^approval:' .reviews/` answers
"what did each review decide" without opening a file, and a retrospective follows one
finding from the report through the plan into its resolution by joining on ids instead of
by recognising sentences.

**The frontmatter is the single source of truth.** Anything the body repeats — a summary
table, a heading — is reproduced from it. The two disagreeing is a defect in the document:
the reader has no way to tell which half is the typo.

**Field values are fixed tokens, and they are the tokens written here** — `approved`,
`changes-required`, `blocker`, `fixed`. The prose around them may be in any language; the
values may not be translated, rephrased, or capitalised differently. A value that reads
naturally but doesn't match is invisible to every reader that isn't a human with the file
already open, which is the whole population this block exists for.

Four fields open every one of the four files:

```yaml
skills: [<the skills involved>]
skills-repo: <skills repository> @ <short commit>
agent: <the model or agent running the round>
date: <YYYY-MM-DD>
```

Take the commit from your clone of the skills repository
(`git -C <clone> rev-parse --short HEAD`). Skills are installed into projects as copies, so
the round's date won't tell you which version ran: without this line nobody can say whether
the round happened before a skill was changed or after — and that means nobody can check
whether the change worked.

`harness.md` carries those four fields and nothing else.

## The review report

### `approval` — the decision, and it is computed

```yaml
---
skills: [thermo-nuclear-code-quality-review, persisting-review-artifacts]
skills-repo: naymi/skills @ e4140c0
agent: <model or agent>
date: 2026-09-03
approval: changes-required
findings: {blockers: 2, majors: 1, minors: 4}
---
```

Which of the four values `approval` takes is not a judgment call:

| Condition | `approval` |
|---|---|
| any open `blocker` | `changes-required` |
| else any open `major` | `changes-required` |
| else | `approved` |
| the review could not be carried out at all | `inconclusive` |

`approved` with minor findings listed under it is the normal, correct result: minors do
not hold a merge, and a review that finds only minors has not failed to do its job.

`inconclusive` is for a review that could not be carried out — the branch doesn't build,
the diff is unreadable, the scope isn't what the request described. Its report opens with
a `## Why the review could not conclude` section naming what stopped it. Without this
value an abandoned review is indistinguishable from a clean one, and silence gets read as
an approval.

The `findings` counters are the cross-check against the ledger below, not decoration. If
the counters and the table disagree, a finding went missing between them — and that is
precisely the finding nobody will fix.

### Every finding carries an id

`<PREFIX>-<NN>` — `SEC-01`, `CQ-07`. Each review skill declares its own prefix in its own
wrapper. Ids are unique **within a slug**, are never renumbered, and are never reused: a
second report of the same kind under the same slug continues the numbering rather than
starting again at `01`.

The report, the plan and the resolution are three documents written at three different
moments. Without ids there is nothing to join them on, and "what happened to this finding"
has no subject. Ids are also what turns "the finding came back in the next round" into a
comparison of two sets rather than an act of recognition.

### Three severities

| Severity | What it means |
|---|---|
| `blocker` | merging is wrong while it stands |
| `major` | a real defect; it must be closed, and it moves to a later round only by an explicit decision naming that round |
| `minor` | worth fixing; it does not hold the merge |

### The ledger

Directly under the report's heading, one row per finding:

| ID | Severity | Rule | First site | Why this severity |
|---|---|---|---|---|
| CQ-01 | blocker | Feature logic never lands in a general-purpose adapter | `store/adapter.ts:12` | ... |

**Rule** is the invariant in one sentence with no filenames in it — the same shape
`running-review-fix-rounds` requires before a finding can go out to a worker. Writing it
here means the fix round inherits it instead of deriving it a second time, from a heading
that was never meant to carry it.

The table indexes the analysis, it does not replace it: every finding keeps its own
`### CQ-01 — <rule>` section below, with the reasoning, the sites and the evidence.

**The ledger has no status column.** At the moment the report is written nothing has been
fixed yet, and the report is not rewritten afterwards — a retrospective reads these files
in chronological order, and a document edited after the fact destroys the order it is read
in. What became of a finding lives in `resolution.md`.

### `codebase-health` — a second axis, and it never moves `approval`

Only reports whose review skill declares this block carry it; that skill's wrapper says so
and names its axes. `approval` is computed from the finding counters alone, in every kind
of review.

```yaml
codebase-health:
  overall: degraded
  first-axis: much-worse
  second-axis: same
```

(the axis names above are placeholders; the review skill's wrapper names the real ones and
fixes their order)

Each axis takes one of five values, and each value is anchored to something observable
rather than to an impression:

| Value | What puts an axis here | Weight |
|---|---|---|
| `much-worse` | this axis carries a `blocker` finding, or a presumptive-blocker item of the review's own prompt fired on it | −2 |
| `worse` | findings below `blocker`, or a concern real enough to state and too diffuse to pin to a site | −1 |
| `same` | nothing | 0 |
| `better` | something named was deleted: a branch, a mode, a wrapper, a copy | +1 |
| `much-better` | a whole layer or concept disappeared | +2 |

`overall` is the sign of the sum and never a further judgment: sum ≤ −1 → `degraded`,
sum = 0 → `unchanged`, sum ≥ +1 → `improved`.

`overall: degraded` alongside `approval: approved` is a legitimate and expected pair — it
is the case this block exists for: nothing individually holds the merge, and the increment
still leaves the codebase worse than it found it. It is **not** a way to hold a merge. If
something has to hold one, that is a `blocker` finding and it gets filed as one.

In the body, a `## Codebase health` section gives every axis one entry: either the finding
ids behind it, or — where the concern is about the shape of the whole increment — the
reason it doesn't localize to a site.

## The round's resolution

The artifacts above record the intent: what you set out to fix and how. The resolution is a
separate document, and without it a retrospective cannot tell "the rule wasn't followed"
from "it was followed, and it didn't help."

- `reports/worker-<k>.md` — the worker's report as a file, not a message in chat. The
  worker writes it in the format its brief specifies, as soon as the work is done.
- `resolution.md` — the coordinator's summary, written during the diff review before
  merging.

```yaml
---
skills: [running-review-fix-rounds, persisting-review-artifacts]
skills-repo: naymi/skills @ e4140c0
agent: <model or agent>
date: 2026-09-11
round: 2
approval: changes-required
findings: {blockers: 1, majors: 0, minors: 4}
resolutions:
  CQ-01: {resolution: fixed, closed-by: "test session_refresh_leaves_no_trace"}
  CQ-02: {resolution: deferred-to-round-3, decided-in: decisions.md}
  CQ-03: {resolution: withdrawn, closed-by: "three lines, see body"}
---
```

`approval` is recomputed here by the same table as in the report, over what is still open
after this round. Read down a slug's `resolution.md` files and the trajectory is the whole
story: `changes-required` with two blockers, then one, then `approved`.

Every resolution is one of five:

| Resolution | What it asserts |
|---|---|
| `fixed` | the invariant is closed at every known site; `closed-by` names the test or the shape of the code that closes it |
| `not-closed` | it was attempted and it did not work; nobody routed it anywhere |
| `deferred-to-round-<N>` | real, and deliberately later; `N` is named, and the decision is in `decisions.md` |
| `wont-fix` | real, and deliberately never |
| `withdrawn` | it turned out not to be a finding at all |

Any row may carry a `note` alongside its resolution; `fixed` additionally requires
`closed-by`, and `deferred-to-round-<N>` requires `decided-in`.

**A finding closed at some of its sites is not `fixed`.** Two sites out of three is the
state that reads like success and behaves like a finding still in the codebase — and it is
the one this ledger exists to stop from passing quietly. If the remaining sites were
deliberately routed onward, the resolution is `deferred-to-round-<N>` and the `note` says
what this round did close:

```yaml
  SEC-01:
    resolution: deferred-to-round-2
    decided-in: decisions.md
    note: "write.ts and replicate.ts closed by store/rollback.test.ts; store/legacy.ts routed on"
```

If they were not routed anywhere, it is `not-closed`, and the plan of the next round is
where that gets an address.

`not-closed` and `deferred-to-round-<N>` are not two words for one thing: one is a failure
nobody has taken responsibility for, the other is somebody's decision with an address. A
retrospective that cannot tell them apart cannot tell a rule that was ignored from a rule
that is wrong.

`withdrawn` carries the three-line analysis
(`running-review-fix-rounds#a-candidate-is-cleared-by-analysis-not-by-a-verdict`): where
the resource is touched, where the path can fail, where it is put back. Without those three
lines it is a bare verdict, and a bare verdict clears nothing.

`wont-fix` on a `blocker` needs a named human's yes, recorded next to it. An agent does not
dismiss a blocker on its own account.

### Every finding is accounted for exactly once

The set of ids in `resolution.md` equals the set of ids in the review report, plus every id
carried in from an earlier round. Write the count out — "12 of 12" — the same way the
counters in the frontmatter are written out.

A finding that quietly fails to appear is not a tidy omission. It is the finding that comes
back in round N+1, and the round that dropped it is the one nobody will think to look at.

"Not closed" is a legitimate outcome and valuable material. An empty table, a missing id,
or a missing file is not an outcome — it's work thrown away.

## Before the commit — an explicit signal

These files must not slip into git silently alongside code changes.

1. Before closing the round (or right after the review report), run
   `git status --porcelain .reviews/<slug>/` and show the list to the user in your
   reply — this step is never skipped, even when the set of files seems obvious, and
   even when the user has already pushed for a commit in general.
2. Commit `.reviews/…` as its own commit with a clear message
   (`chore(review): <slug> round <N> artifacts`), never together with the fix commit —
   otherwise the PR reviewer gets code and meta-documents in one diff.
3. Don't use `git add -A` / `git add .` for this commit — add the `.reviews/<slug>/...`
   path explicitly; that also keeps unrelated uncommitted worktree state out of the
   commit.
4. Only create the commit with the user's confirmation, like any other commit. Pushing
   is a separate decision of theirs.
5. Don't delete the worktree or switch its branch (see
   superpowers:finishing-a-development-branch) until the branch holding `.reviews/` is
   merged or at least pushed. Deleting a worktree whose branch is unmerged wipes the
   code and the artifacts together — the commit alone doesn't make them safe; only a
   merge or a push does.

## Red flags

| Statement / action | What's really going on |
|---|---|
| The decision is a sentence in the summary paragraph | Then it can't be grepped and can't be compared with the next review. It is a frontmatter field with one of four fixed values. |
| `approval: approved` with a non-zero `blockers` count | The field is computed, not chosen. One of the two is wrong, and the report currently contradicts its own ledger. |
| The body's summary table and the frontmatter disagree | The frontmatter is the source of truth and the body is reproduced from it. The reader can't tell which half is the typo. |
| The value is written out in the language of the surrounding text | The tokens are fixed. A translated value is invisible to everything except a human already reading the file. |
| A finding with no id — or ids restarted at `01` in a second report under the same slug | Nothing downstream can name it. The plan and the resolution join on ids. |
| The review stalled, so the report says `approved` with a caveat | That is `inconclusive`, plus the section saying what stopped it. A caveat in prose reads as an approval. |
| `codebase-health` used to hold a merge | It never moves `approval`. Something that has to hold a merge is a `blocker` finding. |
| A `withdrawn` finding with no three lines behind it | A bare verdict. See running-review-fix-rounds. |
| `wont-fix` on a `blocker`, decided by the agent | A blocker is dismissed only by a named human, recorded next to it. |
| `resolution.md` lists fewer ids than the report | The missing one is the finding that comes back next round. Every id appears exactly once, and the count is written out. |
| The plan or brief exists only in a coordinator message | The retrospective will never see it. Write the file. |
| The worktree is deleted before the `.reviews/` branch is merged or pushed | The artifacts die with the branch. Delete the worktree only after a merge or a push. |
| `.reviews/` committed together with the code fix | Meta-documents mixed into the code in the PR diff. Separate commit. |
| `.reviews/` committed without showing the file list to the user | The signal was skipped — step 1 is mandatory, even under "just commit it already." |
| "The user is in a hurry, I'll show the file list later" | Step 1 runs before the commit, not after. Their hurry doesn't cancel the signal. |
| The user asks to skip the plan/decisions — you silently agreed or silently ignored them | Neither. Ask explicitly what they're dropping. |
| The round is merged and `resolution.md` was never written | The outcome is lost: the next retrospective can't tell an ineffective rule from an unfollowed one. Write it during the diff review. |
| The worker's report stayed a message in chat | It won't survive context compaction. A file in `reports/`. |
| A round directory with no `harness.md` | Nobody knows which version of the skills ran the round — there's nothing to check a skill change against. |
| "The outcome is obvious from the diff" | The diff doesn't show what the review before merging turned up, or what you decided not to fix. |

## Who reads all this

`retrospecting-review-rounds` reads the accumulated artifacts back and turns what it
finds into entries in the observation journal. That is the reason the round's resolution
and the harness version are written as files: the retrospective reads the disk, not the
conversation.
