#!/bin/sh
set -eu

script_directory=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_directory/.." && pwd)
data_directory=$repo_root/data
base_url=${GML_DATA_BASE_URL:-https://github.com/PatilNarayan/gold-mine-lab/releases/download/data-xauusd-v1}
base_url=${base_url%/}

if [ "$#" -eq 0 ]; then
    set -- 1d 4h 1h 15m 5m 1m
elif [ "$1" = "--timeframes" ] || [ "$1" = "-Timeframes" ]; then
    shift
fi
if [ "$#" -eq 0 ]; then
    printf '%s\n' 'Provide at least one timeframe after --timeframes.' >&2
    exit 2
fi

for timeframe do
    case "$timeframe" in
        1d|4h|1h|15m|5m|1m) ;;
        *)
            printf 'Unsupported timeframe: %s\n' "$timeframe" >&2
            exit 2
            ;;
    esac
done

command -v curl >/dev/null 2>&1 || {
    printf '%s\n' 'curl is required.' >&2
    exit 1
}
command -v unzip >/dev/null 2>&1 || {
    printf '%s\n' 'unzip is required.' >&2
    exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
    hash_command=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    hash_command=shasum
else
    printf '%s\n' 'sha256sum or shasum is required.' >&2
    exit 1
fi

mkdir -p "$data_directory"
temporary_directory=$(mktemp -d)
checksums_path=$temporary_directory/SHA256SUMS.txt
archive_path=
cleanup() {
    if [ -n "$archive_path" ]; then
        rm -f "$archive_path"
    fi
    rm -rf "$temporary_directory"
}
trap cleanup 0

curl -fsSL "$base_url/SHA256SUMS.txt" -o "$checksums_path"

expected_hash() {
    awk -v csv="$1" '($2 == csv || $2 == ("*" csv)) { print tolower($1); exit }' "$checksums_path"
}

file_hash() {
    if [ "$hash_command" = sha256sum ]; then
        sha256sum "$1" | awk '{ print tolower($1); exit }'
    else
        shasum -a 256 "$1" | awk '{ print tolower($1); exit }'
    fi
}

for timeframe do
    csv_name=XAU_${timeframe}_data.csv
    checksum=$(expected_hash "$csv_name")
    if [ -z "$checksum" ]; then
        printf 'No checksum found for %s in SHA256SUMS.txt\n' "$csv_name" >&2
        exit 1
    fi

    csv_path=$data_directory/$csv_name
    if [ -f "$csv_path" ]; then
        actual_hash=$(file_hash "$csv_path")
        if [ "$actual_hash" = "$checksum" ]; then
            printf 'Skipping %s (SHA-256 already verified)\n' "$csv_name"
            continue
        fi
        printf 'Replacing %s (SHA-256 does not match)\n' "$csv_name"
    fi

    archive_name=XAU_${timeframe}_data.zip
    archive_path=$data_directory/$archive_name
    curl -fsSL "$base_url/$archive_name" -o "$archive_path"
    unzip -oq "$archive_path" -d "$data_directory"
    rm -f "$archive_path"
    archive_path=

    if [ ! -f "$csv_path" ]; then
        printf 'Archive %s did not contain %s at its root\n' "$archive_name" "$csv_name" >&2
        exit 1
    fi
    actual_hash=$(file_hash "$csv_path")
    if [ "$actual_hash" != "$checksum" ]; then
        printf 'SHA-256 mismatch for %s: expected %s, got %s\n' "$csv_name" "$checksum" "$actual_hash" >&2
        exit 1
    fi
    printf 'Downloaded and verified %s\n' "$csv_name"
done
