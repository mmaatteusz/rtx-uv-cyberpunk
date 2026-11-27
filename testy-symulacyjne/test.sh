#!/usr/bin/env bash
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
src="$here/../source"
cp "$src/go.mod" "$tmp/"
for file in "$src"/*.go; do
    case "$file" in
        *_windows*.go) ;;
        *) cp "$file" "$tmp/" ;;
    esac
done
cp "$src/optimize_windows.go" "$tmp/optimize.go"
cp "$src/preview_windows.go" "$tmp/preview.go"
cp "$src/restore_windows.go" "$tmp/restore.go"
cp "$here/environment.txt" "$tmp/sim_test.go"
cd "$tmp"
go test -race -buildvcs=false -run '^TestSimulatedSession' -v .
