#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

APP_NAME="${APP_NAME:-dns-control}"
MAIN_PACKAGE="${MAIN_PACKAGE:-./cmd/dns-control}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
CGO_ENABLED="${CGO_ENABLED:-0}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT/bin}"
SKIP_TESTS=false

usage() {
  cat <<'HELP'
Usage: ./build.sh [--skip-tests] [--help]
Environment: APP_NAME, MAIN_PACKAGE, GOOS, GOARCH, CGO_ENABLED, OUTPUT_DIR
Examples:
  ./build.sh
  ./build.sh --skip-tests
  GOARCH=arm64 ./build.sh
HELP
}

for arg in "$@"; do
  case "$arg" in
    --skip-tests) SKIP_TESTS=true ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $arg" >&2; usage >&2; exit 2 ;;
  esac
done

command -v go >/dev/null || { echo "Go is not installed" >&2; exit 1; }
[[ -f go.mod ]] || { echo "Missing go.mod at $ROOT" >&2; exit 1; }
mkdir -p "$OUTPUT_DIR"

if [[ "$SKIP_TESTS" == false ]]; then
  echo "Running tests..."
  go test ./...
fi

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OUTPUT="$OUTPUT_DIR/$APP_NAME-$GOOS-$GOARCH"
[[ "$GOOS" != windows ]] || OUTPUT+=".exe"

echo "Building $APP_NAME ($GOOS/$GOARCH, version $VERSION, commit $COMMIT, at $BUILT_AT)"
GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED="$CGO_ENABLED" \
  go build -trimpath -ldflags="-s -w" -o "$OUTPUT" "$MAIN_PACKAGE"

echo "Built: $OUTPUT"
