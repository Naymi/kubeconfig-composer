---
name: gh-project-setup
description: Use when bootstrapping the GitHub Projects (v2) backlog for a repository — the one skill in the gh-project-* family that creates backlog/gh-project.json. The other four (gh-project-next, gh-project-capture, gh-project-audit, gh-project-sync) only read it and fail with "run gh-project-setup first" if it's missing or unresolvable.
---

# gh-project-setup

## Overview

Creates and links a GitHub Project (v2) for the current repository, adds the custom `Priority` field on top of GitHub's default `Status`, and writes `backlog/gh-project.json` — the pointer file every other `gh-project-*` skill depends on. Idempotency is decided against GitHub, not against the local file: a repo that already has a Project linked to it is never set up a second time — the existing Project is adopted.

**Read `gh-project-conventions` (the `gh-project-conventions/` directory next to this one in the same skills folder: `SKILL.md` + `reference.md`) before running this** — it owns the field schema, the stop-and-ask rule for items the schema can't express, the exact `backlog/gh-project.json` and mirror-file shapes, and the slug algorithm. This skill doesn't repeat any of that; it only describes the setup procedure and the `gh` CLI mechanics.

## Prerequisites

- `gh` CLI authenticated: `gh auth status`.
- The `project` OAuth scope on the token. Linking an issue to a Project (step 5, migration) fails without it: `gh auth refresh -s project`.

## Algorithm

### 1. Idempotency check — ask GitHub first

`backlog/gh-project.json` is a cache, not the source of truth: it lives in one worktree, may be uncommitted, and is simply absent in a fresh clone. Its absence is no evidence that GitHub is empty. So the first call is always the repository's own Project links, even — especially — when the file is missing:

```bash
gh api graphql -f query='query($owner:String!,$repo:String!){repository(owner:$owner,name:$repo){projectsV2(first:20){nodes{number title url items{totalCount}}}}}' -f owner=<owner-login> -f repo=<repo>
```

**While that query returns any node, never run `gh project create`.** A repo that already has a Project linked is not a fresh setup, whatever the local file says.

| `repository.projectsV2` | `backlog/gh-project.json` | Do |
|---|---|---|
| a node whose number equals the file's `projectNumber` | present and `gh project view <N> --owner <owner> --format json` resolves | Report "already configured", recommend `gh-project-audit`. Stop. |
| one or more nodes | absent, or naming a different or unresolvable Project | **Adopt** the existing Project — see below. Never create. |
| no nodes | present but doesn't resolve | Fresh setup (step 2), saying explicitly that the old pointer was stale — don't silently treat it as if nothing existed. |
| no nodes | absent | Fresh setup (step 2). |

**Adopting an existing Project** — the recovery path when the pointer was lost, the repo was cloned fresh, or the Project was created from another worktree or by hand:

- More than one node → print number, title, URL and item count for each and ask which one this repo's backlog lives in. Don't pick the newest, the lowest number, or the one whose title matches `<repo> Backlog` on your own.
- Read the chosen Project's schema: `gh project field-list <N> --owner <owner-login> --format json`.
- `Priority` absent → create only that field on the existing Project, with step 3's `field-create` call. Present → reuse its IDs as they are; never re-create it.
- Write `backlog/` (step 4) from those IDs, and say plainly that this was an adoption of Project #N, not a new setup.
- Don't migrate (step 5) as part of an adoption. The adopted Project already holds items, so the informal-backlog files on disk may already be in it; report what it holds and leave migration to a separate, explicitly requested run — which still goes through step 5's duplicate check.

### 2. Ask + scan

Ask only for the Project title; default `<repo> Backlog`, where `<repo>` comes from:

```bash
gh repo view --json name -q .name
```

Scan the repo root for typical informal-backlog markers: `todo/`, `TODO.md`, `backlog/`, `notes/`, `ISSUES.md`, `BACKLOG.md`. If `backlog/` exists but has no `gh-project.json` inside it, don't assume it's a migration source — it may be a leftover from an interrupted previous setup; ask the user what it is rather than guessing. Read `backlog/open-questions/` while you are here, as `gh-project-conventions` ("Picking it back up") specifies: whatever this run ends up doing, its report ends with the open-questions section that rule requires, and on a repo this skill hasn't set up yet that directory is usually absent, which reads the same as empty.

If any markers are found, list them and ask whether to migrate their contents into Issues. Don't assume yes. If declined, leave the source directory (e.g. `todo/`) completely untouched and skip step 5 — proceed straight from step 4 to done, with no migration summary and no question about deleting anything.

### 3. Create the Project, link it, add the field

