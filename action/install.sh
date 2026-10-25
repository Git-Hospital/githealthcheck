#!/usr/bin/env bash
# Installs a githealthcheck release into a directory: downloads the archive for this runner, checks it
# against the release's checksums.txt and, unless told not to, its build provenance with
# `gh attestation verify` (which needs gh and GH_TOKEN). Used by action.yml; CI runs it on a
# published release.
#
#   action/install.sh <dir> <vX.Y.Z> <verify-provenance: true|false>
set -euo pipefail

dir=$1 version=$2 verify=$3
if ! [[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "::error::version must be a release tag such as v0.3.0, not '$version'"
  exit 1
fi
case "${RUNNER_OS:-$(uname -s)}" in
  Linux) os=linux ;;
  macOS | Darwin) os=darwin ;;
  *) echo "::error::githealthcheck's action runs on Linux and macOS runners, not ${RUNNER_OS:-$(uname -s)}"; exit 1 ;;
esac
case "${RUNNER_ARCH:-$(uname -m)}" in
  X64 | x86_64) arch=amd64 ;;
  ARM64 | arm64 | aarch64) arch=arm64 ;;
  *) echo "::error::unsupported runner architecture ${RUNNER_ARCH:-$(uname -m)}"; exit 1 ;;
esac

name="githealthcheck_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/Git-Hospital/githealthcheck/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$tmp/$name" "$base/$name"
curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$tmp/checksums.txt" "$base/checksums.txt"

# Exactly one line names the archive; shasum checks it.
sums=$(grep -E "^[0-9a-f]{64}  ${name//./\\.}\$" "$tmp/checksums.txt" || true)
if [ "$(printf '%s\n' "$sums" | grep -c .)" != 1 ]; then
  echo "::error::checksums.txt of $version does not list $name exactly once"
  exit 1
fi
if command -v sha256sum > /dev/null; then
  (cd "$tmp" && printf '%s\n' "$sums" | sha256sum -c -)
else
  (cd "$tmp" && printf '%s\n' "$sums" | shasum -a 256 -c -)
fi

case "$verify" in
  true)
    if ! command -v gh > /dev/null; then
      echo "::error::verify-provenance needs the gh CLI on the runner; install it or set verify-provenance: false"
      exit 1
    fi
    gh attestation verify "$tmp/$name" --repo Git-Hospital/githealthcheck
    ;;
  false) ;;
  *) echo "::error::verify-provenance must be true or false, not '$verify'"; exit 1 ;;
esac

mkdir -p "$tmp/x" "$dir"
tar -xzf "$tmp/$name" -C "$tmp/x" githealthcheck
mv "$tmp/x/githealthcheck" "$dir/githealthcheck"
chmod +x "$dir/githealthcheck"
"$dir/githealthcheck" version
