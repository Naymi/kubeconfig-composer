---
name: gh-project-capture
description: Use when an agent or the user has just found a problem, bug, or backlog-worthy gap during the current session and it needs to be recorded in this repository's GitHub Project (v2) backlog — including when GitHub is unreachable (network, auth, rate-limit) and the finding still must not be dropped. Requires backlog/gh-project.json (from gh-project-setup) to already exist for the live path; falls back to a local file otherwise, and stops to ask the user when the finding does not fit the Status/Priority schema.
---

# gh-project-capture

## Overview

Records a finding from the current session as a GitHub Issue in this repository's Project (v2) backlog: `gh issue create` → add to Project → set `Priority` → optional `addBlockedBy` → synced mirror file.

**Read `gh-project-conventions` (the `gh-project-conventions/` directory next to this one in the same skills folder: `SKILL.md` + `reference.md`) before running this** — it owns the field schema, the stop-and-ask rule for items the schema can't express, the `backlog/gh-project.json`/mirror/pending shapes, the slug algorithm, and the online/offline check. This skill only adds the capture sequence and the never-lose-it fallback.

## The rule: a finding is never lost

**A finding you decide to capture gets written down — to GitHub if reachable, to `backlog/pending/<slug>.md` if not. There is no third option.** Telling the user about it in chat, or making a mental note to file it "later," is not capture — it is exactly the failure this skill exists to prevent. A chat message disappears with the conversation; `backlog/pending/` survives it.

This holds under every pressure that makes skipping the file feel reasonable:

| Excuse | Reality |
|---|---|
| "The user is waiting, I'll mention it and move on" | Mentioning it in chat is not capture. Write the file — it takes one tool call, seconds, not minutes. |
| "GitHub is down, nothing to do here" | Being offline is exactly the case `backlog/pending/` exists for. Offline means write the file, not skip it. |
| "This is a small/obvious finding, not worth the ceremony" | Small findings get lost fastest — no one remembers to re-file them. Same rule regardless of size. |
| "I'm out of time on my current task" | The write is one small file, not a detour into fixing the finding. It doesn't compete with finishing the task. |
| "I'll just tell the user and let them decide" | Deciding whether to act on it is the user's call; losing the record of it isn't. Write it, then tell them. |

**Red flag:** if what you're about to do is describe the finding in your response and not write a file, stop — that is the failure mode, not a shortcut.

**The one legitimate delay** is an open schema-fit question (§0b): while it stands, nothing is created, and the finding's full text sits in the question itself. The moment the user answers, the write happens — and if the session ends first, the question goes to `backlog/open-questions/<slug>.md` so the text still outlives the conversation. Nothing else — not time, not size, not "I'll ask later" — postpones the write.

## 0. Judge Priority — always, before anything else

Per `gh-project-conventions`, `Priority` is required on every `Todo` item and is never left blank. This is the agent's judgment call, not something that waits on network access — decide it before attempting anything else, live or offline:

- **P0** — critical/blocking: breaks the build, causes data loss, a security hole, or blocks other known work outright.
- **P1** — high: real user- or correctness-impact, but a workaround exists or it's not blocking other work.
- **P2** — normal (default when genuinely unsure, not a substitute for judgment when the impact is clear either way).
- **P3** — low: cosmetic, nice-to-have, or affects an edge case nobody currently depends on.

**Read `backlog/open-questions/` here too — before §0b, which can end this run on its own.** Whatever this run goes on to do, the message it closes with carries the open-questions section `gh-project-conventions` requires ("Picking it back up"), which also states what that read takes: a captured finding's report, a `pending/` fallback's report, and §0b's own stop-and-ask alike. Drop the section only when the directory was empty.

## 0b. Judge fit — does this belong under Status/Priority as-is? Blocking.

Before creating anything, check whether the finding actually fits what a plain Todo/Priority item can honestly express:

- **Fits** — an ordinary bug, task, or feature gap that `Priority` alone captures without distortion. Proceed straight to §1.
- **Doesn't fit** — e.g. it's a decision/open question rather than a defect, or it belongs to a distinct category the team cares about (security, migration, spike) that `Status`/`Priority` can't convey. **Stop here and ask.** This is the stop-and-ask rule owned by `gh-project-conventions` — it states what the question must contain and what stopping forbids; read its rationalization table before talking yourself out of the question.

Then act on the answer:

- **As-is** → §1 unchanged, and the body you write (live issue §2, mirror §2 step 6, or pending draft §3 alike) still carries the fit note, so it survives a later `gh-project-sync` promotion — promotion copies the draft's body forward verbatim and does no fit-judgment of its own.
- **Extend first** → the user runs the schema change themselves (`gh-project-setup`'s territory, see Scope); capture resumes afterwards.
- **The session ends before any answer arrives** → write `backlog/open-questions/<slug>.md` per `gh-project-conventions` ("If the session ends before the answer", shape in `reference.md`) and say so in your closing message. This is the only file §0b ever writes: it is not a capture, it is never a `pending/` draft, and it is never a way to skip asking while the user can still answer.

## 1. Check `backlog/gh-project.json`

```bash
gh project view <projectNumber> --owner <owner> --format json
```

using `owner`/`projectNumber` from `backlog/gh-project.json`.

- **Missing, or present but doesn't resolve:** tell the caller to run `gh-project-setup` first, then go straight to the **pending fallback (§3)** — do not attempt `gh issue create`. A bare Issue with no Project to attach it to would have no `Priority` field to set and no mirror to generate consistently with the rest of the family; the pending draft already carries the judged priority in its own frontmatter and needs no Project to exist yet. Stop after writing it.
- **Resolves:** continue to §2.

## 2. Live path

1. Shared online/offline check (`gh-project-conventions`, e.g. `gh auth status`, short timeout). Failure → **pending fallback (§3)**.
2. Create the issue, using `owner`/`repo` from `backlog/gh-project.json`:
   ```bash
   gh issue create --repo <owner>/<repo> --title "<title>" --body "<body>"
   ```
   **Any failure here** — network, auth, rate-limit, missing scope, anything — routes to §3. This is not limited to the failures the check in step 1 would have caught; step 1 is a fast-path, not the only gate.
3. Once the issue exists, resolve its details in one call:
   ```bash
   gh issue view <url> --repo <owner>/<repo> --json id,number,url,updatedAt -q .
   ```
   Retry once on failure. **From here on the finding is already durably captured** — it's a real Issue, findable with `gh issue list --repo <owner>/<repo>` even if every remaining step below fails. That changes how failures are handled: not by falling back to `pending/` (which would either duplicate the issue or violate the pending draft's `github_issue: null` contract owned by `gh-project-conventions`), but by retrying once and then reporting the exact manual remediation.
4. Add to the Project and set `Priority` (mutation signatures: `gh-project-conventions/reference.md`):
   ```bash
   gh api graphql -f query='mutation{addProjectV2ItemById(input:{projectId:"<projectId>" contentId:"<issue node id>"}){item{id}}}'
   ```
   then `updateProjectV2ItemFieldValue` with `priorityFieldId`/`priorityOptionIds[<chosen>]` from `backlog/gh-project.json`. If a cached field/option ID is rejected, re-resolve via `gh project field-list` and rewrite the cache per `gh-project-conventions/reference.md`, then retry once.
   **If this still fails after one retry:** report the issue's number and URL, the `Priority` you judged, and the exact commands to finish it by hand:
   ```bash
   gh project item-add <projectNumber> --owner <owner> --url <issue-url> --format json
   # → {"id": "PVTI_...", ...} — this is the itemId the field mutation below needs
   ```
   then the `updateProjectV2ItemFieldValue` mutation with that `itemId`. Do not write a `pending/` file for it — see step 3 above.