Verified against `gh <cmd> --help` and a live scratch Project (create → link → field-create → field-list → delete) on 2026-08-31, gh v2.98.0. Resolve the owner login first — needed literally in step 3b, see Gotchas:

```bash
gh repo view --json owner -q .owner.login
```

```bash
gh project create --owner <owner-login> --title "<title>" --format json
# → {"id": "PVT_...", "number": N, "owner": {"login": "..."}, ...}
```

```bash
gh project link <N> --owner <owner-login> --repo <owner-login>/<repo>
# No --format flag on `link` — unlike create/field-create/field-list, it has none; output is plain text, nothing to parse.
```

```bash
gh project field-create <N> --owner <owner-login> --name "Priority" --data-type SINGLE_SELECT --single-select-options "P0,P1,P2,P3" --format json
# → {"id": "PVTSSF_...", "name": "Priority", "options": [{"id":"...", "name":"P0"}, {"id":"...", "name":"P1"}, {"id":"...", "name":"P2"}, {"id":"...", "name":"P3"}]}
```

This response already carries everything `backlog/gh-project.json` needs for `priorityFieldId`/`priorityOptionIds` — no follow-up call for those two. Still need the pre-existing `Status` field's own ID and option IDs (unique per Project, so can't be hardcoded):

```bash
gh project field-list <N> --owner <owner-login> --format json
# → find the entry {"name": "Status", "id": "PVTSSF_...", "options": [{"id":"...","name":"Todo"}, ...]}
```

### 4. Write `backlog/`

Create `backlog/README.md` stating it's auto-generated and must not be hand-edited, and `backlog/gh-project.json` in the exact shape defined in `gh-project-conventions/reference.md` (`owner`, `repo`, `projectNumber`, `projectId`, `priorityFieldId`, `priorityOptionIds`, `statusFieldId`, `statusOptionIds`) using the IDs collected in step 3.

### 5. Migration (only if accepted in step 2)

#### Duplicate check — runs before 5a

First check what the repo already has. A previous migration may have run from another worktree, under a different title convention, or into a Project whose pointer file was lost:

```bash
gh issue list --repo <owner-login>/<repo> --state all --limit 200 --json number,title,state
```

Compare each source file against that list by meaning, not by exact string — an earlier run may have prefixed the title (`01 — Types and search`) or reworded the heading. For every source file that plausibly matches an existing Issue, print the pair (file → Issue number and title) and ask before creating anything. **Never open a second Issue for a source file that already has one.** If the match is real but the mirror file is missing, generate the mirror for the existing Issue instead of creating an Issue.

A file that already has its Issue leaves the migration here: 5a and 5b see only the files that are left.

#### 5a. Fit pass — before anything is created

Read every informal-backlog file found in step 2 and judge fit per the stop-and-ask rule in `gh-project-conventions`, which owns both the judgment and the question it leads to. **Nothing is created during this pass** — no Issues, no mirrors, no Project items.

- **Everything fits** → say so in one line and go to 5b.
- **Anything doesn't fit** → **stop and ask, before the first `gh issue create`.** Read `gh-project-conventions`' rationalization table before talking yourself out of the question, and put the question in the shape it requires. What is local to a migration: **one question covers every flagged file at once**, each named by file. Create nothing until the user answers.
  - **As-is** → go to 5b, and for each flagged file keep the fit note in the body of both the Issue and its mirror.
  - **Extend first** → the user runs the schema change themselves (see Out of scope) — and migration resumes after.
  - **The session ends before any answer arrives** → write one `backlog/open-questions/<slug>.md` per flagged file (`source:` naming that file) per `gh-project-conventions` ("If the session ends before the answer", shape in `reference.md`), migrate nothing, and say so in your closing message. Don't invent a status file of your own anywhere else in the repo.

Migrating twenty files where one doesn't fit is still a stop: the question names that one file, not all twenty.

#### 5b. Convert each file (only once 5a is settled)

- Title = the file's first heading line; body = everything after it.
- Create the Issue and add it to the Project in one call — `-p/--project` takes the Project **title**, not its number:

  ```bash
  gh issue create --repo <owner-login>/<repo> --title "<title>" --body "<body>" --project "<project title>"
  ```

