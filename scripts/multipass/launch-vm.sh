#!/usr/bin/env bash
set -euo pipefail

VM_NAME="clawker"
CPUS=6
MEMORY="12G"
DISK="80G"
BRIDGE_IFACE="en0"  # change if your active interface differs
TIMEOUT=600

echo "==> Available networks (verify $BRIDGE_IFACE is your active interface):"
multipass networks
echo

read -rp "Continue with bridge interface '$BRIDGE_IFACE'? [y/N] " confirm
if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
  echo "Aborted. Edit BRIDGE_IFACE in the script and re-run."
  exit 1
fi

echo "==> Launching VM '$VM_NAME' ($CPUS CPU, $MEMORY RAM, $DISK disk)"
multipass launch lts \
  --name "$VM_NAME" \
  --cpus "$CPUS" \
  --memory "$MEMORY" \
  --disk "$DISK" \
  --network "name=$BRIDGE_IFACE,mode=auto" \
  --timeout "$TIMEOUT"

echo
echo "==> VM info:"
multipass info "$VM_NAME"

echo
echo "==> Done. Shell in with: multipass shell $VM_NAME"
