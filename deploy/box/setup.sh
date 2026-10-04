#!/usr/bin/env bash
# Prepares a fresh Ubuntu 24.04 VPS (OVHcloud VPS-1 today; any provider works)
# to run byagent behind a Cloudflare Tunnel. Run it once, from a laptop:
#
#   ./deploy/box/setup.sh ubuntu@<vps ip>
#
# ubuntu@ is OVH's default sudo user. Afterwards everything (ship.sh,
# migrate.sh, CI) logs in as root with a key:
# - root gets this laptop's key, plus a CI deploy key (~/.ssh/byagent_deploy,
#   made here if missing)
# - ssh is key-only; the firewall (ufw) admits ssh and nothing else, since web
#   traffic arrives through the tunnel, which the box dials out to
# - Docker, unattended security updates, the app's directories
# - a nightly snapshot of the data at 02:17 UTC, kept for 5 days (the privacy
#   policy's backup window)
# Re-running is safe.
set -euo pipefail
TARGET="${1:?usage: setup.sh ubuntu@host}"
DEPLOY_KEY="$HOME/.ssh/byagent_deploy"
[ -f "$DEPLOY_KEY" ] || ssh-keygen -q -t ed25519 -N "" -C "byagent-ci" -f "$DEPLOY_KEY"
LAPTOP_PUB="$(cat "$HOME/.ssh/id_ed25519.pub")"
DEPLOY_PUB="$(cat "$DEPLOY_KEY.pub")"

ssh -o StrictHostKeyChecking=accept-new "$TARGET" \
  "sudo LAPTOP_PUB='$LAPTOP_PUB' DEPLOY_PUB='$DEPLOY_PUB' bash -euo pipefail -s" <<'REMOTE'
export DEBIAN_FRONTEND=noninteractive

install -d -m 700 /root/.ssh
touch /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys
for key in "$LAPTOP_PUB" "$DEPLOY_PUB"; do
  grep -qxF "$key" /root/.ssh/authorized_keys || echo "$key" >>/root/.ssh/authorized_keys
done
cat >/etc/ssh/sshd_config.d/10-byagent.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
EOF
systemctl reload ssh

apt-get update -qq
apt-get upgrade -y -qq
apt-get install -y -qq docker.io docker-compose-v2 unattended-upgrades sqlite3 ufw curl
cat >/etc/apt/apt.conf.d/52byagent-upgrades <<'EOF'
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:30";
EOF
systemctl enable --now docker

# Published container ports bypass ufw, which is why compose binds the app to
# 127.0.0.1 only. ufw just keeps everything else shut.
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow OpenSSH >/dev/null
ufw --force enable >/dev/null

install -d -m 755 /opt/artifacts
# The server runs as the distroless nonroot uid, which owns its data.
install -d -o 65532 -g 65532 -m 750 /opt/artifacts/data /opt/artifacts/backups

cat >/usr/local/bin/byagent-backup <<'EOF'
#!/usr/bin/env bash
# Nightly: snapshot the live data (consistent while the server runs) and keep
# five days of them.
set -euo pipefail
cd /opt/artifacts
[ -f .env ] && [ -f docker-compose.yml ] || exit 0
docker compose run --rm --no-deps -T -v /opt/artifacts/backups:/backups \
  --entrypoint /artifacts-server artifacts snapshot "/backups/byagent-$(date -u +%F).tgz"
find /opt/artifacts/backups -name 'byagent-*.tgz' -mtime +4 -delete
EOF
chmod 755 /usr/local/bin/byagent-backup
echo '17 2 * * * root /usr/local/bin/byagent-backup >>/var/log/byagent-backup.log 2>&1' >/etc/cron.d/byagent-backup

docker --version
docker compose version
REMOTE

HOST="${TARGET#*@}"
ssh -o BatchMode=yes "root@$HOST" true && echo "==> root@$HOST ready"
echo "GitHub secrets for CI deploys:"
echo "  BOX_HOST=$HOST"
echo "  BOX_SSH_KEY=<contents of $DEPLOY_KEY>"
echo "  BOX_KNOWN_HOSTS=$(ssh-keyscan -t ed25519 "$HOST" 2>/dev/null)"
