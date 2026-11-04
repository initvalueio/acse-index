#!/usr/bin/env bash
# Copyright 2026 The acse-index Authors
# SPDX-License-Identifier: Apache-2.0
#
# Parity: the Go binary and the jq script must produce byte-identical output
# on every shared fixture (and match testdata/golden), and both must refuse
# every error fixture and every bad argument list, writing nothing to stdout.
#
#   test/parity.sh path/to/acse-index-binary
set -euo pipefail

bin=${1:?usage: test/parity.sh path/to/acse-index-binary}
root=$(cd "$(dirname "$0")/.." && pwd)
script="$root/script/acse-index.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

args=(--repo example-org/platform-infra --dir envs/prod
      --commit 9c1e5b7a3d2f4e6081a9b8c7d6e5f4a3b2c1d0e9
      --run run-AbCdEf123456 --produced-at 2026-11-04T16:00:00Z)

fail=0
echo "jq: $(jq --version)"

for f in "$root"/testdata/fixtures/*.json; do
  name=$(basename "$f")
  "$bin" "${args[@]}" <"$f" >"$work/go.json"
  "$script" "${args[@]}" <"$f" >"$work/jq.json"
  if ! cmp -s "$work/go.json" "$work/jq.json"; then
    echo "FAIL $name: binary and script differ"; diff "$work/go.json" "$work/jq.json" || true; fail=1
  elif ! cmp -s "$work/go.json" "$root/testdata/golden/$name"; then
    echo "FAIL $name: output differs from golden"; diff "$work/go.json" "$root/testdata/golden/$name" || true; fail=1
  else
    echo "ok   $name"
  fi
done

refuse() { # label, input file, args...
  local label=$1 input=$2; shift 2
  local form
  for form in "$bin" "$script"; do
    if "$form" "$@" <"$input" >"$work/out" 2>"$work/err"; then
      echo "FAIL $label: $(basename "$form") accepted it"; fail=1
    elif [ -s "$work/out" ]; then
      echo "FAIL $label: $(basename "$form") wrote to stdout on refusal"; fail=1
    fi
  done
  echo "ok   refused $label"
}

for f in "$root"/testdata/errors/*.json; do
  refuse "$(basename "$f")" "$f" "${args[@]}"
done

while IFS= read -r line; do
  case "$line" in ''|'#'*) continue ;; esac
  IFS=$'\t' read -r -a bad <<<"$line"
  refuse "args: ${bad[*]}" "$root/testdata/fixtures/empty-state.json" "${bad[@]}"
done <"$root/testdata/arg-errors.txt"

# --produced-at defaults to now, in the same format, in both forms.
for form in "$bin" "$script"; do
  "$form" "${args[@]:0:8}" <"$root/testdata/fixtures/empty-state.json" >"$work/now.json"
  jq -e '.produced_at | test("\\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\\z")' "$work/now.json" >/dev/null \
    || { echo "FAIL default produced_at from $(basename "$form")"; fail=1; }
done

[ "$("$bin" --version)" = "$("$script" --version)" ] || { echo "FAIL --version differs"; fail=1; }

exit "$fail"
