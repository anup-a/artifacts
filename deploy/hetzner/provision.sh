#!/usr/bin/env bash
# Creates the byagent box on Hetzner Cloud, once:
#
#   ./deploy/hetzner/provision.sh
#
# - ssh keys: this laptop's, plus a deploy key for GitHub Actions
#   (~/.ssh/byagent_deploy, made here if missing)
# - a firewall that admits ssh and nothing else (web traffic arrives through
#   the Cloudflare Tunnel, which the box dials out to)
# - a server (Ubuntu 24.04, x86) booted with cloud-init.yml
#
# The API token comes from HCLOUD_TOKEN or the macOS Keychain
# (security add-generic-password -s hcloud -a byagent -w). Re-running is safe:
# anything that already exists is left alone.
set -euo pipefail
cd "$(dirname "$0")"

NAME="${NAME:-byagent}"
# First type that is in stock wins. cx23 (2 vCPU, 4 GB, €5.99) is the target;
# Hetzner marked every Cost-Optimized plan unavailable in October 2026, so
# cpx12 (1 AMD vCPU, 2 GB, €11.99) stands in. Both include 20 TB in the EU,
# and the server can be rescaled to cx23 later (same 40 GB disk).
TYPES="${TYPES:-cx23 cpx12}"
LOCATION="${LOCATION:-fsn1}"  # Falkenstein, Germany: 20 TB traffic included
IMAGE="ubuntu-24.04"
DEPLOY_KEY="$HOME/.ssh/byagent_deploy"

export HCLOUD_TOKEN="${HCLOUD_TOKEN:-$(security find-generic-password -s hcloud -a byagent -w 2>/dev/null || true)}"
[ -n "$HCLOUD_TOKEN" ] || { echo "no Hetzner token: set HCLOUD_TOKEN or add it to the Keychain (service hcloud, account byagent)"; exit 1; }
command -v hcloud >/dev/null || brew install hcloud

[ -f "$DEPLOY_KEY" ] || ssh-keygen -q -t ed25519 -N "" -C "byagent-ci" -f "$DEPLOY_KEY"

hcloud ssh-key describe byagent-laptop >/dev/null 2>&1 ||
  hcloud ssh-key create --name byagent-laptop --public-key-from-file "$HOME/.ssh/id_ed25519.pub"
hcloud ssh-key describe byagent-ci >/dev/null 2>&1 ||
  hcloud ssh-key create --name byagent-ci --public-key-from-file "$DEPLOY_KEY.pub"

if ! hcloud firewall describe byagent-ssh >/dev/null 2>&1; then
  hcloud firewall create --name byagent-ssh
  hcloud firewall add-rule byagent-ssh --direction in --protocol tcp --port 22 \
    --source-ips 0.0.0.0/0 --source-ips ::/0 --description ssh
fi

if ! hcloud server describe "$NAME" >/dev/null 2>&1; then
  created=""
  for type in $TYPES; do
    echo "==> trying $type in $LOCATION"
    if hcloud server create --name "$NAME" --type "$type" --location "$LOCATION" --image "$IMAGE" \
      --ssh-key byagent-laptop --ssh-key byagent-ci --firewall byagent-ssh \
      --user-data-from-file cloud-init.yml --label app=byagent; then
      created="$type"
      break
    fi
  done
  [ -n "$created" ] || { echo "none of [$TYPES] could be created in $LOCATION"; exit 1; }
fi

IP=$(hcloud server ip "$NAME")
echo "==> $NAME is at $IP; waiting for first boot to finish"
for _ in $(seq 1 60); do
  ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 "root@$IP" true 2>/dev/null && break
  sleep 5
done
ssh "root@$IP" 'cloud-init status --wait >/dev/null; docker compose version'
echo "==> ready: root@$IP"
echo "GitHub secrets for CI deploys:"
echo "  HETZNER_HOST=$IP"
echo "  HETZNER_SSH_KEY=<contents of $DEPLOY_KEY>"
echo "  HETZNER_KNOWN_HOSTS=$(ssh-keyscan -t ed25519 "$IP" 2>/dev/null)"
