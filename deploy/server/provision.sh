#!/usr/bin/env bash
# Bring a fresh Debian 13 host to the state the ledger server runs in.
# Safe to re-run: existing /etc/ledger/env and certificates are left alone;
# SSH, firewall and packages are applied again. Run as root on the server:
#
#   bash /root/server/provision.sh --public-ip 203.0.113.7
#
# The ledger binary, its systemd unit and its fail2ban jail are not installed
# here; deploy/deploy.sh pushes those from the workstation afterwards.
set -euo pipefail

SERVER_HOSTNAME=your-server
ADMIN_USER="admin"
PUBLIC_IP=

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

usage() {
	cat <<'EOF'
usage: provision.sh [--hostname NAME] [--admin-user NAME] [--public-ip ADDR]

  --hostname    system hostname, also the certificate subject (default your-server)
  --admin-user  first ledger administrator, used only when /etc/ledger/env is
                created (default admin)
  --public-ip   address written into the certificate; detected over the network
                when a certificate has to be generated and this is omitted
EOF
}

log() { printf '\n==> %s\n' "$*"; }
note() { printf '    %s\n' "$*"; }
die() {
	printf 'provision: %s\n' "$*" >&2
	exit 1
}

while [[ $# -gt 0 ]]; do
	case $1 in
	--hostname)
		SERVER_HOSTNAME=$2
		shift 2
		;;
	--admin-user)
		ADMIN_USER=$2
		shift 2
		;;
	--public-ip)
		PUBLIC_IP=$2
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

[[ $(id -u) -eq 0 ]] || die "must run as root"
[[ -r /etc/debian_version ]] || die "this script targets Debian"

install_config() { # install_config <source> <destination> <mode>
	install -D -m "$3" -o root -g root "$HERE/$1" "$2"
}

# random_string emits alphanumeric characters only, so the value is safe to read
# back from a shell-sourced environment file.
random_string() {
	local length=$1 value
	value=$(openssl rand -base64 96 | tr -dc 'A-Za-z0-9')
	printf '%s' "${value:0:length}"
}

upgrade_system() {
	log "Updating packages"
	export DEBIAN_FRONTEND=noninteractive
	local keep=(-y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)
	apt-get update
	apt-get "${keep[@]}" full-upgrade
	apt-get "${keep[@]}" install unattended-upgrades fail2ban nftables ca-certificates curl openssl
	apt-get "${keep[@]}" autoremove
}

set_hostname() {
	log "Setting the hostname to $SERVER_HOSTNAME"
	hostnamectl set-hostname "$SERVER_HOSTNAME"
	local hosts_line
	hosts_line=$(printf '127.0.1.1\t%s' "$SERVER_HOSTNAME")
	if grep -qE '^127\.0\.1\.1[[:space:]]' /etc/hosts; then
		sed -i -E "s|^127\.0\.1\.1[[:space:]].*|$hosts_line|" /etc/hosts
	else
		printf '%s\n' "$hosts_line" >>/etc/hosts
	fi
	# Without this, cloud-init resets the hostname on the next boot.
	if [[ -f /etc/cloud/cloud.cfg ]] && grep -qE '^[[:space:]]*preserve_hostname:' /etc/cloud/cloud.cfg; then
		sed -i -E 's|^([[:space:]]*)preserve_hostname:.*|\1preserve_hostname: true|' /etc/cloud/cloud.cfg
	fi
}

harden_ssh() {
	log "Restricting SSH to public keys"
	# Turning off passwords without a usable key would lock everyone out.
	[[ -s /root/.ssh/authorized_keys ]] ||
		die "/root/.ssh/authorized_keys is missing or empty; install your key before hardening SSH"
	local drop_in=/etc/ssh/sshd_config.d/10-hardening.conf
	install_config sshd-hardening.conf "$drop_in" 0644
	if ! sshd -t; then
		rm -f "$drop_in"
		die "sshd rejected the hardening configuration; it has been removed again"
	fi
	systemctl restart ssh
	note "existing sessions stay open; open a second one to confirm before closing this one"
}

