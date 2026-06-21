#!/usr/bin/env bash
#
# coverage_gate.sh — enforce a per-package unit-test coverage floor.
#
# Gated packages: the root facade, every pkg/*, and internal/binio. The CLI
# (cmd/*) is intentionally not gated, and internal/conformance only builds under
# the 'conformance' tag, so both are excluded. pkg/oci has a lower floor because
# its OCI tar/manifest paths are disproportionately costly to unit-test.
#
# Usage: scripts/coverage_gate.sh [floor] [oci_floor]
set -euo pipefail

FLOOR="${1:-75}"
OCI_FLOOR="${2:-65}"

cd "$(dirname "$0")/.."

mapfile -t PKGS < <(go list ./... | grep -vE '/cmd(/|$)|/internal/conformance$')

echo "Coverage gate: floor ${FLOOR}% (pkg/oci ${OCI_FLOOR}%)"
echo

fail=0
while IFS= read -r line; do
	# Lines look like:
	#   ok  	github.com/.../pkg/alloc	0.003s	coverage: 100.0% of statements
	#   ?   	github.com/.../pkg/foo	[no test files]
	if [[ "$line" == *"[no test files]"* ]]; then
		pkg=$(awk '{print $2}' <<<"$line")
		echo "FAIL ${pkg}: no test files"
		fail=1
		continue
	fi
	[[ "$line" == *"coverage:"* ]] || continue

	pkg=$(awk '{print $2}' <<<"$line")
	pct=$(sed -E 's/.*coverage: ([0-9.]+)% of statements.*/\1/' <<<"$line")

	floor="$FLOOR"
	[[ "$pkg" == */pkg/oci ]] && floor="$OCI_FLOOR"

	if awk "BEGIN{exit !($pct < $floor)}"; then
		printf 'FAIL %s: %s%% < %s%%\n' "$pkg" "$pct" "$floor"
		fail=1
	else
		printf 'ok   %s: %s%% (>= %s%%)\n' "$pkg" "$pct" "$floor"
	fi
done < <(go test -covermode=atomic -cover "${PKGS[@]}")

echo
if [[ "$fail" -ne 0 ]]; then
	echo "coverage gate failed"
	exit 1
fi
echo "coverage gate passed"
