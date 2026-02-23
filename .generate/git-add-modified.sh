#!/bin/bash
# Add only files with meaningful content changes (ignoring key ordering differences).
#
# This script compares JSON files using jq to normalize key ordering, and only
# stages files that have actual semantic changes. This prevents large diffs when
# upgrading tools that change key ordering without changing content.

set -euo pipefail

if [ $# -eq 0 ]; then
    echo "Usage: $0 <path-pattern> [path-pattern...]"
    echo "Example: $0 '../[1-9].[0-9]*.[0-9]*'"
    exit 1
fi

added_count=0
skipped_count=0

for pattern in "$@"; do
    # Use nullglob to handle patterns that don't match
    shopt -s nullglob
    for dir in $pattern; do
        if [ ! -d "$dir" ]; then
            continue
        fi

        echo "Processing directory: $dir"

        # Find all .json files in the directory
        while IFS= read -r -d '' file; do
            # Skip if file doesn't exist in git (it's new)
            if ! git ls-files --error-unmatch "$file" &>/dev/null; then
                git add "$file"
                ((added_count++))
                echo "  ✓ Added new file: $file"
                continue
            fi

            # Get the git version and current version, both normalized with jq
            git_content=$(git show "HEAD:$file" 2>/dev/null | jq --sort-keys -S . 2>/dev/null || echo "")
            current_content=$(jq --sort-keys -S . "$file" 2>/dev/null || echo "")

            # If jq failed on either, fall back to regular diff
            if [ -z "$git_content" ] || [ -z "$current_content" ]; then
                if ! git diff --quiet HEAD "$file" 2>/dev/null; then
                    git add "$file"
                    ((added_count++))
                    echo "  ✓ Added (non-JSON or jq failed): $file"
                fi
                continue
            fi

            # Compare normalized versions
            if [ "$git_content" != "$current_content" ]; then
                git add "$file"
                ((added_count++))
                echo "  ✓ Added modified: $file"
            else
                ((skipped_count++))
                echo "  ⊘ Skipped (only formatting changed): $file"
            fi
        done < <(find "$dir" -type f -name '*.json' -print0)
    done
done

echo ""
echo "Summary: Added $added_count file(s), skipped $skipped_count file(s) with only formatting changes."
