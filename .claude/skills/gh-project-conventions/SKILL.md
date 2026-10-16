---
name: gh-project-conventions
description: Use when creating, reading, or writing to a GitHub Project (v2) backlog through any gh-project-* skill (setup, next, capture, audit, sync) — defines the field schema, blocked-by/sub-issue mechanics, the backlog/ file mirror format, the stop-and-ask rule for items the schema cannot express, and the online/offline check shared by all of them.
---

# gh-project-conventions

## Overview

Canonical schema and mechanics for the `gh-project-*` skill family's GitHub Projects (v2) backlog. Every `gh-project-*` skill reads this before touching a Project or the `backlog/` directory — it's the single source of truth, so `gh-project-audit` has a fixed definition of "correct" to check against instead of a second opinion.

## Field schema

| Field | Type | Values | Notes |
|---|---|---|---|
| `Status` | native single-select (GitHub default) | `Todo`, `In Progress`, `Done` | never modified by a `gh-project-*` skill — see "Why no skill writes `Status`" below (a human may still set it by hand in the Project UI, and the Project's own automation may move it when an Issue closes) |
| `Priority` | custom single-select | `P0` critical/blocking, `P1` high, `P2` normal (default), `P3` low | required on every `Todo`/`In Progress` item — not required once an item is `Done`; added once by `gh-project-setup` |
| blockers | native issue dependency, not a Project field | any issue in the same repo | `addBlockedBy`/`removeBlockedBy` GraphQL mutations, one blocker per call — see reference.md |
| `Milestone` | native Project field | standard GitHub milestones | never modified by a `gh-project-*` skill |
| `Sub-issues progress`, `Parent issue` | native Project fields | — | never modified by a `gh-project-*` skill; use `addSubIssue` to decompose |

**Readiness** (what `gh-project-next` offers): `Status == Todo AND count(open blockedBy) == 0`. A closed blocker still appears in `blockedBy` but doesn't count against readiness. `Priority` is **not** part of this condition — an item with no open blockers is ready even if `Priority` is unset; a missing `Priority` is an audit concern (`gh-project-audit`), not a readiness gate. How ready items are then ranked is `gh-project-next`'s own section to state.

### Why no skill writes `Status`

Not because "done" is unverifiable. A merged PR and a closed Issue are as checkable as anything else these skills read, and `gh-project-sync` already trusts this field enough to **delete** a mirror file when it reads `Done`. The reasons are mechanical:

- Setting `Status` to `Done` through `updateProjectV2ItemFieldValue` **closes the underlying Issue** wherever the Project's "Item closed" ↔ "Status: Done" automation is on — observed live, see `gh-project-sync`'s gotchas. It is not a label, it is closing the work.
- `gh-project-sync` deletes that item's `backlog/<number>-<slug>.md` on `Done`, so a wrong `Done` also takes the local file with it.
- Keeping `Status` out of every skill's write set leaves `gh-project-audit` a definition of "correct" that no skill authored.

**The path from merged work to `Done` doesn't run through anyone editing the field.** The PR closes the Issue, and the Project's automation moves `Status`. That link fires only on GitHub's **English** keywords — `close`/`closes`/`closed`, `fix`/`fixes`/`fixed`, `resolve`/`resolves`/`resolved` — followed by `#<number>` in the PR description or in a commit on the default branch. A closing sentence in any other language ("Закрывает #16") is an ordinary cross-reference: nothing closes, `Status` stays `Todo`, and the item reports finished work as outstanding with no error anywhere. Repos writing PR descriptions in another language should say so in their own contributor docs; `gh-project-audit` check 6 catches the cases that slip through anyway.

**Item = Issue, always.** No draft Project items. Sub-issues decompose an issue into independently closable pieces; flat issues otherwise.

**Scope exclusions.** Only Issue-type items — not PRs. What identifies a non-Issue item in the paginated `items` query every skill here runs is the **absence of `content.number`**: the `... on Issue` fragment yields an empty `content` object rather than `null` when the item isn't an Issue, so there is no type field to test — the missing number is the test. Only items belonging to the current repository (matched by `content.repository.nameWithOwner` against `owner`/`repo` in `backlog/gh-project.json`), even if the Project holds items from elsewhere.

## Schema fit is the human's call — never decided silently

Any skill that judges whether an item fits the flat `Status`/`Priority` schema hits the same moment: an item this schema can't express honestly — a decision or open question rather than a defect, a recurring process, a research spike, or a category the team tracks separately (security, migration).

**When that happens the agent stops and asks. It never picks the answer.** Stopping means nothing is created while the question is open: no Issue, no mirror file, no `pending/` draft, no migration. The question carries, in one message: which item(s) don't fit and why; the full title and body text of anything **not already on disk** (content the question names by path is already durable — a finding that exists nowhere else survives only in the question while it stands, so it goes on screen in full); 1–3 concrete ways the schema could cover the case going forward (a single-select `Type` field, a boolean field, a plain GitHub label); and the "record it as-is under `Status`/`Priority`" option, carrying the `Priority` you judged so that option is concrete.

**Never run the extension yourself.** Adding a field or otherwise changing the Project schema is a human-run action; these skills only stop, name options, and wait.

| Excuse | Reality |
|---|---|
| "I'll note it in the issue body and the final report — that's visible enough" | A note the user reads after the items exist is a decision already taken. A report is not a question. |
| "`P2`/`Todo` is harmless, nothing breaks" | The distortion isn't the field value, it's the backlog asserting this is an ordinary task. |
| "They asked me to migrate/capture, so the format is already approved" | They approved moving the content, not the schema it lands in. Different decision. |
| "Only one item out of twenty doesn't fit" | Then ask about the one. A question costs one message; a mis-shaped backlog outlives the session. |
| "The user is busy — I'll flag it and keep going" | Asking takes seconds. Choosing for them is exactly the failure this rule exists to prevent. |
| "I'm not certain it really doesn't fit" | Uncertainty is the trigger, not an exemption. If the thought occurred, say it out loud and ask. |
| "They said not to interrupt, so I'll write the open-questions file and move on" | That file is for a conversation that is ending, not for a user who is sitting right there. Someone who can answer gets the question. |
| "The open question on disk isn't related to what I'm doing now" | Then nobody will ever raise it while doing something related either. It goes in the report anyway. |

**Red flag:** you're about to write an Issue, a mirror, a draft, or a report containing "doesn't fit" or "the schema could be extended" while the user has answered no question about it. Stop — send the question instead.

### If the session ends before the answer

The question lives in the conversation, so a conversation that ends takes the finding with it. Write it to `backlog/open-questions/<slug>.md` (exact shape in `reference.md`) **only when one of these is true right now**, and say in your closing message that you did:

- a system notice says the context is exhausted or the run is being torn down, or
- the user said they are leaving or unreachable ("I'm on a plane", "gone till Monday"), or
- the question has already been put to them in an earlier session and is still unanswered.

If the user is present in this conversation, the question **is** the action — ask it and stop there. None of these are the predicate: "they told me not to interrupt", "they're busy and probably won't answer", "I'll write it just in case and ask too", "it's cheap insurance". A file the user then has to clean up is a cost, not a backup.

That file is not a backlog item and never becomes one by itself: `gh-project-sync` promotes only `backlog/pending/`, and never promotes, refreshes, or deletes anything under `open-questions/`. Writing a `pending/` draft instead would answer the question — the next sync run would promote it into an ordinary Issue, making "as-is" the decision nobody took.

Two ways to get this wrong:

- **Writing the file instead of asking.** It is what survives an ending session, not a way to skip the question. While the user can still answer, ask.
- **Inventing your own place for it** — a note in the repo root, an extra section in `README.md`, a line in the final report. The next session finds only what this convention says to look for, so an improvised location loses the finding just as thoroughly as writing nothing.

**Picking it back up:** every `gh-project-*` skill that produces a report reads `backlog/open-questions/` at the start of its run — per file, the `priority_if_as_is` frontmatter key, the `# ` title line, and the `## Options put to the user` list (shape in `reference.md`), which is what the section below is assembled from and all of it that any reader needs — and, whatever else that run did, ends its report with this section, one line per file:

> **Open schema questions waiting for you:**
> - `<slug>` — <the finding's title>. Options: as-is under `Status`/`Priority` (`priority_if_as_is`), or <the named extensions>.

The section is required whenever the directory is non-empty, **especially when the questions have nothing to do with what you just did** — an unrelated question is precisely the one nobody remembers to raise. "It didn't block this capture" and "it's about a different item" are not reasons to drop it; they're the reason the file existed long enough for you to find it. Drop the section only when the directory is empty. The file is deleted by the skill that acts on the user's answer, never by `gh-project-sync`.

## backlog/ directory

```
backlog/
  gh-project.json          # pointer + cached field/option IDs — see reference.md
  README.md                # "auto-generated, do not hand-edit"
  <issue-number>-<slug>.md # synced mirror of an open Issue
  pending/<slug>.md        # local-only draft, no Issue yet
  open-questions/<slug>.md # unanswered schema-fit question — not a backlog item
```

Sync is one-directional, GitHub → file, with one exception: a `pending/` draft is promoted file → GitHub exactly once, then becomes a normal synced mirror. A synced mirror whose content no longer matches a fresh regeneration is reported, then overwritten — never merged. That mismatch is not evidence of a hand edit: a blocker change moves what the mirror must say without moving the Issue's `updatedAt`, so `gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged` reports a divergence and names the fields rather than naming a culprit.

Slug: exact ordered algorithm, byte-for-byte identical everywhere a slug is generated (so promoting a draft is a clean `mv`, never a new file next to a stale one) — see reference.md.

**If `backlog/gh-project.json` is missing, or present but its `projectNumber` no longer resolves:** stop and say "run `gh-project-setup` first" — never create or guess a Project.

## Online/offline detection

Short-timeout `gh auth status` (or `gh api user`). Any failure — network or auth — is "offline." Same check, same interpretation, in every skill that needs it (`gh-project-capture`, `gh-project-next`, `gh-project-sync`).

## Scripts

Shared setters for the `Status` and `Priority` fields, under `scripts/` (path relative to this skill's installed directory, e.g. `.agents/skills/gh-project-conventions/scripts/`). Run them from inside the repository: they find the root with `git rev-parse --show-toplevel` and read `backlog/gh-project.json` there.

```sh
scripts/set-status.sh <issue-number> <Todo|In Progress|Done>
scripts/set-priority.sh <issue-number> <P0|P1|P2|P3>
scripts/project-field.py <gh-project.json> <status|priority> <issue-number> <option>
```

`set-status.sh` and `set-priority.sh` are thin wrappers over `project-field.py`, which sits next to them. Each looks up the issue's item in the Project from `gh-project.json` and sets the single-select field through the GraphQL API, printing `#<number> -> <option>`. Setting the value an item already has is safe. They exit 2 on a bad option or argument count. Exit 1 means any runtime failure: run outside a git repository (the wrappers say so on stderr and call nothing), the issue is not in the Project, or a `gh` error such as network, authorization or an unknown issue number; the message on stderr tells which.

Requires `gh` authenticated with write access to the Project, and an existing `backlog/gh-project.json`. They change Project data only; `backlog/` mirrors are refreshed by `gh-project-sync`. `Priority` changes belong to the user's decision (see the stop-and-ask rule above); the scripts do not enforce it.

## Reference

Exact JSON shapes, GraphQL mutation signatures (verified against this account's schema), and the mirror file format: see `reference.md`.

**Cross-references.** `§N` addresses a section of the file it appears in. A pointer into another file takes one of two forms, and neither is written before it is checked against the target — the rule `retrospecting-review-rounds#search-for-the-rule-before-you-call-it-missing` states for every address in this harness:

- **A heading in another skill's `SKILL.md`** — a heading anchor, `gh-project-<skill>#<heading-slug>`, verified against that file's own headings with `grep -n '^#'`.
- **Anything in `reference.md`, and any bolded lead-in that is not a heading** ("Picking it back up", "Never invent `Todo`") — the file, then the phrase in quotes: `gh-project-conventions/reference.md`, "Shared limitations". Verify with `grep -n` for that exact phrase. An anchor cannot address either one: the slug form names a skill, not which of its two files, and a lead-in inside a paragraph or a bullet has no anchor to name.
