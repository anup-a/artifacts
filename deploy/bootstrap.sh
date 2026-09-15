#!/usr/bin/env bash
# Prepares a fresh Ubuntu box (Oracle Always Free ARM) to run the free-tier
# stack. Run it once, from a laptop:
#
#   ./deploy/bootstrap.sh ubuntu@<box ip>
#
# It is idempotent, so re-running it after a box rebuild is fine.
set -euo pipefail
TARGET="${1:?usage: bootstrap.sh user@host}"

ssh -o StrictHostKeyChecking=accept-new "$TARGET" 'sudo bash -euo pipefail -s' <<'REMOTE'
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq docker.io docker-compose-plugin iptables-persistent

# Oracle's Ubuntu image ships an INPUT chain that REJECTs everything past ssh,
# so 80/443 have to be accepted explicitly and the rules made to survive a
# reboot. (The VCN security list still has to allow them too; that part is in
# the OCI console.)
for port in 80 443; do
  iptables -C INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null ||
    iptables -I INPUT 6 -p tcp --dport "$port" -j ACCEPT
done
netfilter-persistent save

install -d -m 755 /opt/artifacts
# The server runs as the distroless nonroot uid, which must own its data dir.
install -d -o 65532 -g 65532 -m 755 /opt/artifacts/data
usermod -aG docker "$SUDO_USER"
docker --version
REMOTE

echo "bootstrapped $TARGET"
