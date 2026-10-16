---
name: gh-project-next
description: Use when the user or an agent wants to know what to work on next in this repository's GitHub Project (v2) backlog. Read-only — never writes to GitHub or backlog/ — and one of two gh-project-* skills (with gh-project-capture) that work fully offline via the backlog/ file mirror. Requires backlog/gh-project.json (from gh-project-setup) to already exist.
---

# gh-project-next

## Overview

Suggests what to take up next from this repository's GitHub Project (v2) backlog. Read-only: unlike `gh-project-capture` and `gh-project-sync`, it never writes to GitHub or to `backlog/`.

**Read `gh-project-conventions` (the `gh-project-conventions/` directory next to this one in the same skills folder: `SKILL.md` + `reference.md`) before running this** — it owns the field schema, the readiness formula, the `backlog/gh-project.json`/mirror-file shapes, and the online/offline check. This skill only adds the query, the sort, and the no-candidate fallback.

**If `backlog/gh-project.json` is missing, or present but its `projectNumber` doesn't resolve:** stop and tell the caller to run `gh-project-setup` first. Never bootstrap a Project yourself — that's `gh-project-setup`'s job, and duplicating it here would silently create a second Project.

**At the start of every run, online or offline, read `backlog/open-questions/`** — this skill's report ends with the open-questions section `gh-project-conventions` requires ("Picking it back up"), which also states what that read takes. Reading that directory is a read like any other and doesn't make this skill a writer.

## Readiness and sort (canonical formula — see gh-project-conventions)

What counts as a candidate is the **Readiness** paragraph of `gh-project-conventions/SKILL.md`, and is not restated here. What this section owns is the sort below, and the one online-only exclusion that narrows the candidate set further.

**Online, an item whose `blockedBy.totalCount` exceeds the number of `blockedBy.nodes` returned for it is not a candidate** (`gh-project-conventions/reference.md`, "Shared limitations"): the formula's open-blocker count is read off those `nodes`, so for such an item "no open blockers" would mean only "none among the ones that came back." Name it, both numbers, and why it was excluded, on its own line of the report. Handing out a possibly-blocked item as ready costs more than offering the next one down — this skill's whole output is one item someone is about to start.

**That exclusion has no offline equivalent**, because `totalCount` is a live-query value and nothing in the mirror stands in for it (same section). The offline path does not paper over it either: its banner says that its blocker lists are what the last sync could see, capped at 50 per item, and that an item with more blockers than that cannot be told apart from a fully-listed one from `backlog/` alone.

Sort ready candidates, in order:

1. `Priority`: `P0` → `P1` → `P2` → `P3` → missing (missing sorts after `P3`, never before `P0`).
2. Milestone due-date proximity: nearer due date first. An item with *any* milestone ranks above one with none, regardless of how far off that milestone's due date is. No milestone sorts last within its priority tier.
3. Issue number ascending, older first — final tie-break.

Use the issue number itself (not `createdAt`) as the tie-break criterion, online and offline alike: numbers are assigned sequentially at creation, so ascending number order *is* creation order, and — unlike `createdAt`, which can tie to the second between two issues filed close together — an issue number is always unique, so this tie-break can never itself produce a tie between two numbered items.

**Online**, this is `content.number` from the query above.

**Offline**, the mirror (`backlog/<number>-<slug>.md`) encodes it directly as `github_issue`. A `backlog/pending/*.md` draft has no issue number yet (`github_issue: null`): it sorts after every numbered item in its priority tier (an issue that doesn't exist yet can't be older than one that does). Break ties among multiple pending drafts by filename (slug), ascending — and this ordering is well-defined because two pending drafts can never share a slug: `gh-project-conventions`' slug algorithm appends a numeric suffix (`-2`, `-3`, …) on collision, so filenames in `backlog/pending/` are unique by construction.

## Online algorithm

1. Read `backlog/gh-project.json` for `owner`, `repo`, `projectNumber`.
2. Paginate `items` inside the Project — never assume the whole backlog fits one page:

```graphql
query($login: String!, $number: Int!, $cursor: String) {
  user(login: $login) {                    # organization($login:) — see step 3
    projectV2(number: $number) {
      items(first: 50, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          status: fieldValueByName(name: "Status") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          priority: fieldValueByName(name: "Priority") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          content {
            ... on Issue {
              number
              title
              url
              createdAt
              repository { nameWithOwner }
              milestone { title dueOn }
              blockedBy(first: 50) { totalCount nodes { number title state } }
            }
          }
        }
      }
    }
  }
}
```

Loop while `hasNextPage`, accumulating every page before filtering or sorting (`gh-project-conventions/reference.md`, "Shared limitations") — here, a later page can still hold the winning candidate.

3. **Root selector:** resolve `user(login:)` vs `organization(login:)` once per run, before the paginated query (`gh-project-conventions/reference.md`, "Shared limitations").
4. Apply the Scope filter (`gh-project-conventions`) to the accumulated nodes.
5. Compute candidates and sort per the section above, excluding any item whose `blockedBy.totalCount` exceeds the number of `blockedBy.nodes` returned for it, per the truncation rule in "Readiness and sort".
6. No candidates → see "No candidates" below. Otherwise report the top candidate (see Output).

Verified 2026-08-31 (gh v2.98.0) against a live scratch Project (`Naymi/nats-auth-unified`) seeded with 8 issues: two `P1` items where only one carried a milestone, one item blocked only by a *closed* blocker, one item blocked by an *open* blocker, one item with no `Priority` set, plus pagination forced with `first: 3` across 3 pages. `hasNextPage`/`endCursor` round-tripped correctly; the computed candidate set and sort order matched the rules above exactly.

