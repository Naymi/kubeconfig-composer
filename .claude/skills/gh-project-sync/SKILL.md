---
name: gh-project-sync
description: Use when reconciling this repository's local backlog/ file mirror against GitHub — promoting backlog/pending/*.md drafts into real Issues, refreshing stale or diverged synced mirrors, generating mirrors for live items that lack one, and deleting mirrors for Done or vanished issues. Online reconciles fully; offline (or with no backlog/gh-project.json at all) reports pending-draft counts and mirror ages without any network call beyond the reachability check. Not the same as gh-project-audit, which checks the Project graph against itself rather than backlog/ against GitHub.
---

# gh-project-sync

## Overview

The local↔remote boundary for the `gh-project-*` family: makes `backlog/` match GitHub, in one direction (GitHub → file, except pending-draft promotion, which is file → GitHub exactly once). Three jobs, always in this order when online:

1. Promote `backlog/pending/*.md` drafts into real Issues, in dependency order.
2. Refresh every synced mirror whose Issue changed since last sync, overwrite any mirror whose content has otherwise diverged, and generate one for any live item that has no mirror yet.
3. Delete mirrors for Issues that are now `Done`, or that no longer show up at all.

**Read `gh-project-conventions` (the `gh-project-conventions/` directory next to this one in the same skills folder: `SKILL.md` + `reference.md`) before running this** — it owns the field schema, the `backlog/gh-project.json`/mirror/pending-draft shapes, the slug algorithm, the GraphQL mutation signatures, and the online/offline check. This skill only adds the reconciliation procedure below.

**Not `gh-project-audit`.** `gh-project-audit` checks whether the graph *inside* GitHub is internally correct (cycles, missing Priority, schema drift) — it never looks at `backlog/`. `gh-project-sync` never checks graph correctness — it only asks "does `backlog/` still match what's on GitHub right now." A blocker cycle *inside GitHub* is `gh-project-audit`'s finding; a cycle *among still-pending drafts* (below) is this skill's problem, because nothing on GitHub exists yet to detect it.

**If `backlog/gh-project.json` is missing, or present but its `projectNumber` doesn't resolve:** don't attempt promotion or any remote comparison. Tell the caller to run `gh-project-setup` first, then still give the report-only output described in §4 ("No project" variant) — a pile of undealt-with pending drafts is worth surfacing even before a Project exists to promote them into.

## 0. Resolve mode

1. Read `owner`, `repo`, `projectNumber` from `backlog/gh-project.json`. Confirm it resolves: `gh project view <projectNumber> --owner <owner> --format json`.
   - Missing file, or present but this fails → **§4, "No project" variant.** Stop after reporting; never attempt §1–§3.
2. Shared online/offline check (`gh-project-conventions`, e.g. `gh auth status`, short timeout).
   - Fails → **§4, "Offline" variant.**
3. Both resolve and reachable → **§1 → §2 → §3, in that order**, then report what happened.
4. Whatever the mode resolves to: read `backlog/open-questions/` before starting the work that mode calls for, as `gh-project-conventions` ("Picking it back up") specifies, and end this run's report — §1–§3's, or either §4 variant's — with the open-questions section `gh-project-conventions` requires ("Picking it back up"). Drop it only when that directory is empty.

## 1. Promote `backlog/pending/*.md`, in dependency order

### 1a. Build the dependency graph

Read every `backlog/pending/*.md` file's frontmatter. A `blocked_by` entry shaped `{slug: "...", title: "..."}` (per `gh-project-conventions/reference.md`'s pending-draft shape) is an edge to another **still-pending** draft; a `{number: N, title, status}` entry references an already-synced issue and needs no ordering — its blocker already exists, `addBlockedBy` can fire the moment this draft is created.

