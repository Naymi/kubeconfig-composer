# gh-project-conventions — Reference

## backlog/gh-project.json

```json
{
  "owner": "<account>",
  "repo": "<repo>",
  "projectNumber": 2,
  "projectId": "PVT_...",
  "priorityFieldId": "PVTSSF_...",
  "priorityOptionIds": { "P0": "...", "P1": "...", "P2": "...", "P3": "..." },
  "statusFieldId": "PVTSSF_...",
  "statusOptionIds": { "Todo": "...", "In Progress": "...", "Done": "..." }
}
```

Field/option IDs are cached because `updateProjectV2ItemFieldValue` needs the option's ID (`singleSelectOptionId`), not its label. If a cached ID is rejected, re-resolve via `gh project field-list <number> --owner <owner> --format json` and rewrite the cache — don't hard-fail on the first error.

## Mirror file (`backlog/<number>-<slug>.md`)

```markdown
---
github_issue: 42
url: https://github.com/<owner>/<repo>/issues/42
status: Todo
priority: P1
blocked_by:
  - {number: 17, title: "...", status: Done}
milestone: null
updated_at: 2026-08-31T12:00:00Z
---

# <issue title>

<issue body, verbatim>
```

`updated_at` is the Issue's live `updatedAt` at last sync — the staleness marker `gh-project-sync` compares against. `blocked_by` is denormalized (number + title + status) for offline readability, at the cost of lagging until the next sync pass — and that lag is invisible to `updated_at`, because adding or removing a blocker does not advance the blocked Issue's `updatedAt` (`gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged` covers what follows from that).

**Byte-exact assembly.** `gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged` decides "diverged" by regenerating this file and comparing it to the copy on disk, so two writers that assemble the same data into different bytes make every run report a divergence that isn't one. Assemble it in exactly this order, with no step left to taste:

1. The `---`-fenced frontmatter, then a blank line.
2. `# ` and the Issue title on one line. A blank line follows it **only if the normalized body is non-empty** — see "Empty body" below, which is the same rule stated from the other end; two writers reading this step as unconditional and as conditional produce different bytes for every Issue with no description, and `gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged` then reports and rewrites all of them, every run.
3. The body, normalized as below — nothing stripped from its interior, nothing reflowed, nothing appended after it.

**Body normalization — the one deliberate departure from "verbatim."** GitHub returns bodies with CRLF: convert every `\r\n` to `\n`. Then strip *all* trailing newlines from the body and write exactly one at the end of the file, so the file always ends with a single `\n` and never with a blank line.

Do not shorten this to "write the body as it came." GitHub does not guarantee a body ends with a newline — one typed in the web textarea without a final Enter comes back without one, and an Issue with no description comes back as `""`. A mirror written with no final newline does not stay that way: this repo's own `.editorconfig` sets `insert_final_newline = true`, and the pre-commit hook and every ordinary editor act on it. The file then differs from what `gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged` regenerates, that pass overwrites it, the newline comes back, and the two take turns forever — a churn loop with no bad actor in it. Pinning both ends of the body is what stops it; an earlier version of this rule pinned only one and split a repo's mirrors into two incompatible generations that diffed against each other indefinitely.

**Empty body.** When the body normalizes to nothing, the file ends at the title line — frontmatter, blank line, `# ` and the title, one `\n`. No blank line, no trailing whitespace after it.

Frontmatter rules, all load-bearing for that comparison:

