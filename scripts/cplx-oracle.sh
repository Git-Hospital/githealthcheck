#!/usr/bin/env bash
# Compare githealthcheck's cognitive and cyclomatic complexity of Go functions with gocognit's and
# gocyclo's on one directory.
#
#   scripts/cplx-oracle.sh <directory of Go code>
#
# gocognit (github.com/uudashr/gocognit) and gocyclo (github.com/fzipp/gocyclo) must be on PATH. Prints
# every function where a value differs, and exits 1 if any does. gocognit leaves out functions of
# complexity 0, so they are left out of that comparison.
set -euo pipefail
export LC_ALL=C

dir=$(cd "${1:?directory}" && pwd)
here=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

GHC_CPLX_ORACLE="$dir" GHC_CPLX_ORACLE_OUT="$work/ghc.txt" \
	go -C "$here" test ./internal/lang/golang -run '^TestOracle$' -count=1 >/dev/null
# Both tools print "<value> <package> <function> <file>:<line>:<column>".
gocognit "$dir" 2>/dev/null | awk '{ split($NF, p, ":"); print p[1] ":" p[2], $1 }' | sort >"$work/cognit.txt"
gocyclo "$dir" | awk '{ split($NF, p, ":"); print p[1] ":" p[2], $1 }' | sort >"$work/cyclo.txt"
# gocyclo skips testdata directories and counts declarations without a body (assembly stubs), which
# githealthcheck does not measure: both comparisons cover the functions both tools measure.
grep -v '/testdata/' "$work/ghc.txt" >"$work/ghc-own.txt" || true
awk '$2 > 0 { print $1, $2 }' "$work/ghc-own.txt" | sort >"$work/ghc-cognit.txt"
awk '{ print $1, $3 }' "$work/ghc-own.txt" | sort >"$work/ghc-cyclo.txt"
awk 'NR == FNR { seen[$1] = 1; next } seen[$1]' "$work/ghc-cyclo.txt" "$work/cyclo.txt" >"$work/cyclo-both.txt"
mv "$work/cyclo-both.txt" "$work/cyclo.txt"
grep -v '/testdata/' "$work/cognit.txt" >"$work/cognit-own.txt" || true
mv "$work/cognit-own.txt" "$work/cognit.txt"

status=0
for m in cognit cyclo; do
	n=$(wc -l <"$work/$m.txt" | tr -d ' ')
	if diff "$work/$m.txt" "$work/ghc-$m.txt" >"$work/$m.diff"; then
		echo "$m: $n functions, same"
	else
		echo "$m: $n functions, $(grep -c '^[<>]' "$work/$m.diff") lines differ"
		head -20 "$work/$m.diff"
		status=1
	fi
done
exit $status