Treat this as a standard "B must be promoted before D" dependency graph — draft `D` depends on draft `B` for every pending-slug entry in `D.blocked_by`. Run **Kahn's algorithm** (repeatedly promote every draft whose remaining pending-dependency count is zero, then decrement its dependents), not a naive DFS chain-walk — Kahn's handles a diamond correctly by construction: if `A` blocks both `B` and `C`, and both `B` and `C` block `D`, then `A` (in-degree 0) promotes first, dropping both `B` and `C` to in-degree 0 (order between them doesn't matter — process alphabetically by slug for a reproducible run), and only then does `D` reach in-degree 0 and promote. A DFS-chain approach risks visiting `D` through the `B` branch before `C` has run, and either double-promoting the wait or promoting `D` too early with only one of its two blockers resolved.

Tie-break among simultaneously-ready drafts: **alphabetical by slug** — same tie-break rule `gh-project-next` uses for pending drafts, kept consistent so a run's order is predictable from the file listing alone.

**Dangling reference:** a `blocked_by` slug that matches neither another pending draft nor an existing synced mirror (`backlog/<number>-<slug>.md`) is a broken reference, not a dependency — report it by name, skip promoting *that* draft this run (leave its file in `pending/`), and continue with everything else. Don't let one bad reference stall the whole batch.

> `backlog/pending/add-retry-backoff-to-leafnode-reconnect.md` references `blocked_by: {slug: "does-not-exist"}`, which matches no pending draft or synced mirror — skipping promotion for this draft; fix or remove the reference and re-run

A `{number: N}` reference that doesn't resolve to a real issue is a different flavor of dangling reference — it needs no pre-promotion lookup (an already-synced issue is assumed to exist), so it isn't caught here; it surfaces naturally when the resulting `addBlockedBy` call fails during promotion (§1c).

### 1b. Cycle among pending drafts — hard error, not silently broken

After Kahn's algorithm finishes, any draft still at nonzero in-degree is part of a cycle (there may be more than one, disjoint, in the same run — report each separately). For each cycle found:

- List every slug in the cycle and, for each, the specific `blocked_by` entry causing the edge — the human needs to see exactly which file to edit, not just that "a cycle exists."
- Print the chain starting from its alphabetically-first slug, same fixed-starting-point convention `gh-project-audit` uses for blocker cycles, so a repeated run reports the identical string:

  > Cycle among pending drafts: `add-retry-backoff` → `fix-flaky-webhook-test` → `add-retry-backoff` (backlog/pending/add-retry-backoff.md's blocked_by references fix-flaky-webhook-test; backlog/pending/fix-flaky-webhook-test.md's blocked_by references add-retry-backoff — edit one of these two files to break the cycle)

- **Do not promote any draft that is part of a reported cycle**, even if breaking the tie arbitrarily would "work" — resolving it is the human's call (same principle `gh-project-audit` applies to a live `blockedBy` cycle). Every draft *outside* the cycle still promotes normally in its own topological slot.

### 1c. Promoting one draft

Once a draft's turn comes up in the topological order, the create → add-to-Project → set-`Priority` sequence is the same live path `gh-project-capture` uses (`gh-project-capture#2-live-path`, steps 2–4), with two differences: `Priority` here is never judged fresh — read it straight from the draft's own `priority` frontmatter field (already judged at capture time, per `gh-project-conventions`'s pending-draft shape); and once the Issue exists, resolve **every** `blocked_by` entry via `addBlockedBy` (one call per blocker, `gh-project-conventions/reference.md`) — for a `{number: N}` entry, use `N` directly; for a `{slug: "..."}` entry, use the issue number obtained when *that* draft was promoted earlier in this same run (guaranteed to already exist, by construction of the topological order). **When a draft has more than one blocker, fire the `addBlockedBy` calls in ascending resolved-issue-number order** — an arbitrary but fixed rule, purely so a repeated run of the same promotion produces the same call sequence; the mutations aren't order-sensitive to each other, only the sequence needs to be deterministic.

Same failure-handling principle as `gh-project-capture`: once `gh issue create` succeeds, the finding is durably captured — a downstream failure (Project add, `Priority`, a specific `addBlockedBy` call) is retried once and then reported as exact manual-remediation commands, never rolled back and never re-routed to `pending/` (that would violate the pending draft's `github_issue: null` contract). For a `Project add`/`Priority` failure, the remediation commands are exactly `gh-project-capture#2-live-path` step 4's fallback (`gh project item-add` + `updateProjectV2ItemFieldValue`). For a failed `addBlockedBy` — including one that fails because a `{number: N}` reference doesn't resolve to a real issue (a dangling numeric reference, the counterpart to §1a's dangling-slug check, surfaced here rather than during graph-building since a synced-issue reference needs no pre-promotion lookup) — report the raw mutation so it can be run by hand once the target is fixed or reachable:

```bash
gh api graphql -f query='mutation{addBlockedBy(input:{issueId:"<new draft issue node id>" blockingIssueId:"<blocker issue node id>"}){issue{number}}}'
```

naming both issue numbers and, for the dangling-reference case, that the blocker number in the draft's frontmatter doesn't resolve — the human needs to fix the reference, not just retry the command.

**Once the Issue exists and is in the Project, write the mirror — don't wait for the rest of the sequence to succeed.** Same principle `gh-project-capture#2-live-path` states for its own steps 4–6 ("independent, best-effort, and unconditional once the issue exists ... always reach step 6 and write the mirror"): a failed `Priority` write or a failed `addBlockedBy` is reported as remediation, not a reason to leave the draft unpromoted on disk. Generate the synced mirror at `backlog/<number>-<slug>.md` (same slug, issue number now in front — `gh-project-conventions/reference.md`), using the real `updatedAt` from the just-created Issue, and — for every other field — **only what actually landed on GitHub** (`gh-project-conventions/reference.md`'s frontmatter rules): `status: null`, because this skill never sets `Status` and never read it back ("Never invent `Todo`", ibid.), and it buys no read-back for it either — §3 completes this mirror against live data before the same run is over; the draft's `priority` **if the field write succeeded**, `null` if it failed; and `blocked_by` denormalized from the `addBlockedBy` calls that actually landed (state for a `{number}`-based blocker carries forward as already known; state for a freshly-promoted former-pending blocker is `Todo`, since it was just created). **A blocker whose `addBlockedBy` failed does not go into `blocked_by`** — the link doesn't exist on GitHub, so writing it here would make §3 regenerate a mirror without it and report a divergence every run until someone noticed. The reported remediation command is what creates the link; the next sync picks it up on its own. Then delete `backlog/pending/<slug>.md`. This is a logical move, not an atomic filesystem rename — the mirror is written first, the pending file removed only after that write succeeds, so a crash mid-step leaves the pending file present rather than losing the record.

**A mirror written here is *provisional* until this run ends: §2a never deletes it (a promoted item may simply not be indexed yet), §2b never regenerates it, and §3 completes it — filling fields the live read returns `null` for, never replacing a value §1c watched land — and reports nothing, because the difference is this run finishing its own write rather than drift.** §1c writes before the item has ever been queried, so `status` is `null` by rule and a failed `Priority` is `null`, neither of which is what the live item will report seconds later. Carry forward **the set of issue numbers §1 promoted during this run** — that set is what all three exemptions key off. From the next run a promoted mirror is an ordinary one: deleted if it stops appearing, generated if it is missing, and any divergence in it reported normally.

Both halves of that contract answer one fact: **Projects v2 does not index a newly added item instantly, and §2 runs seconds after §1.** In the lag's first form the item hasn't come back at all, which lands it in exactly §2a's "not in `live_by_number`" case — and deleting there destroys the mirror §1c wrote moments earlier, after §1c has already removed the draft from `pending/`, leaving nothing on disk for an Issue that exists on GitHub while the run reports it as "no longer appears in this Project." In the second form the item does come back: it is indexed, its field values are not. What arrives then is `null`, indistinguishable from the `null` of a genuinely unset field (see Gotchas) — and §1c watched that `Priority` land on GitHub seconds earlier, which is the stronger of the two claims. Overwriting with that `null` would destroy a value this run put on disk, and since the report is suppressed for exactly these numbers, destroy it with nothing said. Filling in errs the recoverable way: a `null` left on disk is completed by any later run, a value thrown away is gone.

Gating the mirror on the *whole* sequence is what makes a partially-promoted draft dangerous: the Issue is live and in the Project, nothing on disk records that, and the draft sits in `pending/` waiting to be promoted a second time. §2b would then generate the mirror anyway and report it as a GitHub-side item nobody had captured — a cheerful line directly under the failure it contradicts.

**The one case that still leaves the draft in `pending/`: `gh issue create` succeeded but the Project add failed.** No mirror is written here — this is the one branch of §1c that produces no provisional mirror — because the item isn't in the Project: §2's query won't return it, so the mirror would keep §1c's blind values with nothing to complete them, and the next run's §2a would delete it as "no longer appears." Leave the draft where it is, and say plainly in the report that **re-running `gh-project-sync` while this draft is still in `pending/` will create a second Issue for it** — the draft is still promotable and carries no record of the Issue that already exists.

Two things have to happen before the next run, and the report has to name both, because doing only the obvious one does not help. Applying the reported `gh project item-add` puts the existing Issue back into the Project, and nothing else: `backlog/pending/<slug>.md` is untouched, so §1 promotes it again into a second Issue, and §2b — now seeing the first Issue live with no mirror — generates a mirror for that one too. Two Issues and two mirrors for one finding, which is the outcome the paragraph above exists to prevent. So: apply the remediation **and** move the draft out of `pending/`. The finding isn't lost by moving it — once the Issue is back in the Project, §2b generates its mirror on the next run.

**Concurrency:** if another `gh-project-sync` run already promoted this same draft (the pending file is already gone by the time this run reaches it), treat that as a no-op, not an error — per `gh-project-conventions`'s design, promotion ending in a file move is exactly what makes this safe without locking.

## 2. Refresh and delete — one paginated pass

Both jobs need the same live snapshot of every in-scope Project item, so do them in one query pass rather than two.

```graphql
query($login: String!, $number: Int!, $cursor: String) {
  user(login: $login) {                    # organization($login:) per the owner.type check
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
              body
              updatedAt
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

Loop while `hasNextPage`, accumulate every page before filtering, and resolve the `user`/`organization` root selector once per run (`gh-project-conventions/reference.md`, "Shared limitations"). Then apply the Scope filter (`gh-project-conventions`). Unlike `gh-project-next`'s query, this one is **not** filtered to `Status == Todo` — a `Done` item has to appear here too, or §2a below can't tell "Done" apart from "vanished." Where an item's `blockedBy.totalCount` exceeds the number of `nodes` returned, this run writes that mirror's `blocked_by` incomplete (`gh-project-conventions/reference.md`, "Shared limitations"): name the item and both numbers in the report, and say with it that §3 cannot settle such a mirror afterwards.

Build `live_by_number`: a map from `content.number` to that node's data, over the filtered set.

### 2a. Walk both sides — mirrors on disk and live items

This pass covers the **union** of two sets: every mirror file on disk, and every in-scope number in `live_by_number`. A number on both sides is an ordinary synced mirror, resolved below; a number with a file but no live entry is a deletion, also below; a number that is live but has no file is §2b's job. Walking only the disk — as this section did until the union was introduced — is precisely how an item stays invisible to `backlog/` indefinitely, because nothing on disk points at what is missing from it.

For every `backlog/<number>-<slug>.md` on disk (exclude `backlog/README.md`, `backlog/gh-project.json`, and everything under `backlog/pending/` and `backlog/open-questions/`), parse `<number>` from the filename (leading digits before the first `-`):

- **`<number>` not in `live_by_number`, or `live_by_number[<number>].status.name == "Done"`** → delete the mirror. Both cases collapse to one action but are worth naming separately: the first covers an Issue that's gone from this Project's in-scope view (deleted, unlinked, or moved out of the repo by hand), the second is the documented "Done items lose their mirror" rule (`gh-project-conventions/reference.md`). Report each deletion, one line per file, in this shape:

  > Deleted `backlog/64-add-retry-backoff-to-leafnode-reconnect.md` — issue #64 is `Done`
  > Deleted `backlog/63-add-structured-logging-to-gateway-startup.md` — issue #63 no longer appears in this Project (removed from scope, unlinked, or deleted)
- **Otherwise** → run the staleness/divergence check, §3 below.

**Never delete a provisional mirror** — a number §1 promoted during this run is exempt from the deletion branch above until this run ends, whatever §2's query returned (§1c). Say so in the report rather than staying silent, since it means this mirror carries §1c's blind values for one more run:

  > `backlog/42-add-a-convergence-oracle.md` kept — issue #42 was promoted in this run and hasn't shown up in the Project query yet (indexing lag, not a deletion); its `status`/`priority` fill in on the next sync

### 2b. Generate a mirror for a live item that has none

A number in `live_by_number` that is not `Done`, and for which no `backlog/<number>-*.md` exists, gets its mirror generated now — same generation path §3 uses, slug per `gh-project-conventions/reference.md`. This is the Issue created straight in the GitHub UI, or one decomposed into sub-issues there: it never passed through `gh-project-capture` or draft promotion, so nothing ever wrote its file.

"Not `Done`" here means `status.name != "Done"` **including `status` being `null`** (`gh-project-conventions/reference.md`, "Shared limitations"). That item needs a mirror as much as a `Todo` one does: write `status: null` into it, per that file's frontmatter rules, and print "no Status" where the report line below names the status.

**Skip any number §1 promoted during this same run** — its mirror is provisional (§1c), not missing. Generating one here would duplicate §1c's write, or — in the one failure case §1c leaves open — bury §1c's error report under a line claiming nobody had ever captured the item.

Report each one:

> Generated `backlog/42-add-a-convergence-oracle.md` — issue #42 (`Todo`) is a live Project item that had no mirror
> Generated `backlog/43-pin-the-mirror-byte-layout.md` — issue #43 (no Status) is a live Project item that had no mirror

A `Done` item without a mirror is correct and stays silent: §2a deletes mirrors once an item is `Done`, so generating one here only to delete it next run is churn, not coverage.

This is the one place the skill writes a file GitHub never asked it to write, and it stays safe for the same reason the rest of the pass does — the content comes entirely from the live query, and nothing is sent back to GitHub.

## 3. Detecting "stale" vs "diverged" vs "unchanged"

**A provisional mirror never reaches this check as a divergence** — that is, a number §1 promoted during this same run (§1c). Regenerate its mirror from the live data and overwrite, but report nothing. (If the number isn't in `live_by_number` at all, §2a's exemption already applies and there is nothing to regenerate.)

**For these numbers, and only these, the regeneration fills the file in rather than replacing it:** a field the live read returns as `null`, or a `blocked_by` it returns empty, keeps what §1c wrote to disk; only a value that is actually there overwrites one. §1c's contract states why.

Everything below is about mirrors this run did not write.

No full snapshot of the previous generation is stored anywhere — the mirror format (`gh-project-conventions/reference.md`) carries only the single `updated_at` timestamp, not a hash or a copy of prior content. The check is staged so a content comparison is only ever needed as a second-order confirmation, never as the primary signal:

1. Read the mirror's on-disk frontmatter `updated_at` (call it `local`). Take `live = live_by_number[<number>].content.updatedAt`.
2. **`live != local`** → **stale**, and only ever stale — GitHub's own `updatedAt` only advances on a real change to that Issue, so a mismatch here is fully explained by a legitimate remote edit. Regenerate the mirror from the live data in step 2's query result and overwrite, no flag raised, no content comparison needed. (In the pathological case `live < local` — clock skew or a support-side revert — still just resync and note the anomaly once in the report; not worth a special code path.)
3. **`live == local`** → GitHub says the *Issue* hasn't changed since this mirror was last written. Regenerate the mirror in memory from the live data still in hand and compare, byte-for-byte, to the on-disk file:
   - **Identical** → already correct, skip the write entirely (no churn, nothing to report).
   - **Different** → **diverged**. Overwrite with the freshly generated content (the mirror format is one-directional — `gh-project-conventions` — never merge) and report it, naming the fields that moved:

     > `backlog/66-document-the-callout-bootstrap-sequence.md` diverged from GitHub (issue #66's `updatedAt` hasn't moved, but on-disk content no longer matches a fresh regeneration) — overwriting with the regenerated version; the divergence is in `blocked_by`

     **Do not report this as a hand edit.** `updatedAt` belongs to the Issue, not to everything the mirror carries: `addBlockedBy`/`removeBlockedBy` change what `blocked_by` must say without advancing the blocked Issue's `updatedAt` at all, so a mirror falls behind its own blocker list with both timestamps sitting still. A genuine local edit produces an identical signal, and nothing in the data separates the two — so name the divergence, say which fields moved, and let the diff carry the explanation instead of accusing a human who may never have opened the file.

     A divergence confined to `blocked_by` or `milestone` is that lag, and it earns its own line in the report for a reason beyond bookkeeping: a mirror claiming `blocked_by: []` while GitHub holds open blockers makes offline `gh-project-next` hand out a blocked item as ready to start.

This is why the comparison is anchored on `updated_at` rather than a stored content hash: staleness (step 2) is resolved without ever touching file content, and the one case that does need a content comparison (step 3) recomputes the "previous" version on the spot from live data instead of persisting it anywhere.

Regeneration itself follows the mirror format (`gh-project-conventions/reference.md`) exactly: `blocked_by[].status` maps each blocker's Issue `state` the same way `gh-project-capture` does (`CLOSED → Done`, `OPEN → Todo`); `milestone` is `null` or `{title, due_on}` from `content.milestone`; `status` and `priority` are those fields' names or `null` if unset (never omitted); body is `content.body`, normalized and terminated per that file's byte-exact assembly rules — CRLF converted, trailing newlines collapsed to exactly one at end of file. Getting that termination wrong is not a cosmetic slip: it makes step 3 below report a divergence on every run, forever.

## 4. Report-only variants (no promotion, no writes)

### Offline (project resolves, GitHub unreachable)

No network calls beyond the reachability check itself. Report:
- Count of `backlog/pending/*.md` drafts waiting, and — best-effort, since a cycle can't be confirmed without also checking dangling references, both of which are cheap and local — run §1a/§1b's graph build offline too and surface any cycle or dangling reference found, so it doesn't wait for a future online run to be noticed.
- For every synced mirror, its `updated_at` age (`now - updated_at`), sorted oldest-first. Label this clearly as **age since last known sync, not verified against live GitHub** — offline, there's no way to tell a mirror that's merely old from one that's actually stale; only an online run (§3) can tell the difference.
- Last, the open-questions section every report ends with (§0 step 4).

### "No project" (missing or unresolvable `backlog/gh-project.json`)

Same report items as the offline variant above (pending-draft count/graph issues, synced-mirror ages, the closing open-questions section), plus an explicit instruction to run `gh-project-setup` before anything here can promote or refresh. This is independent of network reachability — even if GitHub is reachable, promotion needs `owner`/`projectId`/`priorityFieldId`/etc. from `backlog/gh-project.json`, so a missing pointer stops promotion regardless.

## Gotchas (verified 2026-08-31, gh v2.98.0)

- §2a's Done-detection reads the `Status` field value, never the Issue's open/closed state (`gh-project-conventions`, "Why no skill writes `Status`", for why the two can disagree).
- The reverse direction, however, can happen, and this gotcha is where `gh-project-conventions` ("Why no skill writes `Status`") got it observed: this account's default Project automation ("Item closed" ↔ "Status: Done" kept in sync) closed the underlying Issue as a side effect of `updateProjectV2ItemFieldValue` setting `Status` to `Done` directly via the API, not just via the UI. This doesn't change §2a's logic (it only ever reads `Status`, never open/closed state) but explains why a `Done` test item may also show up `CLOSED` in another item's `blockedBy` list — as observed live, see below — and why its own `updatedAt` advances at the same moment `Status` does.

**Verified live** (2026-08-31, gh v2.98.0) against a scratch Project (`Naymi/nats-auth-unified`, project #6, deleted after verification) and five scratch Issues (#63–#67, all closed after verification, titles prefixed `[gh-project-sync dry-run]`):

- Seeded `backlog/pending/` with a 4-draft diamond (A blocks B and C; B and C both block D) plus a separate 2-draft cycle (E blocks F, F blocks E), promoted by hand following §1 exactly. Kahn's algorithm promoted A (#63) first, then B (#64) and C (#65) — alphabetical tie-break, matching filename order — then D (#66), whose two `addBlockedBy` calls landed against B and C's real issue numbers in ascending order as specified. E and F were correctly left untouched in `pending/`, with the cycle detectable exactly as the §1b report format describes (alphabetically-first slug leads the chain).
- Ran §2's paginated query and applied §2a/§3 by hand to the four promoted mirrors plus one untouched baseline (G, #67): A had been removed from the Project (`deleteProjectV2Item`) → its mirror correctly deleted as "no longer appears" (not a `Done` case). B's `Status` was set to `Done` → its mirror correctly deleted, with its `Issue.state` observed `CLOSED` per the automation gotcha above. C's title was edited on GitHub (`updatedAt` advanced, frontmatter `updated_at` unchanged) → correctly identified as **stale** and regenerated silently, no flag. D's mirror body was hand-edited on disk with no corresponding GitHub change (`updatedAt` unchanged both sides) → correctly identified as **diverged** (in-memory regeneration differed from on-disk content) and overwritten, with the on-disk hand-edit sentence gone from the result. G was left completely untouched (both timestamps equal, in-memory regeneration matched on-disk byte-for-byte) → correctly a no-op, confirming the "already correct, skip the write" branch actually triggers and doesn't just fall through to an unnecessary rewrite.

## Scope

Never mutates `Status`, `Milestone`, or an existing item's `Priority` — a promoted draft's `Priority` is set once, at creation, from its own frontmatter; nothing about an already-synced item's fields is ever changed, only its mirror file. Generates a mirror for a live non-`Done` item that lacks one (§2b), reading GitHub only — it never creates or changes anything on the GitHub side to do so. Never breaks a cycle — pending or live — automatically; always a reported, human-resolved condition. Never runs any part of §1–§3 without both `backlog/gh-project.json` resolving and GitHub reachable (§4 covers every other case). Never promotes, refreshes, or deletes anything under `backlog/open-questions/` — an unanswered schema-fit question (`gh-project-conventions`) is not a draft, and promoting one would settle a question the user never answered. They reach the user through the open-questions section this run's report ends with (§0 step 4), and stay on disk until whoever asked acts on the answer.