- Keys appear in the order shown above, and every key is always written — `blocked_by: []` and `milestone: null` are spelled out, never omitted.
- **Scalars are written unquoted, exactly as shown:** `github_issue` a bare integer, `url` a bare URL, `status`/`priority`/`blocked_by[].status` bare option names, `updated_at` a bare timestamp. Quotes appear only where the rules below put them — around a title or a `due_on` — because a writer that adds them anywhere else diverges byte-for-byte from one that doesn't.
- **`updated_at` is the API's `updatedAt` string copied through verbatim** — same characters, no reformatting. Not `+00:00` in place of the trailing `Z`, not fractional seconds added or dropped, not converted to local time, not re-rendered by a date library. This is the field every run compares first (`gh-project-sync#3-detecting-stale-vs-diverged-vs-unchanged`), so a writer that normalizes it puts the mirror in permanent disagreement with a writer that doesn't — a divergence reported every run, with the two timestamps naming the same instant. The same trap catches a writer that assembles frontmatter by handing a dict to a YAML dumper: in YAML 1.1 an unquoted timestamp resolves to a timestamp type and comes back out in the dumper's own format. Assemble these lines as text.
- `status` and `priority` each carry the Project field's option name, or `null` when the field is unset on that item — which is what the query answers for an unset field rather than an error ("Shared limitations" below). Spell the unset case `null`; never omit the key, never invent `Todo` for it. A reader filtering on `Status == Todo` (`gh-project-next`'s offline path) excludes `null` on its own, matching what the same filter does online.
- **"Never invent `Todo`" binds the writers hardest right after they create an item.** No `gh-project-*` skill sets `Status` (`gh-project-conventions`, "Why no skill writes `Status`"), so a skill that has just run `gh issue create` plus the add-to-Project call has not read that field, and has nothing to write but `null` unless it goes and reads it (which is what the paragraphs below are about). Whether the Project's built-in "Item added → Todo" automation exists, and whether it has already fired, is not knowable from the call that added the item. Guessing `Todo` is what turns the next sync into a rewrite back to `null` and the one after it into a rewrite forward again. Write `null`; the first sync that queries the item fills in whatever `Status` really is and reports that fill-in once, correctly, as a divergence.

  This has a price, named here so nobody trades it back: between creation and that first sync, offline `gh-project-next` won't offer the item, because its `Status == Todo` filter excludes `null`. That is the same answer the online path gives for an item whose `Status` really is unset, and it errs the direction this family already errs elsewhere — leaving a ready item unoffered for one sync costs less than the alternating rewrites, and far less than a `backlog/` whose fields say things GitHub never said.

  **Scale that price by how many mirrors a run writes at once before accepting it as small.** "One item unoffered for one sync" is the true cost for `gh-project-capture`, which adds a single finding to a `backlog/` that already has others in it. It is not the cost for `gh-project-setup`, which writes a whole `backlog/` in one migration: every mirror in it carries `null` at the same moment, so offline `gh-project-next` has no `Todo` item to work with anywhere — not for its candidate sort, and not for its no-candidate fallback, which walks `Todo` items too. The offline answer there isn't a lower-ranked item, it's nothing at all.

  The way out is not to relax the rule but to stop needing the guess: a writer holding the item it just added can **read** `Status` back and write what came back, which is a read, not an invention, and satisfies this rule exactly. The read is bought where the blind write is expensive and nothing else will finish the job — which writer pays for it is that skill's own step to state.
- **A field whose live write failed is `null` here too — not the value that was meant to land.** Same rule the `blocked_by` entry for a failed `addBlockedBy` follows (`gh-project-sync#1c-promoting-one-draft`): this file records what GitHub holds. A mirror carrying a `priority` the Project never accepted is overwritten with `null` by the next divergence check anyway, so writing it only destroys the value the failure report told a human to restore. The reported remediation command is what puts it on GitHub; the sync after that copies it here.
- `blocked_by` entries are sorted by ascending `number`, whatever order `blockedBy.nodes` returned them in. The API order is not documented as stable, and an unsorted list turns a reordering on GitHub's side into a phantom divergence.

  **Sorting only settles the list when the page it came from was complete, and this format cannot say whether it was** — see "Shared limitations" below, which owns that fact and what follows from it.
