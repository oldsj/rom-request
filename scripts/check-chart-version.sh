#!/usr/bin/env bash
# Fails when files under the chart changed against the base branch but Chart.yaml's version
# did not: the publish workflow pushes each version once and never overwrites it.
set -euo pipefail
chart=${1:?usage: check-chart-version.sh CHART_DIR}
base=${CHART_BASE_REF:-origin/main}

if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  echo "check-chart-version: no $base to compare with; skipped"
  exit 0
fi
if git diff --quiet "$base" -- "$chart" && git diff --quiet --cached "$base" -- "$chart" \
  && [ -z "$(git ls-files --others --exclude-standard -- "$chart")" ]; then
  echo "check-chart-version: $chart unchanged"
  exit 0
fi
old=$(git show "$base:$chart/Chart.yaml" 2>/dev/null | sed -n 's/^version: //p' || true)
new=$(sed -n 's/^version: //p' "$chart/Chart.yaml")
[[ "$new" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Chart version '$new' is not stable SemVer"; exit 1; }
if [ -n "$old" ] && [ "$old" = "$new" ]; then
  echo "$chart changed but its version is still $new: bump version in $chart/Chart.yaml"
  exit 1
fi
echo "check-chart-version: $chart ${old:-new} -> $new"
