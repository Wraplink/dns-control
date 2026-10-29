#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
APP_NAME="${APP_NAME:-dns-control}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-$(go env GOARCH 2>/dev/null || uname -m)}"
case "$GOARCH" in x86_64) GOARCH=amd64 ;; aarch64) GOARCH=arm64 ;; esac
BIN="${BINARY_PATH:-$ROOT/bin/$APP_NAME-$GOOS-$GOARCH}"
CONFIG=""
EXTRA=()

usage() {
  cat <<'HELP'
Usage: ./run.sh --config FILE [-- APP_ARGS...]
       ./run.sh FILE [-- APP_ARGS...]
Options:
  -c, --config FILE  Configuration file path (required)
  -h, --help         Show help
Environment: APP_NAME, GOOS, GOARCH, BINARY_PATH
Run ./build.sh first if the executable is missing.
HELP
}

while (($#)); do
  case "$1" in
    -c|--config)
      (($# >= 2)) || { echo "Missing configuration file" >&2; exit 2; }
      CONFIG="$2"; shift 2 ;;
    --config=*) CONFIG="${1#*=}"; shift ;;
    --) shift; EXTRA=("$@"); break ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
    *)
      if [[ -z "$CONFIG" ]]; then CONFIG="$1"; shift
      else echo "Unexpected argument: $1" >&2; exit 2; fi ;;
  esac
done

[[ -n "$CONFIG" ]] || { echo "Configuration file is required" >&2; usage >&2; exit 2; }
[[ -f "$CONFIG" && -r "$CONFIG" ]] || { echo "Cannot read configuration: $CONFIG" >&2; exit 1; }
CONFIG="$(realpath -- "$CONFIG")"
[[ -f "$BIN" && -x "$BIN" ]] || { echo "Executable not found: $BIN (run ./build.sh)" >&2; exit 1; }

echo "Starting $APP_NAME with config: $CONFIG"
exec "$BIN" -config "$CONFIG" "${EXTRA[@]}"