- One entry per line, `  - {number: N, title: "...", status: Todo|Done}`, with `"` and `\` in the title backslash-escaped.
- `milestone` is `null`, or `{title: "...", due_on: "..."|null}` on one line, same escaping.

`milestone` is `null` when the Issue has no milestone, otherwise `{title: "...", due_on: "2026-09-14T00:00:00Z"}` (the Issue's live `milestone.dueOn`, `null` if the milestone itself has no due date) — the shape a milestone-aware reader (e.g. `gh-project-next`'s offline sort) needs; any writer of this mirror (`gh-project-capture`, `gh-project-sync`) populates it in this shape.

## Slug algorithm

Applied in this exact order:

1. Lowercase the title.
2. Replace every run of one-or-more non-alphanumeric characters with a single `-`.
3. Strip any leading/trailing `-` left over from step 2 (e.g. from punctuation at the start/end of the title).
4. If longer than 60 characters, cut at 60 (mid-word is fine, no word-boundary awareness) then strip any trailing `-` the cut exposed.
5. Check for a collision against the **slug portion** of every existing file in both directories — for `backlog/pending/<slug>.md` that's the whole stem, for `backlog/<number>-<slug>.md` that's the stem with the leading `<number>-` stripped off. If the slug you generated matches one already in use, append `-2` (then `-3`, …) — literal hyphen plus the next unused integer.

Example: `"A revoked agent id can never be reused, ever!!"` → `a-revoked-agent-id-can-never-be-reused-ever`.

## Pending draft (`backlog/pending/<slug>.md`)

Same shape, `github_issue: null`, `status: pending-sync`. `blocked_by` may reference other still-pending drafts by slug (no issue number exists yet) — resolve those to real issue numbers only once every referenced draft has been promoted (topological order — `gh-project-sync`'s job; a cycle among pending drafts is a hard error, surfaced, not silently broken).

## Unanswered schema-fit question (`backlog/open-questions/<slug>.md`)

Written only when a session ends with a schema-fit question unanswered (`gh-project-conventions`, "If the session ends before the answer"):

```markdown
---
schema_fit: unresolved
asked_at: 2026-09-02T11:04:00Z
priority_if_as_is: P1
source: gh-project-capture
---

# <the finding's title>

<the finding's body, verbatim — this is the only surviving copy>

## Why it doesn't fit

<one or two sentences>

## Options put to the user

