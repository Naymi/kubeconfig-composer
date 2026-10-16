---
name: running-review-fix-rounds
description: Use when turning code-review findings into fix work — planning a fix round, writing briefs for parallel worker agents, splitting findings across worktrees, or integrating the result
---

# Fix rounds off the back of a review

## Overview

A fix round spawns the next one when the plan lists **sites** instead of **rules**,
and when nobody reads the fix code before it merges. This skill exists to close
findings in a single round.

**REQUIRED SUB-SKILL:** Use persisting-review-artifacts for where the coordinator
plan, briefs, and decision log live on disk, for the frontmatter schema every one of
them carries, and how to flag them before committing.

## A finding = a rule + its sites

Every finding in the plan must carry four parts. Without them it doesn't go out to
a worker.

- **Id** — the one the review report gave it, `SEC-01`, `CQ-07`, carried over unchanged.
  Never renamed and never renumbered here: the plan, the briefs and `resolution.md` are
  read against the report by joining on this, and a finding renamed in the plan is a
  finding the retrospective will report as having vanished.
- **Rule** — the invariant in one sentence, with no filenames in it.
  "A failed durable commit leaves no trace in node state."
  The review report's ledger already carries it in this shape — copy it rather than
  writing a second version, or the round starts by disagreeing with the review about
  what the finding was.
- **Sites** — the full list of places where the rule is broken. Found by searching;
  the search command goes into the plan next to the list.
- **Already compliant** — places where the rule holds. That's the example the rest
  gets fixed against, and the proof that the rule already exists in this project.

The reviewer names one site — the one they tripped over. The coordinator finds the
rest, before slicing up the work: how many workers there are, and whether they fit
into one wave, depends on that count.

### Search by resource, not by the names in the finding

Other sites of the same rule are almost always named differently: `apply_local` vs
`apply_remote`, the write path vs replication vs bulk load. Searching for the
identifiers from the finding won't reach them, and hands you a false "there's no
second place."

Search by **the resource the rule protects**: who else writes to that state, that
counter, that table, who else holds that mutex. The list of entry points into the
resource is the list of candidates.

### A candidate is cleared by analysis, not by a verdict

For each candidate, write out three lines: where the resource is touched, where the
path can fail, where the resource is put back. **No third line means it's a site.**

"This path doesn't have that problem," "the recovery model is different here," "this
isn't two-phase" clear nothing: they restate a verdict instead of doing the analysis.
A sentence like that with no three lines behind it is the most expensive mistake in
this skill: a site found and then lost, which the next round will find all over again.

Deciding not to fix a real site in this round is legitimate, but it's a **coordinator
decision**: it goes into the coordinator plan with the reason and with the destination
it moves to — which wave, or which later round. Not as a paragraph in a worker brief.

## A file belongs to exactly one worker per wave

Overlapping on a file is not "the integrator's problem." Two branches editing one file
in parallel produce a conflict on top of a fresh refactor, and it gets resolved by
whoever wrote neither change.

Build a file × worker table. A file that lands on two workers moves to the second
wave — which starts after the first one merges.

Non-overlapping regions inside one file only count as no overlap when both changes are
mechanical. Moving blocks around and editing adjacent lines already counts as overlap.

## An invariant is closed by the shape of the code or by a test

A doc comment closes nothing: it catches neither a regression nor the eighth copy. A
comment that lists other files as the mechanism enforcing the rule is a sign of a
missing abstraction, not a defense.

Pick in this order:

