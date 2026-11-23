#!/usr/bin/env bash
# Helm and kubeconform for CI, checksum-verified.
set -euo pipefail
# Download into a caller-owned temporary directory; never change system tools.
destination=${1:?usage: install-chart-tools.sh TEMP_DIRECTORY}
test "$(uname -s)" = Linux && test "$(uname -m)" = x86_64
mkdir -p "$destination"
curl --fail --silent --show-error --location \
  https://get.helm.sh/helm-v4.3.0-linux-amd64.tar.gz -o "$destination/helm.tar.gz"
printf '%s  %s\n' 86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb "$destination/helm.tar.gz" | sha256sum --check
tar -xzf "$destination/helm.tar.gz" -C "$destination" linux-amd64/helm
mv "$destination/linux-amd64/helm" "$destination/helm"
curl --fail --silent --show-error --location \
  https://github.com/yannh/kubeconform/releases/download/v0.8.0/kubeconform-linux-amd64.tar.gz \
  -o "$destination/kubeconform.tar.gz"
printf '%s  %s\n' 9bc2bffbf71f261128533edaf912153948b7ff238f9a531ae6d34466ec287883 "$destination/kubeconform.tar.gz" | sha256sum --check
tar -xzf "$destination/kubeconform.tar.gz" -C "$destination" kubeconform
