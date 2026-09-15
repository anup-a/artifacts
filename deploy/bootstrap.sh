#!/usr/bin/env bash
# Prepares a fresh Ubuntu box to run the validation stack. Run it once, from
# a laptop:
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

# Some cloud images (Oracle's especially) ship an INPUT chain that REJECTs
# everything past ssh, so 80/443 have to be accepted explicitly and the rules
# made to survive a reboot. Insert above the REJECT where there is one; on a
# box with an empty chain the insert fails and appending is equivalent.
# (A provider-side firewall, if any, still has to allow them too.)
for port in 80 443; do
  iptables -C INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null && continue
  iptables -I INPUT 6 -p tcp --dport "$port" -j ACCEPT 2>/dev/null ||
    iptables -A INPUT -p tcp --dport "$port" -j ACCEPT
done
netfilter-persistent save

install -d -m 755 /opt/artifacts
# The server runs as the distroless nonroot uid, which must own its data dir.
install -d -o 65532 -g 65532 -m 755 /opt/artifacts/data
usermod -aG docker "$SUDO_USER"
docker --version
REMOTE

echo "bootstrapped $TARGET"
