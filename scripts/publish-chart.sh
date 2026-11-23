#!/usr/bin/env bash
# Pushes the chart to oci://ghcr.io/oldsj/charts once per version, as OpenCozy does: an existing
# version with identical contents is a no-op, and with different contents is an error.
set -euo pipefail
chart=${1:?usage: publish-chart.sh CHART_DIR}
registry=${CHART_REGISTRY:-oci://ghcr.io/oldsj/charts}
name=$(sed -n 's/^name: //p' "$chart/Chart.yaml")
version=$(sed -n 's/^version: //p' "$chart/Chart.yaml")
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Expected a stable SemVer chart version'; exit 1; }

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
helm package "$chart" --destination "$work"
mkdir "$work/existing" "$work/candidate"

if helm pull "$registry/$name" --version "$version" --destination "$work/existing" 2>"$work/pull.err"; then
  tar -xzf "$work/existing/$name-$version.tgz" -C "$work/existing"
  tar -xzf "$work/$name-$version.tgz" -C "$work/candidate"
  diff -r "$work/existing/$name" "$work/candidate/$name" || {
    echo "Chart $version is already published with different contents; bump the version."
    exit 1
  }
  echo "Chart $name $version already published with matching contents."
elif grep -qiE 'not found|manifest unknown' "$work/pull.err"; then
  helm push "$work/$name-$version.tgz" "$registry"
  echo "Published $registry/$name:$version"
else
  cat "$work/pull.err" >&2
  echo 'Could not tell whether the version exists; nothing published.' >&2
  exit 1
fi
