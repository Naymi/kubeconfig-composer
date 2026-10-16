#!/usr/bin/env bash
# usage: set-priority.sh <issue-number> <P0|P1|P2|P3>
# Sets the Priority field of an issue in the GitHub Project from backlog/gh-project.json.
case "${1:-}" in -h|--help) sed -n 's/^# usage: //p' "$0"; exit 0 ;; esac
set -euo pipefail
[ $# -eq 2 ] || { sed -n 's/^# usage: //p' "$0"; exit 2; }
root=$(git rev-parse --show-toplevel) || { echo "set-priority.sh: run it from inside the repository that holds backlog/gh-project.json" >&2; exit 1; }
exec python3 "$(dirname "$0")/project-field.py" "$root/backlog/gh-project.json" priority "$1" "$2"
