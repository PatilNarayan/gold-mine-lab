#!/bin/sh
# sync-xauusd-history.sh — see sync-xauusd-history.ps1 for documentation.
set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo=$(CDPATH= cd "$script_dir/.." && pwd)

SRC=data; OUT=data-twelvedata-v2; TFS="1d,4h,1h,15m"; MAX=40; GAP=8s; DRY=0
while [ $# -gt 0 ]; do
    case "$1" in
        -Src|--src) SRC=$2; shift 2 ;;
        -Out|--out) OUT=$2; shift 2 ;;
        -Timeframes|--timeframes) TFS=$2; shift 2 ;;
        -MaxRequests|--max-requests) MAX=$2; shift 2 ;;
        -RequestInterval|--request-interval) GAP=$2; shift 2 ;;
        -DryRun|--dry-run) DRY=1; shift ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

env_file="$repo/backend/.env"
if [ -f "$env_file" ] && [ -z "${TWELVE_DATA_API_KEY:-}" ]; then
    set -a; . "$env_file"; set +a
fi

cd "$repo/backend"
args="run ./cmd/datasync -src ../$SRC -out ../$OUT -timeframes $TFS -max-requests $MAX -request-interval $GAP"
[ "$DRY" = 1 ] && args="$args -dry-run"
# shellcheck disable=SC2086
exec go $args
