#!/usr/bin/env bash

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
app_dir="$root_dir/App"

if [[ ! -d "$app_dir" ]]; then
	echo "Error: App directory not found at $app_dir" >&2
	exit 1
fi

mapfile -t go_files < <(find "$app_dir" -type f -name '*.go' | sort)

if [[ ${#go_files[@]} -eq 0 ]]; then
	echo "No Go files found in App/"
	exit 0
fi

total=0

echo "Go files in App/ (with line counts):"
for file in "${go_files[@]}"; do
	lines=$(wc -l < "$file")
	total=$((total + lines))
	rel="${file#"$root_dir"/}"
	printf "%6d  %s\n" "$lines" "$rel"
done

echo
printf "Total Go lines in App/: %d\n" "$total"
