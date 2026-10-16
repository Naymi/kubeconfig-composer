#!/usr/bin/env python3
# usage: capture.py <items.json>
# Files each item as a GitHub Issue, adds it to the Project from backlog/gh-project.json, sets Priority and writes the
# backlog/<number>-<slug>.md mirror in the current repository (found from the working directory). Input: a JSON list of
# {"title": "...", "body": "...", "priority": "P0|P1|P2|P3"}. A Project failure is printed
# as PROJECT FAIL and the mirror keeps priority null.
import json, os, pathlib, re, subprocess, sys

if len(sys.argv) != 2 or sys.argv[1] in ("-h", "--help"):
    print("usage: capture.py <items.json>")
    sys.exit(0 if len(sys.argv) == 2 else 2)
items = json.load(open(sys.argv[1]))
os.chdir(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True, capture_output=True, text=True).stdout.strip())
cfg = json.load(open("backlog/gh-project.json"))
repo = f"{cfg['owner']}/{cfg['repo']}"
def run(*a): return subprocess.run(a, check=True, capture_output=True, text=True).stdout
used = {re.sub(r"^\d+-", "", p.stem) for p in pathlib.Path("backlog").glob("*.md")} | {p.stem for p in pathlib.Path("backlog/pending").glob("*.md")}
for it in items:
    if it["priority"] not in cfg["priorityOptionIds"]:
        sys.exit(f"bad priority {it['priority']!r} for {it['title']!r}")
for it in items:
    url = run("gh", "issue", "create", "--repo", repo, "--title", it["title"], "--body", it["body"]).strip()
    v = json.loads(run("gh", "issue", "view", url, "--repo", repo, "--json", "id,number,url,updatedAt"))
    pr = None
    try:
        item = json.loads(run("gh", "api", "graphql", "-f", f'query=mutation{{addProjectV2ItemById(input:{{projectId:"{cfg["projectId"]}" contentId:"{v["id"]}"}}){{item{{id}}}}}}'))["data"]["addProjectV2ItemById"]["item"]["id"]
        run("gh", "api", "graphql", "-f", f'query=mutation{{updateProjectV2ItemFieldValue(input:{{projectId:"{cfg["projectId"]}" itemId:"{item}" fieldId:"{cfg["priorityFieldId"]}" value:{{singleSelectOptionId:"{cfg["priorityOptionIds"][it["priority"]]}"}}}}){{projectV2Item{{id}}}}}}')
        pr = it["priority"]
    except subprocess.CalledProcessError as e:
        print("PROJECT FAIL", v["number"], e.stderr)
    s = re.sub(r"[^a-z0-9]+", "-", it["title"].lower()).strip("-")[:60].rstrip("-")
    base, k = s, 2
    while s in used: s = f"{base}-{k}"; k += 1
    used.add(s)
    pathlib.Path(f"backlog/{v['number']}-{s}.md").write_text(
        f"---\ngithub_issue: {v['number']}\nurl: {v['url']}\nstatus: null\npriority: {pr or 'null'}\nblocked_by: []\nmilestone: null\nupdated_at: {v['updatedAt']}\n---\n\n# {it['title']}\n\n{it['body']}\n")
    print(v["number"], pr, v["url"])