5. If the finding genuinely blocks other known work, `addBlockedBy` (one call per blocker — see reference.md). Best-effort: retry once on failure, then report which blocker didn't get linked without failing the rest of the capture — the issue, Project membership, and Priority are already secured by this point.
6. Generate the synced mirror at `backlog/<number>-<slug>.md` per `gh-project-conventions/reference.md` (slug algorithm ibid. — check collisions against both `backlog/*.md` and `backlog/pending/*.md`), using the real `updatedAt` from step 3, and — for the Project fields — only what actually landed: `status: null`, since this skill never sets `Status` and never reads it back (`gh-project-conventions/reference.md`, "Never invent `Todo`"), and the judged `Priority` **only if step 4's field write succeeded**, `null` if it failed. `blocked_by` is denormalized (number + title + status) for anything linked in step 5 (`blocked_by: []` if none — always keep the key, never omit it).

   `blocked_by[].status` is `Done`/`Todo` vocabulary (matching `gh-project-next`'s `!= "Done"` readiness check), but the only field a blocker query returns is the blocking issue's open/closed `state` — fetching its actual Project `Status` would need a second query per blocker, and it may not even be in this Project. Map `state` directly: `CLOSED` → `status: Done`, `OPEN` → `status: Todo`. Only the done/not-done distinction feeds readiness, so this loses nothing a reader depends on.

**Steps 4–6 are independent, best-effort, and unconditional once the issue exists (step 3):** a failure in one is reported but never skips the ones after it. In particular, always reach step 6 and write the mirror even if step 4's live field write failed and was only reported as manual remediation above — an unwritten mirror is a live Issue nothing on disk records, which is the worse outcome by a wide margin.

**What the mirror must not do is record the `Priority` that failed to land.** The mirror mirrors GitHub; the Project item has no `Priority`, so the file says `null`. Writing the judged value instead does not preserve it — `gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged` regenerates this file from live data, finds the mismatch, and overwrites it with `null` on the next run, erasing exactly the value the remediation report told the human to restore. The report is what carries it until someone runs the command; the sync after that copies the real value here. The same rule covers a blocker whose `addBlockedBy` failed (`gh-project-sync#1c-promoting-one-draft`).

## 3. Pending fallback — offline, unresolvable Project, or any live-path failure before the issue exists

1. Compute the slug from the title, using the exact algorithm in `gh-project-conventions/reference.md` — check collisions against both `backlog/*.md` and `backlog/pending/*.md`.
2. If this finding blocks other known work: reference an already-synced issue the normal way (`{number, title, status}`); reference another still-pending draft by slug (`{slug, title}`) since it has no issue number yet. If there's nothing to list, keep `blocked_by: []` — never omit the key. **Don't cycle-check** — a two-way (or longer) block between pending drafts is possible and isn't this skill's job to catch; `gh-project-sync` hard-errors on a cycle at promotion time (`gh-project-conventions`). If you notice one while capturing, say so, but write the file anyway — resolving it is a human decision.
3. Write `backlog/pending/<slug>.md`:
   ```markdown
   ---
   github_issue: null
   url: null
   status: pending-sync
   priority: <judged in §0>
   blocked_by:
     - {slug: "...", title: "..."}      # unpromoted pending draft
     - {number: 17, title: "...", status: Done}   # already-synced issue
   milestone: null
   updated_at: <now, UTC, e.g. `date -u +%Y-%m-%dT%H:%M:%SZ`>
   ---

   # <title>

   <body>
   ```
4. Report where it landed and why (offline / `gh-project.json` missing or unresolvable / a specific `gh issue create` error) and that `gh-project-sync` will promote it once GitHub and (if applicable) `gh-project-setup` are both in place. End with the open-questions section §0 names.

## Script

`scripts/capture.py` (path relative to this skill's installed directory, e.g. `.agents/skills/gh-project-capture/scripts/capture.py`) runs the live path in one call, so a batch of findings doesn't have to be filed by hand.

```sh
python3 .agents/skills/gh-project-capture/scripts/capture.py items.json
```

`items.json` is a list of `{"title": "...", "body": "...", "priority": "P0|P1|P2|P3"}`. For each item it runs `gh issue create`, adds the issue to the Project, sets `Priority`, and writes the synced mirror `backlog/<number>-<slug>.md` (slug per `gh-project-conventions/reference.md`). It finds the repository root with `git rev-parse --show-toplevel` from the current directory. If the Project step fails it prints `PROJECT FAIL <number>` and writes the mirror with `priority: null`; the issue still exists.

Requires `gh` authenticated with access to the repository and its Project, and an existing `backlog/gh-project.json`. It creates real issues, so it only covers the online path: offline, or with no `gh-project.json`, follow the pending-file fallback above. It does not check for blockers; add `addBlockedBy` separately. The sequence in this file stays the authority; the script only automates its happy path.

## Scope

Never bootstraps a Project (`gh-project-setup`'s job — stop and say so per §1). Never extends the Project schema itself (new field, new label) — running that change is the user's own action, per the stop-and-ask rule in `gh-project-conventions` that §0b defers to. Never modifies `Status`, `Milestone`, or an existing item's `Priority` — only sets `Priority` on the item it just created. Never promotes an existing `pending/` draft itself — that's `gh-project-sync`. Doesn't cycle-check blockers among pending drafts (deferred to `gh-project-sync`/`gh-project-audit`).