1. a test for the rule (not for today's case — for the whole rule);
2. the shape of the code: a type, a single entry point, the body of a function;
3. if neither works — the finding isn't closed; say so plainly in the plan instead of
   papering over it with a comment.

"That test needs a live environment" is not a verdict: a test double for the
dependency that fails the operation is usually enough.

## The coordinator plan

Creating the round directory starts with `harness.md` — which skills and which
skills-repo commit are running this round — written before the plan and before the first
brief (see persisting-review-artifacts). The round's date won't tell anyone which version
of the skills ran, so a round without that file can never be dated against a change to
them.

The plan itself is written to a file at `.reviews/<slug>/round-<N>-<date>/plan.md`
section by section as it's ready, not kept in chat. It opens with the frontmatter block
persisting-review-artifacts defines, plus this round's split of the review's findings:

```yaml
---
skills: [running-review-fix-rounds, persisting-review-artifacts]
skills-repo: naymi/skills @ e4140c0
agent: <model or agent>
date: 2026-09-11
round: 2
findings:
  taken: [CQ-01, CQ-03, SEC-02]
  excluded:
    CQ-02: {reason: "second writer lands in round 3's refactor", destination: round-3}
---
```

`taken` and `excluded` together must account for every id the review report left open,
and for every id an earlier round deferred to this one. An id in neither list is a
finding this round silently dropped, and the frontmatter is where that shows up as a
count rather than as an impression.

Required sections after it. A missing one isn't a style issue, it's work not done:

- the "id → rule → sites → worker → wave" table, one row per finding, ids as in the
  report;
- the "file × worker" table and the reasoning behind the waves;
- **excluded sites**: real violations of the rule that this round won't take, each named
  by id, with the reason and the destination (a wave, a later round) — matching the
  `excluded` map above and duplicated in `decisions.md` next to the plan;
- **diff review before merging**: whose step it is and what to look for.

## The worker brief

Each brief is written to its own file at
`.reviews/<slug>/round-<N>-<date>/briefs/worker-<k>.md` (see
persisting-review-artifacts) — that file is its final text, not a draft to be
paraphrased to the worker in chat. Required sections:

- the rule, the sites, the compliant example (from the finding);
- a closed list of files;
- **what closes the invariant** — a specific test or a specific shape of the code;
- **comments this change makes false** — where to look for them and what to do with
  them; a comment that survives the change and becomes wrong counts as a defect in
  the change;
- what to do if the rule is broken outside the list of files (see below);
- verification commands and the report format — the report goes into a file at
  `reports/worker-<k>.md`, not retold in chat.

The coordinator makes the decisions. If the plan says "decide for yourself," "either
option is fine," "design is up to you," the decision hasn't been made, and the next
round will make it again — sometimes reverting exactly what this one did.

### Brief skeleton

Copy the headings and fill them in. Sections marked `×finding` repeat for every
finding in the brief — a brief with two findings carries them twice.

```
# Worker N — <findings>
## Rule
## Sites and the compliant example
## Shape of the fix                             ×finding
## What closes the invariant                    ×finding
## Comments this change makes false             ×finding
## Files (closed list)
## If the rule is broken outside these files
## Verification commands and the report file in reports/worker-<k>.md
```

Don't delete an empty section — write "none" in it and why. A deleted section is
indistinguishable from a forgotten one, and the one that gets forgotten is in the last
brief, once the finding already feels obvious.

## The finding reaches past the files handed out

Say up front in the brief what to do. "Note it in the report, don't fix it" is only
valid if the site is already assigned to another worker in this wave or scheduled for
the second wave; in that case say exactly that: to whom, and into which wave.

If the site isn't assigned to anyone, that's a gap in the plan, not a matter of worker
discipline. Fix the plan: widen the file list, move the finding into a wave, or add a
worker.

## The round closes with a diff review

Read every branch's diff before merging — that's the coordinator's step, not the
worker's. A worker can't tell whether they added the eighth copy: they see one file and
one finding. Fix code is new unreviewed code; if nobody looks at it now, the next round
will read it and call it a finding.

Look first for: whether the change added one more copy of something already duplicated;
whether a comment was left behind that's now false; whether every site of the rule was
covered.

This step's output is a `resolution.md` file next to the plan (see
persisting-review-artifacts, which owns its frontmatter and the five resolutions): for
every finding, by id — what closed the invariant, what the diff review turned up, and what
moved on and where to. A retelling in chat does not count: the next retrospective reads
files, not the conversation.

Two of the five are easy to blur and must not be. `not-closed` is an attempt that failed
and that nobody has routed anywhere; `deferred-to-round-<N>` is your decision, with the
round named and the reason in `decisions.md`. Writing the softer one because the round is
ending is how a failure leaves the round disguised as a plan.

The ids in `resolution.md` are the ids in `plan.md`'s `taken`, plus anything an earlier
round deferred into this one — every one of them, exactly once, with the count written
out. `approval` is recomputed here over what is still open.

As part of the same step, do the pre-commit signal from persisting-review-artifacts:
`git status --porcelain .reviews/<slug>/` and an explicit file list for the user —
`harness.md`, the plan, the briefs, `reports/`, `decisions.md` and `resolution.md` go in
their own commit, not together with the fixes.

## Red flags

These phrasings in a plan mean the round won't be the last one:

| Statement | What's really going on |
|---|---|
| "The conflict will resolve trivially at integration" | Two workers in one file. Split them across waves. |
| "That's the integrator's problem" | It's the coordinator's, and it's settled before the start. |
| "Either option is fine, the criterion is one function" | No direction was chosen. The next round will revert it. |
| "Design is up to you" | Same thing, about shape. |
| "No test is possible, a doc comment will do" | The invariant isn't closed. Look for a test double or a shape of the code. |
| "Note anything adjacent, don't fix it" with no addressee | The site is assigned to nobody. A gap in the plan. |
| The finding is named after a file and a function | The rule was never stated and the sites were never searched for. |
| "There are no other such places in the project" after searching for the names in the finding | Wrong search. Search by the resource the rule protects. |
| "This path doesn't have that problem," "the model is different here" | The candidate isn't cleared. Three lines: touched — fails — put back. |
| A decision not to fix a site sits in a worker brief | That's a coordinator decision. Its place is the coordinator plan. |
| The plan or brief exists only in chat, with no file in `.reviews/` | The retrospective will never see it. See persisting-review-artifacts. |
| `.reviews/` committed together with the fix diff | Meta-documents mixed into the code in the PR. Separate commit. |
| The diff review is done and `resolution.md` was never written | The step isn't finished: the round's outcome is recorded nowhere. |
| The plan renames a finding, or numbers findings afresh from 1 | The report's id is the join. Renumbered, the finding reads as vanished from the review and invented by the round. |
| A finding from the report is in neither `taken` nor `excluded` | The round dropped it silently. Both lists together account for every open id. |
| "It didn't work out, put it down as deferred to the next round" | `deferred-to-round-<N>` is a decision with a named round and a reason. An attempt that failed is `not-closed`, and it is a legitimate thing to write. |
| A site is withdrawn as "not actually a problem" with no three lines | That's `withdrawn` without its analysis — the bare verdict this skill already forbids, now with a schema field to hide in. |
| "The worker's report is there in the conversation" | The conversation doesn't survive context compaction. A file in `reports/`. |
