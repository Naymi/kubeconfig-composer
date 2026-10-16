---
name: gh-project-audit
description: Use when checking whether this repository's GitHub Project (v2) backlog graph is internally correct — missing Priority, blockedBy cycles, a Done parent with open sub-issues, the Priority field's options having drifted from the canonical set, or merged work an item still doesn't show as Done. Online only, read-only — it reports, it never mutates the Project graph. Not the same as gh-project-sync, which checks backlog/ against GitHub rather than GitHub against itself. Requires backlog/gh-project.json (from gh-project-setup) to already exist.
---

# gh-project-audit

## Overview

Checks the correctness of the Priority/blocker/milestone/sub-issue graph **inside** GitHub for this repository's Project (v2) — not against `backlog/`, against itself. Online only: if GitHub isn't reachable there is nothing to audit, so this skill does not fall back to `backlog/*.md`. Read-only: it never calls a mutation, sets a field, breaks a block, or edits a file.

**Read `gh-project-conventions` (the `gh-project-conventions/` directory next to this one in the same skills folder: `SKILL.md` + `reference.md`) before running this** — it owns the field schema, the canonical `Priority` option set (`P0`/`P1`/`P2`/`P3`), and the online/offline check. This skill only adds the six checks below and how to compute them.

**Not `gh-project-sync`.** `gh-project-sync` is about the local↔remote boundary — stale mirrors, pending drafts waiting to be promoted, whether `backlog/` still matches GitHub. `gh-project-audit` never opens a `backlog/<number>-<slug>.md` mirror at all — it reads `backlog/gh-project.json` for the pointer and, for its report, the open questions under `backlog/open-questions/` (§1 step 5), and nothing else; it is entirely about whether the graph on GitHub is self-consistent.

**If `backlog/gh-project.json` is missing, or present but its `projectNumber` doesn't resolve:** stop and tell the caller to run `gh-project-setup` first. Never bootstrap a Project yourself.

## The six checks

1. **Needs triage** — `Status` is `Todo` or `In Progress` and `Priority` is unset. **Not** flagged on `Status == Done` — an item can finish without ever having been prioritized; that's an accepted terminal state per `gh-project-conventions`, not a gap.
2. **Blocker cycle** — a directed cycle in `blockedBy` (A blocked by B, B blocked by A, or a longer chain back to A). Flagged only, never broken — resolving a cycle is a human call, not something this skill decides.
3. **Done parent, open sub-issues** — an item's own `Status` is `Done` but it has one or more sub-issues whose Issue `state` is still `OPEN`.
4. **Priority schema drift** — the live `Priority` single-select field's option set no longer matches the canonical `{P0, P1, P2, P3}` from `gh-project-conventions` (someone renamed, added, or removed an option by hand in the Project UI). Report citing `gh-project-conventions` as the source of truth — this skill does not decide what the options *should* be, only that they've diverged.
5. **At risk (advisory, not an error)** — `Status == Todo`, `Priority` is not `P0`/`P1`, and the item's milestone due date is within the risk window (default **7 days** from the audit run's current time — a documented default, not a fixed rule; state the window used in the report so the reader can tell if it needs a different threshold for their situation). No milestone → not at risk (nothing to be at risk *of*).
6. **Merged work, item not `Done` (advisory, not an error)** — the item's `Status` is `Todo` or `In Progress` while GitHub itself carries evidence the work landed. This is the *opposite* failure to check 1: not an item nobody ranked, an item nobody's backlog knows is finished. Reported, never fixed — this skill doesn't set `Status` or close an Issue (`gh-project-conventions` owns why). Evidence, strongest first, in §8.

## Scope

**Issue-type items only, current repository only** — the Scope filter every skill in this family applies (`gh-project-conventions`). A PR auto-added to the Project (native `Linked pull requests`/`Reviewers` fields) never appears in any of the six findings above, even if it has no `Priority` — it is out of scope, not compliant.

## Algorithm

### 1. Resolve pointer and root selector, list open questions

1. Read `owner`, `repo`, `projectNumber` from `backlog/gh-project.json`.
2. Confirm it resolves: `gh project view <projectNumber> --owner <owner> --format json`. Failure → stop, tell the caller to run `gh-project-setup` first (see Overview).
3. Shared online/offline check (`gh-project-conventions`). Offline → stop and say so; there is no offline mode for this skill (unlike `gh-project-next`/`gh-project-capture`) — report "GitHub unreachable, nothing to audit" rather than falling back to `backlog/`.
4. Root selector (`gh-project-conventions/reference.md`, "Shared limitations"): read `owner.type` off `<same view call>`'s output — step 2 has already made that call, so this skill pays nothing extra for it.
5. Read `backlog/open-questions/` as `gh-project-conventions` ("Picking it back up") specifies — the three fields the report section is built from, and nothing else in those files. This is the one thing this skill reads under `backlog/` besides the pointer file, and §9's report ends with it. No mirror file is opened on either path.

