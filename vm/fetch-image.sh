#!/usr/bin/env bash
# Downloads one Ubuntu cloud image as a read-only base, derives three
# copy-on-write overlays from it, and builds a cloud-init seed for each target.
# Re-running is idempotent: existing artifacts are left in place.
set -euo pipefail

VM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLOUD_INIT_DIR="${VM_DIR}/cloud-init"

IMAGE_URL="${IMAGE_URL:-https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img}"
BASE_IMG="${VM_DIR}/base/ubuntu-base.img"
OVERLAY_SIZE="${OVERLAY_SIZE:-10G}"
TARGETS=(vm1 vm2 vm3)

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: missing required tool: $1" >&2; exit 1; }; }

need qemu-img
need curl

# 1. Base image (downloaded once, then treated as read-only).
mkdir -p "${VM_DIR}/base"
if [[ ! -f "${BASE_IMG}" ]]; then
  echo "downloading base image: ${IMAGE_URL}"
  curl -fSL "${IMAGE_URL}" -o "${BASE_IMG}.tmp"
  mv "${BASE_IMG}.tmp" "${BASE_IMG}"
else
  echo "base image present: ${BASE_IMG}"
fi

# 2. Copy-on-write overlay + cloud-init seed per target.
build_seed() {
  local vm="$1" seed="$2"
  local src="${CLOUD_INIT_DIR}/${vm}"
  if command -v cloud-localds >/dev/null 2>&1; then
    cloud-localds "${seed}" "${src}/user-data" "${src}/meta-data"
  elif command -v genisoimage >/dev/null 2>&1; then
    genisoimage -output "${seed}" -volid cidata -joliet -rock \
      "${src}/user-data" "${src}/meta-data"
  elif command -v mkisofs >/dev/null 2>&1; then
    mkisofs -output "${seed}" -volid cidata -joliet -rock \
      "${src}/user-data" "${src}/meta-data"
  elif command -v xorriso >/dev/null 2>&1; then
    xorriso -as mkisofs -output "${seed}" -volid cidata -joliet -rock \
      "${src}/user-data" "${src}/meta-data"
  else
    echo "error: need cloud-localds, genisoimage, mkisofs, or xorriso to build seeds" >&2
    echo "       install one, e.g.:  sudo apt-get install -y cloud-image-utils" >&2
    exit 1
  fi
}

for vm in "${TARGETS[@]}"; do
  overlay="${VM_DIR}/overlay-${vm}.qcow2"
  seed="${VM_DIR}/seed-${vm}.img"

  if [[ ! -f "${overlay}" ]]; then
    echo "creating overlay: ${overlay}"
    qemu-img create -f qcow2 -b "${BASE_IMG}" -F qcow2 "${overlay}" "${OVERLAY_SIZE}"
  else
    echo "overlay present: ${overlay}"
  fi

  echo "building seed: ${seed}"
  build_seed "${vm}" "${seed}"
done

echo "done. overlays and seeds are in ${VM_DIR}"
