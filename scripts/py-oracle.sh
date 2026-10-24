#!/usr/bin/env bash
# Compare githealthcheck's complexity of Python functions with the reference implementations:
#
#   scripts/py-oracle.sh <directory of Python code>   cyclomatic complexity against radon's
#   scripts/py-oracle.sh --sonar                       cognitive complexity against the expected values
#                                                      in SonarSource's own test file
#
# radon (pip install radon) must be on PATH for the first. The second fetches one test file of
# sonar-python at a fixed commit, whose functions are annotated "# =N" with their cognitive
# complexity. Prints every function where a value differs, and exits 1 if any does.
set -euo pipefail
export LC_ALL=C

here=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sonar_commit=62d4fa9df0c6dbdc93d3755a54b25ab72b5b05d6
sonar_file=python-frontend/src/test/resources/metrics/cognitive-complexities.py

measure() {
	GHC_CPLX_ORACLE="$1" GHC_CPLX_ORACLE_OUT="$work/ghc.txt" \
		go -C "$here" test ./internal/lang/python -run '^TestOracle$' -count=1 >/dev/null
}

if [ "${1:-}" = "--sonar" ]; then
	mkdir "$work/sonar"
	curl -fsSL "https://raw.githubusercontent.com/SonarSource/sonar-python/$sonar_commit/$sonar_file" -o "$work/sonar/cc.py"
	measure "$work/sonar"
	# The expected value is on the line of each def: "def name(): # =N".
	awk '{ if (match($0, /#[ ]*=[ ]*[0-9]+/)) { v = substr($0, RSTART, RLENGTH); gsub(/[^0-9]/, "", v); print FILENAME ":" FNR, v } }' \
		"$work/sonar/cc.py" | sort >"$work/want.txt"
	awk 'NR == FNR { want[$1] = 1; next } ($1 in want) { print $1, $2 }' "$work/want.txt" "$work/ghc.txt" | sort >"$work/got.txt"
	n=$(wc -l <"$work/want.txt" | tr -d ' ')
	if diff "$work/want.txt" "$work/got.txt"; then
		echo "sonar cognitive: $n functions, same"
		exit 0
	fi
	echo "sonar cognitive: differences above"
	exit 1
fi

dir=$(cd "${1:?directory}" && pwd)
measure "$dir"
# radon cc -j lists, per file, every function and method with its line and complexity; closures are
# nested inside them, and githealthcheck does not measure them on their own either.
radon cc -j "$dir" | python3 -c '
import json, sys
for path, blocks in json.load(sys.stdin).items():
    if not isinstance(blocks, list):
        continue
    for b in blocks:
        if b.get("type") in ("function", "method"):
            print("%s:%d %d" % (path, b["lineno"], b["complexity"]))
' | sort >"$work/radon.txt"
awk '{ print $1, $3 }' "$work/ghc.txt" | sort >"$work/ghc-cyclo.txt"
# Both sides cover the functions both measure.
awk 'NR == FNR { seen[$1] = 1; next } ($1 in seen)' "$work/ghc-cyclo.txt" "$work/radon.txt" >"$work/radon-both.txt"
awk 'NR == FNR { seen[$1] = 1; next } ($1 in seen)' "$work/radon-both.txt" "$work/ghc-cyclo.txt" >"$work/ghc-both.txt"
n=$(wc -l <"$work/radon-both.txt" | tr -d ' ')
if diff "$work/radon-both.txt" "$work/ghc-both.txt" >"$work/cyclo.diff"; then
	echo "radon cyclomatic: $n functions, same"
	exit 0
fi
echo "radon cyclomatic: $n functions, $(grep -c '^[<>]' "$work/cyclo.diff") lines differ"
head -20 "$work/cyclo.diff"
exit 1