### 2. Paginate items with the audit-scoped query

```graphql
query($login: String!, $number: Int!, $cursor: String) {
  user(login: $login) {                    # organization($login:) per step 1.4
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
              state
              repository { nameWithOwner }
              milestone { title dueOn }
              blockedBy(first: 50) { totalCount nodes { number title state } }
              subIssues(first: 50) { totalCount nodes { number title state } }
            }
          }
        }
      }
    }
  }
}
```

Loop while `hasNextPage`, accumulating every `nodes` entry before filtering (`gh-project-conventions/reference.md`, "Shared limitations") — here, a later page can hold the winning finding. Then apply the Scope filter above.

`content.state` (the Issue's own open/closed state) is read by **check 6 only**. No other check may touch it — checks 1, 3 and 5 read the `Status` field value, never open/closed state, per the first gotcha below.

`blockedBy` and `subIssues` both request `totalCount` (`gh-project-conventions/reference.md`, "Shared limitations"): for every item, compare it against the number of `nodes` actually returned. Where the two differ, checks 2 and 3 saw only part of that item's graph — name the item and both numbers in the report. A clean result must never imply full coverage of an item that was truncated.

### 3. Compute check 1 — needs triage

For each in-scope item: `status.name` is `"Todo"` or `"In Progress"` and `priority` is `null` — an unset field, not a failed query (`gh-project-conventions/reference.md`, "Shared limitations") → flag.

### 4. Compute check 2 — blocker cycle

Build a directed graph over in-scope items only, using `content.number` as the node key and one edge per `blockedBy` entry: `item.number → blocker.number`. Run standard cycle detection (DFS with a recursion-stack / three-color marking) over this graph; report every distinct cycle found as its ordered chain of issue numbers, once (a 3-cycle visited from any of its three members is the same cycle — dedupe by the cycle's node set, not by which node the search started from). Print the chain starting from its lowest-numbered issue (e.g. a cycle over {82, 80, 81} always prints as `#80 → #81 → #82 → #80`) — an arbitrary but fixed rule, so the same cycle reads identically across repeated runs.

**Known limitation, state it in the report if it applies:** a `blockedBy` target that isn't itself an in-scope Project item (a same-repo issue never added to this Project) is a dead end in this graph — this check cannot see whether *that* issue loops back to something in scope without an extra lookup this skill doesn't make. This is a scope limitation of what's cheaply computable from one paginated query, not a silent gap to paper over — say so rather than implying full-repo cycle coverage.

### 5. Compute check 3 — Done parent, open sub-issues

For each in-scope item where `content.subIssues.nodes` is non-empty: if `status.name == "Done"` and any `subIssues.nodes[].state == "OPEN"` → flag, listing the open sub-issue numbers.

### 6. Compute check 4 — Priority schema drift

```bash
gh project field-list <projectNumber> --owner <owner> --format json
```

Find the field named `"Priority"`, collect its `options[].name` as a set, compare to `{P0, P1, P2, P3}` exactly (same four, no more, no fewer, spelling exact — order doesn't matter). Mismatch → flag, citing `gh-project-conventions` as the canonical source and listing both the live set and the expected set.

### 7. Compute check 5 — at risk

Current time: `date -u +%Y-%m-%dT%H:%M:%SZ`. For each in-scope item where `status.name == "Todo"`, `priority` is not `P0`/`P1` (includes `null`), and `content.milestone.dueOn` is non-null and within 7 days of now (inclusive, already-overdue counts as within the window too) → flag as advisory, naming the milestone and its due date.

**The six checks are independent — evaluate all six against every in-scope item, never stop at the first match.** An unprioritized `Todo` item with a near-due milestone is *both* "needs triage" (check 1) *and* "at risk" (check 5); an item at the head of a `blockedBy` cycle (check 2) that also lacks a `Priority` is *both* check 2 and check 1; an item whose work already merged can be all three of check 1, check 5 and check 6 at once. Report each match under its own check — collapsing an item into a single finding because one check already flagged it silently drops the others.

### 8. Compute check 6 — merged work, item not `Done`

**Scan set:** in-scope items whose `status.name` is `"Todo"` or `"In Progress"`. Nothing else is examined — a `Done` item cannot fail this check, and the scan set bounds the extra queries below to the part of the backlog that claims to be unfinished.

**Tier a — the Issue is already closed.** `content.state == "CLOSED"`. Free: it comes from the §2 query, no extra call. GitHub says the work is closed while the Project still says `Todo`/`In Progress`, which means the "Item closed → Done" automation is off, or was off when it closed, or someone moved `Status` back by hand. Deterministic — report it as a plain finding, not a guess.

**Tiers b and c** need the Issue's cross-reference timeline. One query per scan-set item that survived tier a as still `OPEN`:

```graphql
query($owner: String!, $repo: String!, $num: Int!) {
  repository(owner: $owner, name: $repo) {
    issue(number: $num) {
      number
      state
      timelineItems(first: 50, itemTypes: [CROSS_REFERENCED_EVENT]) {
        pageInfo { hasNextPage }
        nodes {
          ... on CrossReferencedEvent {
            willCloseTarget
            source { ... on PullRequest { number title body merged } }
          }
        }
      }
    }
  }
}
```

Keep only nodes where `source.number` is non-null and `source.merged` is `true`. **A node whose `source` came back as an empty object `{}` is a cross-reference from another Issue, not a PR** — the inline fragment simply didn't match; skip it, it is not an error. `timelineItems(first: 50)` takes its first page only, exactly like `blockedBy`/`subIssues` in §2 — say so in the report if an item's timeline plausibly runs longer (`pageInfo.hasNextPage` is in the query for precisely this).

**Tier b — GitHub recorded a closing link that merged and still didn't close.** `willCloseTarget == true` on a merged PR while the Issue is `OPEN`. Deterministic and rare; the usual cause is the PR merging into a branch other than the default one, where the keyword does nothing.

**Tier c — a closing sentence GitHub never honoured.** The merged PR's body *states that this PR closes this issue*, yet `willCloseTarget` is `false` — because the keyword wasn't one of GitHub's English ones (`close`/`closes`/`closed`, `fix`/`fixes`/`fixed`, `resolve`/`resolves`/`resolved` + `#<number>`). See `gh-project-conventions`, "Why no skill writes `Status`".

**Tier c is a judgment on the sentence, never a keyword grep, and a merged PR that merely mentions the issue is not a finding.** Most PRs reference neighbouring issues in passing; flagging those would bury the real ones. Read the sentence carrying the `#<number>` and ask whether it claims *this PR closes that issue*:

| Sentence in the merged PR's body | Verdict |
|---|---|
| "Закрывает #18 (`todo/13-record-ttl.md`)." | tier c — it claims closure of #18 |
| "Закрывает брейншторм по задаче #16 … и фиксирует ту часть #18, которая влияет на wire-формат" | not a finding for #18 — it closes something else and only touches part of #18 |
| "Первая итерация разреза общая с #16 (формат `expires_at`)" | not a finding — an ordinary mention |

Both rows about #18 above come from the same repository and would be indistinguishable to any regex on "закрыв.*#18". If a sentence is genuinely ambiguous, report it as ambiguous rather than dropping it or asserting closure — the reader has the PR.

### 9. Report

Structure is this skill's own call, with one exception `gh-project-conventions` fixes for every report in the family: the open-questions section that closes this one, below. Otherwise — group by check, each finding naming the issue number, title, URL, and the specific values that triggered it (e.g. the blocked-by chain for a cycle, the open sub-issue numbers for check 3, both option sets for schema drift). One finding, one line, in this shape:

> **#50** "Ship v2 launch page" (https://github.com/<owner>/<repo>/issues/50) — `P3`, milestone "v2 launch" due 2026-09-03 (3 days) → at risk

A check-6 finding names the evidence tier and the PR it rests on, so the reader can act without re-deriving it:

> **#18** "13 — TTL на запись" (https://github.com/<owner>/<repo>/issues/18) — `Todo`, Issue still `OPEN`, but merged PR #24 says "Закрывает #18" and `willCloseTarget` is `false` (tier c: non-English closing keyword) → merged work, item not `Done`

State the risk window used for check 5. If a run hit the check-2, check-6 or nested-pagination known limitations (§4/§8/§2), say so once at the top, not per finding. Zero findings across all six checks is a valid, worth-stating outcome — don't omit the report just because it's clean.

End the report — after the findings, after the known-limitations note — with the open-questions section `gh-project-conventions` requires ("Picking it back up"), over what §1 step 5 listed. Drop it only when that directory was empty. A run that found nothing wrong on GitHub still carries it: an unanswered schema question is exactly the thing an audit has no other way of surfacing.

## Gotchas (verified 2026-08-31 and 2026-09-02, gh v2.98.0)

- Checks 1, 3 and 5 read the `Status` field value and never infer it from the Issue's open/closed state (`gh-project-conventions`, "Why no skill writes `Status`", for why the two can disagree). Check 6 is the single exception, and it reads `state` as *evidence against* the `Status` field, never as a substitute for it.
- **Where that automation is on, closing the Issue is the whole mechanism.** Verified live 2026-09-02 on `Naymi/system-design-practice` Project #1: `gh issue close 16` / `18` flipped both items from `Todo`/`In Progress` to `Done` with no field write at all. Don't report a check-6 tier-a finding as "someone must set `Status` by hand" — closing the Issue is what moves it, and the fix for tier b/c is the PR keyword, not the field.
- **Only English keywords close an Issue from a PR** — `close`/`closes`/`closed`, `fix`/`fixes`/`fixed`, `resolve`/`resolves`/`resolved`, each followed by `#<number>`. A closing sentence in another language is an ordinary cross-reference: `willCloseTarget` stays `false`, the Issue stays open, `Status` stays `Todo`, and nothing anywhere errors. This is check 6 tier c, and it is silent by construction — the backlog looks fine, it just describes finished work as outstanding.
- Sub-issue openness (check 3) is the opposite: read `state` (the *Issue's* own open/closed state) on each sub-issue, not any Project `Status` a sub-issue might or might not carry — a sub-issue need not even be added to this Project.
- **GitHub itself refuses a direct two-item `blockedBy` cycle** — `addBlockedBy(A, blockingIssueId: B)` then `addBlockedBy(B, blockingIssueId: A)` fails the second call with a native `VALIDATION` error ("this dependency would create a cycle where the target is already blocked by the source"). It does **not** catch a longer chain: `addBlockedBy(F,G)` → `addBlockedBy(G,I)` → `addBlockedBy(I,F)` all succeed, producing a real 3-cycle. In practice this means check 2 will essentially never fire on a direct A↔B pair (GitHub already prevents that at write time) — real cycles this check catches are chains of 3 or more. Don't take a clean check-2 result on a Project with lots of direct blockedBy pairs as strong evidence there's no cycle risk; it only means no *chain* cycle was found, and direct pairs were never possible in the first place.
- Adding a sub-issue to an item that's already a Project item (`addSubIssue`) auto-adds the sub-issue to the same Project too, even without a separate `addProjectV2ItemById` call — don't assume an item reached the paginated `items` list only because it was explicitly added.

Check 6 verified 2026-09-02 (gh v2.98.0) against `Naymi/system-design-practice` Project #1 (a real backlog, not a scratch one — nothing was mutated except the two Issue closures the finding called for). Tier c fired on #16 and #18: both `OPEN` with `Status` `In Progress`/`Todo` while merged PRs #21 and #24 said "Закрывает #16" / "Закрывает #18" and every `willCloseTarget` came back `false`. The tier-c judgment rule was exercised on real noise: #16's timeline carried five merged PRs (#17, #19, #20, #21, #24) and #18's four (#19, #20, #21, #24), while exactly one PR per issue claimed closure. #20 appears in both and claims neither — its "Закрывает брейншторм по задаче #16 … и фиксирует ту часть #18" is the near-miss row in §8's table, and a regex on "закрыв.*#18" would have flagged it. Closing both Issues moved `Status` to `Done` automatically (gotcha above), which is what a check-6 finding is asking the reader to do. **Tier b was not reproduced live** — it needs a PR carrying an English keyword merged into a non-default branch; it rests on `willCloseTarget`'s documented meaning plus the same query, which returned the field correctly on every node.

Checks 1–5 verified 2026-08-31 (gh v2.98.0) against a live scratch Project (`Naymi/nats-auth-unified`, deleted after verification) seeded with 9 issues covering all five checks at once: a same-repo 3-cycle (confirming the direct-pair-vs-chain gotcha above), a `Done` parent with one open and one closed sub-issue, two unprioritized `Todo` items (one of which was also the cycle's entry point, confirming checks fire independently per the rule in §7), a `P3` item and a `P0` control item sharing a milestone 2 days out (confirming the `P0`/`P1` exclusion in check 5), and a live-added `P4` option on `Priority` (confirming check 4). All five checks matched the query results by hand exactly as specified above.

## Out of scope

Never mutates anything — no `updateProjectV2ItemFieldValue`, no `removeBlockedBy`, no field edits, no `backlog/` writes, and specifically no `gh issue close` or PR-body edit off the back of a check-6 finding, however conclusive tier a looks. Never runs offline. Never checks `backlog/*.md` mirror content or freshness — that's `gh-project-sync`; the only `backlog/` read it makes beyond the pointer file is §1 step 5's, and no mirror file is opened for it. Never audits PRs.