1. Record as-is under `Status`/`Priority` (`priority_if_as_is` above).
2. <named extension, e.g. a single-select `Type` field>
3. <named extension, e.g. a `process` label>
```

`source` is the skill that asked, plus the migration file it came from when `gh-project-setup` wrote it (e.g. `gh-project-setup: todo/03-weekly-dep-review.md`) — one file per flagged item, never one file listing several. Same slug algorithm as everywhere else, collisions checked against `backlog/*.md`, `backlog/pending/*.md`, and `backlog/open-questions/*.md`.

No `github_issue`/`url`/`status` keys: this is not a backlog item, `gh-project-sync` never touches it, and `gh-project-next` never offers it as work. It is deleted by whichever skill acts on the user's answer, once the finding has been recorded the way the user chose.

## GraphQL mutations (verified in this account's schema — input field names, not guessed)

| Purpose | Mutation | Key input fields |
|---|---|---|
| Add an issue to the Project | `addProjectV2ItemById` | `projectId`, `contentId` (the issue's node id) |
| Set a single-select field (`Priority`, `Status`) | `updateProjectV2ItemFieldValue` | `projectId`, `itemId`, `fieldId`, `value: { singleSelectOptionId }` |
| Block one issue on another | `addBlockedBy` | `issueId`, `blockingIssueId` — **one blocker per call**, not a list; loop to add several |
| Remove a block | `removeBlockedBy` | `issueId`, `blockingIssueId` |
| Read blockers/blocked | — | `Issue.blockedBy`, `Issue.blocking` |
| Add a sub-issue | `addSubIssue` | `issueId` (parent), `subIssueId` or `subIssueUrl`, optional `replaceParent` |
| Remove a sub-issue | `removeSubIssue` | `issueId`, `subIssueId` |
| Reorder sub-issues | `reprioritizeSubIssue` | — |
| Read sub-issue state | — | `Issue.parent`, `Issue.subIssues`, `Issue.subIssuesSummary` |

**Every `*Id`/`*IssueId` field above is a GraphQL node id** (the same kind of value as `addProjectV2ItemById`'s `contentId`, e.g. `I_kwDO...` for an issue), never an issue number. Get one with `gh issue view <number> --repo <owner>/<repo> --json id -q .id`.

`gh issue` has no built-in subcommand for any of these — drive them through `gh api graphql`.

## Shared limitations

Facts about GitHub, or about this format, that more than one `gh-project-*` skill has to act on. Each is stated here once, with its cause and with whatever would close it; a skill that meets one says only what it does about it.

### Blocker and sub-issue lists are a single unpaginated page of 50

`blockedBy(first: 50)` and `subIssues(first: 50)` return one page — no query in this family loops `pageInfo`/`hasNextPage` over either connection. Every query that reads them also asks for `totalCount`, so the shortfall is measurable instead of invisible: where `totalCount` exceeds the number of `nodes` returned, what came back is part of that item's graph, not all of it.

The mirror format carries no truncation marker, so nothing on disk tells a capped `blocked_by` from a complete one, and the report line of the run that wrote it is the only record. Two things follow. A reader working from `backlog/` alone computes readiness off a list it cannot know is partial. And two runs handed two different subsets of the same blocker set regenerate two different `blocked_by` lists, so the mirror is reported as diverged and rewritten on **every** sync, with `blocked_by` named as the moved field each time and no bad actor anywhere in the loop — sorting the list ("Mirror file", the `blocked_by` sort rule) does not settle that, because sorting two different subsets leaves them different.

What would close it is a truncation marker in the mirror frontmatter, and that is a format change across every writer and reader in the family — not something one skill can do on its own. Until it exists, saying so is the fix: every skill that reads one of these connections reports the shortfall it saw.

### `fieldValueByName` returns `null` for an unset field, not an error

Every query in this family reads `Status` and `Priority` through `fieldValueByName`, and every one of them meets items where the field carries no value: an Issue added to the Project by hand in the GitHub UI routinely has no `Status`, and an item nobody has triaged yet has no `Priority`. The call answers `null` for those, and nothing anywhere errors.

So `null` is the answer "this field is unset," and it is the only signal there is. A reader that takes it for a query failure stops on an ordinary item; a reader that fills it in with a default records a value GitHub never held ("Never invent `Todo`" above, for where that costs the most). Nothing would close this — it is how the API answers — so the fix is to read `null` as the value it is.

### The `items` query is paginated, and its root selector is not cached anywhere

`gh-project-next`, `gh-project-audit` and `gh-project-sync` all run the same `projectV2(number:) { items(first: 50, after: $cursor) }` query over this Project, and two things about it go wrong quietly.

**Pagination.** Loop while `pageInfo.hasNextPage`, passing `endCursor` back as `cursor`, and accumulate every page **before** filtering, sorting or scanning. A first page that already answers the question is no evidence that there is not a second one, and what a run is looking for can sit on any page — so a run that stops early reports a confident answer computed over part of the backlog, with nothing in the output saying so.

**Root selector.** The query's root is `user(login:)` for a user-owned Project and `organization(login:)` for an org-owned one, and nothing on disk says which: `backlog/gh-project.json` (above) does not carry it. Resolve it once per run, before the paginated query, from `gh project view <number> --owner <owner> --format json`'s `owner.type` (`"User"` or `"Organization"`). What would close that lookup is caching `owner.type` in the pointer file — a change to this format and to every skill that writes it; until then, one extra call per run is the price.

## Done items lose their mirror

When an Issue's `Status` becomes `Done`, `gh-project-sync` deletes its `backlog/<number>-<slug>.md` — the directory holds what's outstanding, not an archive. Git history keeps the deleted content.

## Field-ID cache invalidation

Any writing skill (`gh-project-capture`, `gh-project-sync`) that gets a GraphQL error referencing an unknown field/option ID must: re-run `gh project field-list`, update `backlog/gh-project.json` with the fresh IDs, retry once. If it still fails, surface the real error — don't loop indefinitely. `gh-project-next` and `gh-project-audit` are read-only with respect to the Project graph and this cache — they never write to `backlog/gh-project.json` and this invalidation procedure doesn't apply to them.

## Blockers are same-repo only

`addBlockedBy`/`removeBlockedBy` are not restricted by GitHub to issues in the same repository, but this convention only ever uses them within one repo — a `gh-project-*` skill never creates a cross-repository block. This is a scope decision of the convention, not a technical limitation of the mutations.
