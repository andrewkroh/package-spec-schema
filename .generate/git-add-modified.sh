#!/bin/bash
# Add only files with meaningful content changes (ignoring key ordering differences).
#
# This script compares JSON files using yq to normalize key ordering, and only
# stages files that have actual semantic changes. This prevents large diffs when
# upgrading tools that change key ordering without changing content.

set -euo pipefail

if [ $# -eq 0 ]; then
    echo "Usage: $0 <path-pattern> [path-pattern...]"
    echo "Example: $0 '../[1-9].[0-9]*.[0-9]*'"
    exit 1
fi

# Check if yq is available
if ! command -v yq &>/dev/null; then
    echo "Error: yq is not installed. Please install yq to use this script."
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
            # Convert to path relative to git root for git commands
            git_path="$file"

            # Skip if file doesn't exist in git (it's new)
            if ! git ls-files --error-unmatch "$git_path" &>/dev/null; then
                git add "$git_path"
                added_count=$((added_count + 1))
                echo "  ✓ Added new file: $file"
                continue
            fi

            # Get the git version and current version, both normalized with yq
            # sortKeys(..) recursively sorts all nested objects (.. means recursive descent)
            git_content=""
            current_content=""

            if git_content=$(git show "HEAD:$git_path" 2>/dev/null | yq -o json -P 'sortKeys(..)' 2>/dev/null); then
                current_content=$(yq -o json -P 'sortKeys(..)' "$file" 2>/dev/null || echo "")
            fi

            # If yq failed on either, fall back to regular diff
            if [ -z "$git_content" ] || [ -z "$current_content" ]; then
                if ! git diff --quiet HEAD "$git_path" 2>/dev/null; then
                    git add "$git_path"
                    added_count=$((added_count + 1))
                    echo "  ✓ Added (non-JSON or yq failed): $file"
                fi
                continue
            fi

            # Compare normalized versions
            if [ "$git_content" != "$current_content" ]; then
                git add "$git_path"
                added_count=$((added_count + 1))
                echo "  ✓ Added modified: $file"
            else
                skipped_count=$((skipped_count + 1))
                echo "  ⊘ Skipped (only formatting changed): $file"
            fi
        done < <(find "$dir" -type f -name '*.json' -print0)
    done
done

echo ""
echo "Summary: Added $added_count file(s), skipped $skipped_count file(s) with only formatting changes."