setup_firewall() {
	log "Loading the firewall"
	# Check before installing so a bad ruleset never becomes the boot-time one.
	nft -c -f "$HERE/nftables.conf" || die "nftables rejected deploy/server/nftables.conf"
	install_config nftables.conf /etc/nftables.conf 0755
	systemctl enable --now nftables
	nft -f /etc/nftables.conf
}

setup_fail2ban() {
	log "Configuring fail2ban for SSH"
	install_config fail2ban-sshd.local /etc/fail2ban/jail.d/sshd.local 0644
	systemctl enable --now fail2ban
	systemctl restart fail2ban
}

setup_auto_upgrades() {
	log "Enabling unattended upgrades"
	install_config apt-auto-upgrades /etc/apt/apt.conf.d/20auto-upgrades 0644
	local defaults=/etc/apt/apt.conf.d/50unattended-upgrades
	if [[ -f $defaults ]]; then
		sed -i -E 's|^//([[:space:]]*Unattended-Upgrade::Remove-Unused-Dependencies[[:space:]]+"true";)|\1|' "$defaults"
	fi
	systemctl enable --now unattended-upgrades
}

setup_app_environment() {
	log "Preparing the ledger service account and configuration"
	getent group ledger >/dev/null || groupadd --system ledger
	id -u ledger >/dev/null 2>&1 ||
		useradd --system --gid ledger --home-dir /var/lib/ledger --shell /usr/sbin/nologin ledger
	install -d -o ledger -g ledger -m 0750 /var/lib/ledger
	install -d -o root -g ledger -m 0750 /etc/ledger
	write_env
	write_certificate
}

# write_env never touches an existing file: it holds the session secret, and
# replacing it would sign every member out and orphan the stored passwords.
write_env() {
	local env_file=/etc/ledger/env
	if [[ -e $env_file ]]; then
		note "$env_file exists, left untouched"
		return
	fi
	local password secret
	password=$(random_string 16)
	secret=$(random_string 48)
	(
		umask 077
		cat >"$env_file" <<EOF
LEDGER_ADDR=:18080
LEDGER_DATA=/var/lib/ledger
LEDGER_DB=/var/lib/ledger/ledger.db
LEDGER_ADMIN_USER=$ADMIN_USER
LEDGER_ADMIN_PASSWORD=$password
LEDGER_SECRET=$secret
LEDGER_TLS_CERT=/etc/ledger/tls.crt
LEDGER_TLS_KEY=/etc/ledger/tls.key
LEDGER_DEEPSEEK_KEY=
EOF
	)
	chown root:ledger "$env_file"
	chmod 0640 "$env_file"
	log "First administrator: $ADMIN_USER / $password"
	note "this password is shown once; sign in and change it in the app"
}

write_certificate() {
	local crt=/etc/ledger/tls.crt key=/etc/ledger/tls.key
	if [[ -e $crt && -e $key ]]; then
		note "$crt exists, left untouched"
		return
	fi
	[[ ! -e $crt && ! -e $key ]] ||
		die "only one of $crt and $key exists; remove the leftover before provisioning"
	local address
	address=$(resolve_public_ip)
	log "Generating a self-signed certificate for $SERVER_HOSTNAME ($address)"
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
		-keyout "$key" -out "$crt" -subj "/CN=$SERVER_HOSTNAME" \
		-addext "subjectAltName=IP:$address,DNS:$SERVER_HOSTNAME"
	chown root:ledger "$key" "$crt"
	chmod 0640 "$key"
	chmod 0644 "$crt"
	note "browsers warn once about this certificate; accept it and they remember"
}

resolve_public_ip() {
	if [[ -n $PUBLIC_IP ]]; then
		printf '%s' "$PUBLIC_IP"
		return
	fi
	local found
	found=$(curl -fsS --max-time 10 https://ipinfo.io/ip | tr -d '[:space:]' || true)
	[[ $found =~ ^[0-9]+(\.[0-9]+){3}$ ]] ||
		die "could not detect the public address; pass --public-ip"
	printf '%s' "$found"
}

upgrade_system
set_hostname
harden_ssh
setup_firewall
setup_fail2ban
setup_auto_upgrades
setup_app_environment

log "Done"
note "next: run deploy/deploy.sh from the workstation to install the ledger binary"
if [[ -e /var/run/reboot-required ]]; then
	note "a reboot is required to finish applying the upgrades"
fi
