#!/usr/bin/env python3
# usage: project-field.py <gh-project.json> <status|priority> <issue-number> <option>
# Sets a single-select field of the issue's item in the GitHub Project; ids come from backlog/gh-project.json.
import json, subprocess, sys

if len(sys.argv) != 5 or sys.argv[2] not in ("status", "priority"):
    print("usage: project-field.py <gh-project.json> <status|priority> <issue-number> <option>", file=sys.stderr)
    sys.exit(2)
cfg_path, kind, number, option = sys.argv[1:]
cfg = json.load(open(cfg_path))
field = cfg[f"{kind}FieldId"]
options = cfg[f"{kind}OptionIds"]
if option not in options:
    print(f"bad {kind} {option!r}, expected one of {', '.join(options)}", file=sys.stderr)
    sys.exit(2)
def gh(*a):
    return subprocess.run(["gh", "api", "graphql", *a], check=True, capture_output=True, text=True).stdout
q = "query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){issue(number:$n){projectItems(first:20){nodes{id project{id}}}}}}"
nodes = json.loads(gh("-f", f"query={q}", "-f", f"o={cfg['owner']}", "-f", f"r={cfg['repo']}", "-F", f"n={number}"))
items = [n["id"] for n in nodes["data"]["repository"]["issue"]["projectItems"]["nodes"] if n["project"]["id"] == cfg["projectId"]]
if not items:
    print(f"#{number} not in project", file=sys.stderr)
    sys.exit(1)
m = "mutation($p:ID!,$i:ID!,$f:ID!,$v:String!){updateProjectV2ItemFieldValue(input:{projectId:$p,itemId:$i,fieldId:$f,value:{singleSelectOptionId:$v}}){projectV2Item{id}}}"
gh("-f", f"query={m}", "-f", f"p={cfg['projectId']}", "-f", f"i={items[0]}", "-f", f"f={field}", "-f", f"v={options[option]}")
print(f"#{number} -> {option}")