## Offline algorithm

1. Run the shared online/offline check (`gh-project-conventions`). Any failure (network or auth) → this path.
2. Read every `backlog/*.md` and `backlog/pending/*.md` file's frontmatter (skip `backlog/gh-project.json` and `backlog/README.md` — not item mirrors). A pending draft's `status` is always `pending-sync`, never `Todo`/`In Progress`/`Done`: treat `pending-sync` as `Todo` for candidacy — it exists specifically to become a real Issue, so it's neither done nor in progress.
3. Compute candidates from `status` and `blocked_by[].status` exactly as online — `blocked_by[].status != "Done"` counts as an open blocker, same "a closed blocker doesn't count" rule — with the one exception the section above names: the truncation exclusion has no offline equivalent, because nothing in the mirror says whether `blocked_by` was written complete. Sort per the section above.
4. Report the same result as online (see Output), prefixed with:

   `OFFLINE — data as of <oldest updated_at among every file read in step 2>`
   `Blocker lists are as of that sync and capped at 50 per item — an item with more blockers than that cannot be told apart from a fully-listed one here.`

   Compute the oldest across *all* read files, not just the candidates or the winner — a stale blocker mirror is exactly what this banner exists to surface.

Verified by hand against a fixture of 6 `backlog/*.md` mirrors + 1 `backlog/pending/*.md` draft: two `P1` items where only one had a milestone (the milestone-bearing one sorted first), one item blocked only by a `status: Done` blocker (counted ready), one item blocked by a `status: Todo` blocker (excluded), one item with no `priority` (sorted last), one `P3` pending draft with `github_issue: null`. Computed order matched the rule set exactly; the oldest `updated_at` across all 7 files (which belonged to a mid-ranked candidate, not the winner) correctly became the banner's timestamp.

## No candidates: show the blocker, not "nothing to do"

If there are zero candidates, don't report "nothing to take." Instead, find the highest-priority `Todo` item using the same sort from the section above (priority → milestone → issue number — the offline path still uses `github_issue`/slug as that tie-break here too), applied to *all* `Todo` items now (not just unblocked ones), and report it as blocked — naming every one of its open blockers by number and title, not just the first:

> No ready item. Highest-priority blocked item: **#48 F-p0-blocked-by-open** (P0) — blocked by **#47 E-blocker-target-open** (open). Resolve that first.

The same cap applies to that list: if the reported item's `blockedBy.totalCount` exceeds the `nodes` returned, say so alongside the blockers you did name, so "resolve that first" isn't read as the complete set of what stands in the way.

**Two different things can land an item here, and they don't get the same sentence.** One is the ordinary case above: the item has open blockers among the `nodes` that came back, so name them. The other is the truncation exclusion from "Readiness and sort" — every returned blocker is closed, and the item was held back only because `totalCount` exceeds those `nodes`. There is nothing to list after "blocked by" for that one, and printing the line above with an empty list states the opposite of what happened: it reads as "blocked by nothing," which would make it a candidate. Give it its own wording, naming both numbers and what is unaccounted for:

> No ready item. Highest-priority item not offered: **#48 G-p0-many-blockers** (P0) — GitHub reports 62 blockers on it and only 50 came back; all 50 are closed, so the other 12 are unknown and this item can't be called ready. Check those by hand.

An item can be in both cases at once — open blockers among the 50 *and* a truncated page. Then it takes the ordinary line plus the cap sentence, which is what the paragraph above already covers.

**If there are no `Todo` items at all**, this fallback has nothing to walk, and it says so plainly — "no ready item and no blocked one either: nothing here is in `Todo`" — rather than falling silent on a state it has no line for. On the offline path, name the likely cause with it: a `backlog/` whose mirrors all carry `status: null` is what a fresh `gh-project-setup` migration leaves behind until the first `gh-project-sync` (`gh-project-conventions/reference.md`, the price of writing `null`), and that is a different situation from a Project with genuinely no `Todo` work in it.

## Output

Report, in order: issue number, title, URL, `Priority` (or "unset"), milestone + due date if any, and a one-line reason ("no open blockers, highest priority ready"). For the offline path, prepend the `OFFLINE —` banner before anything else — never let an offline answer look indistinguishable from a fresh online one. Then, after all of the above and last in the report, the open-questions section `gh-project-conventions` requires ("Picking it back up") over what the Overview's read found — dropped only when that directory was empty.

## Gotchas (verified 2026-08-31, gh v2.98.0)

- Always filter on the `Status` field value, never on the Issue's open/closed state, for `Status == Todo` (`gh-project-conventions`, "Why no skill writes `Status`", for why the two can disagree). Blocker openness is the opposite: always read `blockedBy[].state` (the *Issue's* open/closed state), never the blocker's own Project `Status` — a blocker may not even be in this Project.
- An item whose `priority` comes back `null` is unprioritized, not a failed query (`gh-project-conventions/reference.md`, "Shared limitations") — it is still a candidate, and it sorts after `P3` per the sort above.

## Scope

Read-only: never calls a mutation, never writes `backlog/gh-project.json` or any mirror file. Issue-type items only, current repository only (see `gh-project-conventions`). Does not resolve blockers, set `Priority`, or create anything — that's `gh-project-capture`, `gh-project-sync`, or a human's job.
