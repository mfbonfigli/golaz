#!/usr/bin/env bash
# Build the fixture-generator image, run it, and copy the generated fixtures
# into the golaz testdata tree.
#
# Works from Git Bash on Windows (paths are converted to the //c/... form
# docker needs) and from plain Linux/macOS shells.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TESTDATA="$REPO_ROOT/internal/laz/testdata"
OUT="$SCRIPT_DIR/out"

# convert an MSYS-style path (/c/Users/...) to a docker-safe path (//c/Users/...)
docker_path() {
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) echo "/$1" ;;
    *) echo "$1" ;;
  esac
}

mkdir -p "$OUT"

echo "== building image =="
docker build -t golaz-fixturegen "$SCRIPT_DIR"

echo "== generating fixtures =="
docker run --rm \
  -v "$(docker_path "$TESTDATA/las"):/in:ro" \
  -v "$(docker_path "$OUT"):/out" \
  golaz-fixturegen

echo "== placing fixtures into testdata =="
mkdir -p "$TESTDATA/cpporacle/corrupt" "$TESTDATA/cpporacle/compat"
cp -v "$OUT"/las/*.las "$OUT"/las/*.laz "$TESTDATA/las/"
cp -v "$OUT"/cpporacle/*.las "$OUT"/cpporacle/*.laz "$OUT"/cpporacle/*.csv "$TESTDATA/cpporacle/"
cp -v "$OUT"/cpporacle/corrupt/*.laz "$OUT"/cpporacle/corrupt/*.json "$TESTDATA/cpporacle/corrupt/"
cp -v "$OUT"/cpporacle/compat/*.las "$OUT"/cpporacle/compat/*.laz "$OUT"/cpporacle/compat/*.csv \
      "$OUT"/cpporacle/compat/*.json "$TESTDATA/cpporacle/compat/"

echo "== done =="
