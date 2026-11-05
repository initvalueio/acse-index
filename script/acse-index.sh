#!/bin/sh
# Copyright 2026 The acse-index Authors
# SPDX-License-Identifier: Apache-2.0
#
# acse-index, script form: the same command line as the Go binary, running
# acse-index.jq (kept next to this file) with jq 1.6 or later.
#
#   terraform show -json | acse-index.sh --repo "$REPO" --dir "$DIR" \
#       --commit "$SHA" --run "$RUN_REF" > index.json
set -eu

usage() {
  cat >&2 <<'EOF'
usage: acse-index.sh --repo OWNER/NAME --dir DIR --commit SHA --run RUN_REF [--produced-at YYYY-MM-DDTHH:MM:SSZ]
Reads `terraform show -json` (state, after apply) on stdin; writes a resource-index-1.0 document on stdout.
EOF
}

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
program="${ACSE_INDEX_JQ:-$here/acse-index.jq}"

repo=""; dir=""; commit=""; run=""; produced_at=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --repo|-repo) [ "$#" -ge 2 ] || { usage; exit 2; }; repo=$2; shift 2 ;;
    --repo=*|-repo=*) repo=${1#*=}; shift ;;
    --dir|-dir) [ "$#" -ge 2 ] || { usage; exit 2; }; dir=$2; shift 2 ;;
    --dir=*|-dir=*) dir=${1#*=}; shift ;;
    --commit|-commit) [ "$#" -ge 2 ] || { usage; exit 2; }; commit=$2; shift 2 ;;
    --commit=*|-commit=*) commit=${1#*=}; shift ;;
    --run|-run) [ "$#" -ge 2 ] || { usage; exit 2; }; run=$2; shift 2 ;;
    --run=*|-run=*) run=${1#*=}; shift ;;
    --produced-at|-produced-at) [ "$#" -ge 2 ] || { usage; exit 2; }; produced_at=$2; shift 2 ;;
    --produced-at=*|-produced-at=*) produced_at=${1#*=}; shift ;;
    --version|-version)
      printf 'acse-index %s\n' "$(sed -n 's/^def acse_index_version: "\(.*\)";$/\1/p' "$program")"
      exit 0 ;;
    -h|--help|-help) usage; exit 0 ;;
    *) printf 'acse-index: unexpected argument %s\n' "$1" >&2; usage; exit 2 ;;
  esac
done

for pair in "repo:$repo" "dir:$dir" "commit:$commit" "run:$run"; do
  if [ -z "${pair#*:}" ]; then
    printf 'acse-index: --%s is required\n' "${pair%%:*}" >&2
    exit 2
  fi
done

command -v jq >/dev/null 2>&1 || { echo "acse-index: jq is required" >&2; exit 1; }
[ -r "$program" ] || { echo "acse-index: cannot read $program" >&2; exit 1; }

# jq writes nothing to stdout when the program fails, so a refused input
# never leaves a partial index behind.
if jq -s -f "$program" \
    --arg repo "$repo" --arg dir "$dir" --arg commit "$commit" \
    --arg run "$run" --arg produced_at "$produced_at"; then
  exit 0
fi
exit 1