- Set its `Priority` to the migration default `P2` (retriage is a separate, later concern — never leave it unset, but don't try to infer real priority here):

  ```bash
  gh project item-edit <N> --owner <owner-login> --url <issue-url> --field "Priority" --value "P2"
  ```

- Read the item's `Status` back off GitHub — this skill is the one mirror writer that pays for the extra call, and `gh-project-conventions/reference.md` ("Never invent `Todo`") says why:

  ```bash
  gh api graphql -f query='query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){issue(number:$number){projectItems(first:10){nodes{project{number} status: fieldValueByName(name:"Status"){... on ProjectV2ItemFieldSingleSelectValue{name}}}}}}}' \
    -f owner=<owner-login> -f repo=<repo> -F number=<number>
  ```

  Take the node whose `project.number` matches this Project and use its `status.name`. **`null` is an answer here, not a failure** — a read this soon after the add can legitimately come back empty (as can `projectItems` itself, for the same indexing lag `gh-project-sync#2a-walk-both-sides--mirrors-on-disk-and-live-items` describes). Write whatever came back. Don't retry, don't wait for it to turn into `Todo`, and don't fall back to writing `Todo` by hand. What the read buys is the common case: where the Project's "Item added → Todo" automation has already fired, the mirror carries the real `Todo` instead of a `null` that makes offline `gh-project-next` skip the item.

  **Do this per file, in that file's own turn — never as one batched pass after every Issue exists.** Collecting all the statuses at the end with a single `gh project item-list` is cheaper and breaks the rule every mirror writer in this family follows: the mirror is written as soon as its Issue exists, gated on nothing downstream (`gh-project-sync#1c-promoting-one-draft`, `gh-project-capture#2-live-path` steps 4–6). Batched, an interruption mid-migration leaves live Issues with nothing on disk recording them at all.

- Generate its synced mirror at `backlog/<number>-<slug>.md` per `gh-project-conventions/reference.md` (slug algorithm ibid.): `status` = exactly what the read-back returned (the option name, or `null` if it came back unset — never `Todo` written on your own authority), `priority: P2`, `blocked_by: []`, `milestone: null`, `updated_at` copied verbatim from `gh issue view <number> --repo <owner-login>/<repo> --json updatedAt -q .updatedAt`.

After every file is converted, report what was migrated — naming each file flagged in 5a and how it ended up recorded per the user's decision there. **Name every mirror whose `status` came back `null`, and if that is all of them, say what follows from it:** offline `gh-project-next` reads `Status` only, and both its candidate filter and its no-candidate fallback look at `Todo` items — so a `backlog/` where every mirror carries `null` gives an offline run nothing at all to report, not a lower-ranked item. `gh-project-sync` is the fix and the next step; say so rather than leaving someone to meet an empty answer and wonder. Then **explicitly ask** whether to delete the original directory (e.g. `todo/`). Never delete it unasked — agreeing to migrate the content earlier is not the same as agreeing to remove the source. Close with a one-line summary: "Migrated N item(s) into Project '<title>'." Then, last of all, the open-questions section `gh-project-conventions` requires ("Picking it back up"), over what step 2 found under `backlog/open-questions/`.

## Gotchas (verified 2026-08-31, gh v2.98.0)

- `gh project link` has no `--format` flag — `create`, `field-create`, `field-list`, `item-add`, and `item-edit` all do.
- `gh project link --owner "@me" --repo <login>/<repo>` fails with `'<login>/<repo>' has different owner from '@me'`, even when `@me` and `<login>` are the same authenticated account. Always pass the literal login for `link`'s `--owner` (`create`/`field-create` accept `"@me"` fine).
- `gh issue create -p/--project` takes the Project's **title** string, not its number — and requires the `project` OAuth scope, or the call fails outright (see Prerequisites).
- `gh project list --owner <login>` lists every Project the owner has and says nothing about which repository a Project is linked to. Only the GraphQL `repository.projectsV2` query answers "does this repo already have a Project?". A missing pointer file plus an unread existing link produces a duplicate Project *and* a duplicate Issue per source file.
- `gh issue create -p/--project` returns the new Issue's URL and nothing about the Project item it just created, so the item's field values can't be read off that response. `repository.issue(number:).projectItems` reaches them without paginating the whole Project — verified 2026-09-03 (gh v2.98.0) against a live Project item, which came back with its `project.number` and `status.name` populated. Not verified: what it returns in the seconds right after the Issue is added to the Project, which is exactly the case 5b treats as "`null` is an answer."
- `gh project field-create`'s JSON response already includes the new field's option IDs — don't make a second `field-list` call just for the field you created; only use `field-list` for the pre-existing `Status` field.

## Out of scope

Never creates a second Project for a repo that already has one linked — adopts it instead, and asks which one when there are several. Never deletes the migration source directory without an explicit yes. Never modifies `Status`, `Milestone`, or blockers — those stay untouched per `gh-project-conventions`. Never extends the Project schema itself in response to a fit-judgment flag (step 5a) — running the change stays a separate, human action, per the stop-and-ask rule in `gh-project-conventions`. Never runs offline — creating a Project always needs GitHub reachable, so there's no offline branch here unlike `gh-project-capture`/`gh-project-next`.
