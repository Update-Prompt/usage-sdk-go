#!/usr/bin/env bash
# Runs a command in every Go module of this repo: the SDK and each router
# adapter. Runs it in all of them, then fails if it failed in any.
#
# The command gets OLDEST_GO, the toolchain of the module's go line, the
# oldest Go apps may use with it, such as go1.24.0.
set -uo pipefail

failed=0
for dir in $(git ls-files -- go.mod '*/go.mod' | xargs -n1 dirname); do
  echo "::group::$dir: $*"
  version=$(awk '$1 == "go" { print $2 }' "$dir/go.mod")
  case "$version" in *.*.*) ;; *) version="$version.0" ;; esac
  if ! (cd "$dir" && OLDEST_GO="go$version" "$@"); then
    echo "::error::'$*' failed in $dir"
    failed=1
  fi
  echo "::endgroup::"
done
exit "$failed"
