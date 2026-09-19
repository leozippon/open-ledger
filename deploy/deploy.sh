#!/usr/bin/env bash
# Build the ledger for Linux and install it on the server prepared by
# deploy/server/provision.sh. Run from the workstation:
#
#   deploy/deploy.sh [ssh-host]
#
# The host defaults to your-server and must be reachable as root over SSH.
set -euo pipefail

HOST=${1:-your-server}
BINARY=dist/ledger-linux-amd64
FILTER=deploy/fail2ban/filter.d/ledger.conf
JAIL=deploy/fail2ban/jail.d/ledger.local

cd "$(dirname "${BASH_SOURCE[0]}")/.."

log() { printf '\n==> %s\n' "$*"; }
die() {
	printf 'deploy: %s\n' "$*" >&2
	exit 1
}

digest() {
	if command -v sha256sum >/dev/null; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

log "Building $BINARY"
make build-linux

# fail2ban is only reloaded when its ledger files actually change, so a routine
# deploy does not disturb the active bans.
remote_digests=$(ssh "$HOST" 'sha256sum /etc/fail2ban/filter.d/ledger.conf /etc/fail2ban/jail.d/ledger.local 2>/dev/null' || true)
fail2ban_changed=no
if [[ $remote_digests != *"$(digest "$FILTER")"* || $remote_digests != *"$(digest "$JAIL")"* ]]; then
	fail2ban_changed=yes
fi

log "Uploading to $HOST"
scp -q "$BINARY" "$HOST:/usr/local/bin/ledger.new"
scp -q deploy/ledger.service "$HOST:/etc/systemd/system/ledger.service"
if [[ $fail2ban_changed == yes ]]; then
	scp -q "$FILTER" "$HOST:/etc/fail2ban/filter.d/ledger.conf"
	scp -q "$JAIL" "$HOST:/etc/fail2ban/jail.d/ledger.local"
fi

log "Restarting ledger"
# shellcheck disable=SC2029 # the flag is expanded here on purpose
ssh "$HOST" "FAIL2BAN_CHANGED=$fail2ban_changed bash -s" <<'EOF'
set -euo pipefail
chmod 0755 /usr/local/bin/ledger.new
# Renaming swaps the binary in a single step; the running process keeps the old
# one until systemd restarts it below.
mv -f /usr/local/bin/ledger.new /usr/local/bin/ledger
systemctl daemon-reload
systemctl restart ledger
if [ "$FAIL2BAN_CHANGED" = yes ]; then
	systemctl reload fail2ban
fi
EOF

log "Status"
if ! ssh "$HOST" 'systemctl is-active ledger'; then
	ssh "$HOST" 'journalctl -u ledger -n 20 --no-pager' || true
	die "ledger did not come back up on $HOST"
fi
ssh "$HOST" 'journalctl -u ledger -n 10 --no-pager'
