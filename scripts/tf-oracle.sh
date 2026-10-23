#!/usr/bin/env bash
# Compare githealthcheck's truck factor with the reference tool's on one repository.
#
#   scripts/tf-oracle.sh <repository URL or path> <Truck-Factor checkout>
#
# The reference is github.com/aserg-ufmg/Truck-Factor at 614600ed (MIT), with tf-oracle.patch applied
# (git apply scripts/tf-oracle.patch in the checkout) and built with mvn package in its gittruckfactor
# directory; java must be on PATH. The patch fixes one bug: the tool marks a rename as expanded on the
# record that all files share, so of two files with a common rename chain the second in path order
# loses the older history. Without the patch the two tools differ on such repositories only.
#
# Both tools get the same snapshot and the same file list (the files githealthcheck counts), so only
# the history and the algorithm are compared. The repository is cloned to a temporary directory first,
# since the tool's script writes into it. KEEP=1 keeps that directory.
set -euo pipefail
export LC_ALL=C # one decimal separator for both tools

src=${1:?repository}
tool=$(cd "${2:?Truck-Factor checkout}/gittruckfactor" && pwd)
here=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
echo "work: $work" >&2
trap 'if [ -z "${KEEP:-}" ]; then rm -rf "$work"; fi' EXIT

git clone --quiet --no-hardlinks "$src" "$work/repo"
GHC_TF_ORACLE="$work/repo" GHC_TF_ORACLE_OUT="$work" \
	go -C "$here" test ./internal/analyze/history -run '^TestOracle$' -count=1 >/dev/null

(cd "$tool/scripts" && ./commit_log_script.sh "$work/repo" >/dev/null)
cp "$work/filelist.log" "$work/repo/filelist.log" # the script lists every tracked file; use ours
(cd "$tool" && java -Duser.language=en -Duser.country=US -jar target/gittruckfactor-1.0.jar "$work/repo" repo 2>/dev/null) >"$work/ref.txt"

ref_tf=$(sed -n 's/^TF = \([0-9]*\) .*/\1/p' "$work/ref.txt")
ref_cov=$(sed -n 's/^TF = [0-9]* (coverage = \([0-9.,]*\)%)$/\1/p' "$work/ref.txt" | tr , .)
ref_files=$(sed -n '/^TF authors/,$p' "$work/ref.txt" | tail -n +2 | awk -F';' 'NF { print $(NF-1) }' | sort -n | paste -sd, -)

ghc_tf=$(sed -n 's/.*"tf": \([0-9]*\).*/\1/p' "$work/ghc.json")
files=$(sed -n 's/.*"files": \([0-9]*\),.*/\1/p' "$work/ghc.json" | head -1)
after=$(sed -n 's/.*"coveredAfter": \([0-9]*\).*/\1/p' "$work/ghc.json")
ghc_cov=$(awk -v a="$after" -v f="$files" 'BEGIN { printf "%.2f", f ? 100 * a / f : 0 }')
ghc_files=$(sed -n '/"devs"/,$p' "$work/ghc.json" | sed -n 's/.*"files": \([0-9]*\).*/\1/p' | sort -n | paste -sd, -)

printf 'reference:      TF %s, coverage after removal %s%%, TF developers authoring %s files\n' "$ref_tf" "$ref_cov" "$ref_files"
printf 'githealthcheck: TF %s, coverage after removal %s%%, TF developers authoring %s files\n' "$ghc_tf" "$ghc_cov" "$ghc_files"
if [ "$ref_tf" = "$ghc_tf" ] && [ "$ref_cov" = "$ghc_cov" ] && [ "$ref_files" = "$ghc_files" ]; then
	echo same
else
	echo DIFFERENT
	exit 1
fi
