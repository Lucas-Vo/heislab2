#!/usr/bin/env bash

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
app_dir="$root_dir"

if [[ ! -d "$app_dir" ]]; then
    echo "Error: App directory not found at $app_dir" >&2
    exit 1
fi

mapfile -d '' -t go_files < <(find "$app_dir" -type f -name '*.go' -print0 | sort -z)

if [[ ${#go_files[@]} -eq 0 ]]; then
    echo "No Go files found in App/"
    exit 0
fi

total_with_soup=0
total_without_soup=0
included=()
excluded=()

for file in "${go_files[@]}"; do
    lines=$(wc -l < "$file")
    rel="${file#"$app_dir"/}"

    total_with_soup=$((total_with_soup + lines))

    if [[ "$file" == "$app_dir/Network-go/"* || "$file" == "$app_dir/elevhw/elevator_io.go" ]]; then
        excluded+=("$(printf "%6d  %s" "$lines" "$rel")")
        continue
    fi

    total_without_soup=$((total_without_soup + lines))
    included+=("$(printf "%6d  %s" "$lines" "$rel")")
done

echo "Included files:"
if [[ ${#included[@]} -eq 0 ]]; then
    echo "  (none)"
else
    printf "%s\n" "${included[@]}"
fi

echo
echo "Excluded files:"
if [[ ${#excluded[@]} -eq 0 ]]; then
    echo "  (none)"
else
    printf "%s\n" "${excluded[@]}"
fi

echo
printf "Total lines with soup: %d\n" "$total_with_soup"
printf "Total lines without soup: %d\n" "$total_without_soup"
