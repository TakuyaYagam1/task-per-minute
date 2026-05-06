#!/usr/bin/env bash

set -Eeuo pipefail

APP_USER="${APP_USER:-ctf}"
APP_GROUP="${APP_GROUP:-$APP_USER}"
APP_DIR="${APP_DIR:-/opt/task-per-minute}"
DEPLOY_USER="${DEPLOY_USER:-${SUDO_USER:-}}"
ENV_FILE="${ENV_FILE:-$APP_DIR/.env}"
CONFIGURE_UFW="${CONFIGURE_UFW:-1}"

if [[ "$DEPLOY_USER" == "root" ]]; then
	DEPLOY_USER=""
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_TEMPLATE="${ENV_TEMPLATE:-$APP_DIR/.env.example}"

log() {
	printf '[bootstrap] %s\n' "$*"
}

die() {
	printf '[bootstrap] error: %s\n' "$*" >&2
	exit 1
}

require_root() {
	if [[ "${EUID}" -ne 0 ]]; then
		die "run as root: sudo $0"
	fi
}

command_exists() {
	command -v "$1" >/dev/null 2>&1
}

deploy_user_enabled() {
	[[ -n "$DEPLOY_USER" && "$DEPLOY_USER" != "$APP_USER" ]]
}

compose_user() {
	if deploy_user_enabled; then
		printf '%s' "$DEPLOY_USER"
		return
	fi
	printf '%s' "$APP_USER"
}

apt_install() {
	DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"
}

ensure_apt_ready() {
	if ! command_exists apt-get; then
		die "this bootstrap script supports apt-based Ubuntu/Debian hosts"
	fi
	apt-get update
	apt_install ca-certificates curl gnupg
}

ensure_git() {
	if command_exists git; then
		log "git already installed: $(git --version)"
		return
	fi

	log "installing git"
	ensure_apt_ready
	apt_install git
}

install_docker() {
	if command_exists docker; then
		log "docker already installed: $(docker --version)"
	else
		log "installing Docker Engine with official get-docker.sh"
		ensure_apt_ready
		tmp_script="$(mktemp)"
		curl -fsSL https://get.docker.com -o "$tmp_script"
		sh "$tmp_script"
		rm -f "$tmp_script"
	fi

	if docker compose version >/dev/null 2>&1; then
		log "docker compose plugin ready: $(docker compose version --short 2>/dev/null || docker compose version)"
		return
	fi

	log "docker compose plugin is missing; installing docker-compose-plugin"
	ensure_apt_ready
	apt_install docker-compose-plugin
	docker compose version >/dev/null 2>&1 || die "docker compose plugin is still unavailable"
}

nologin_shell() {
	if [[ -x /usr/sbin/nologin ]]; then
		printf '/usr/sbin/nologin'
		return
	fi
	printf '/sbin/nologin'
}

ensure_app_user() {
	if ! getent group "$APP_GROUP" >/dev/null; then
		log "creating system group $APP_GROUP"
		groupadd --system "$APP_GROUP"
	fi

	if id "$APP_USER" >/dev/null 2>&1; then
		log "user $APP_USER already exists"
		usermod --home "$APP_DIR" --shell "$(nologin_shell)" "$APP_USER"
	else
		log "creating system user $APP_USER"
		useradd \
			--system \
			--gid "$APP_GROUP" \
			--home-dir "$APP_DIR" \
			--shell "$(nologin_shell)" \
			"$APP_USER"
	fi

	if getent group docker >/dev/null; then
		usermod -aG docker "$APP_USER"
	fi
}

ensure_deploy_user() {
	if ! deploy_user_enabled; then
		return
	fi
	if ! id "$DEPLOY_USER" >/dev/null 2>&1; then
		die "DEPLOY_USER $DEPLOY_USER does not exist; create it first or run with DEPLOY_USER="
	fi

	log "granting $DEPLOY_USER access to group $APP_GROUP"
	usermod -aG "$APP_GROUP" "$DEPLOY_USER"
	if getent group docker >/dev/null; then
		usermod -aG docker "$DEPLOY_USER"
	fi
}

ensure_app_dir() {
	log "ensuring $APP_DIR"
	if deploy_user_enabled; then
		install -d -m 2770 -o "$DEPLOY_USER" -g "$APP_GROUP" "$APP_DIR"
		return
	fi
	install -d -m 0750 -o "$APP_USER" -g "$APP_GROUP" "$APP_DIR"
}

ensure_env_file() {
	local owner="$APP_USER"
	local mode="0600"
	if deploy_user_enabled; then
		owner="$DEPLOY_USER"
		mode="0640"
	fi

	if [[ -e "$ENV_FILE" ]]; then
		log "$ENV_FILE already exists; preserving contents"
	else
		if [[ -f "$ENV_TEMPLATE" ]]; then
			log "creating $ENV_FILE from $ENV_TEMPLATE"
			install -m "$mode" -o "$owner" -g "$APP_GROUP" "$ENV_TEMPLATE" "$ENV_FILE"
		elif [[ -f "$REPO_ROOT/.env.example" ]]; then
			log "creating $ENV_FILE from local .env.example"
			install -m "$mode" -o "$owner" -g "$APP_GROUP" "$REPO_ROOT/.env.example" "$ENV_FILE"
		else
			log "creating empty $ENV_FILE"
			install -m "$mode" -o "$owner" -g "$APP_GROUP" /dev/null "$ENV_FILE"
		fi
	fi

	chown "$owner:$APP_GROUP" "$ENV_FILE"
	chmod "$mode" "$ENV_FILE"
}

configure_ufw() {
	if [[ "$CONFIGURE_UFW" != "1" ]]; then
		log "ufw configuration skipped"
		return
	fi

	if ! command_exists ufw; then
		log "installing ufw"
		ensure_apt_ready
		apt_install ufw
	fi

	log "ensuring ufw rules for ssh/http/https"
	ufw allow 22/tcp comment 'task-per-minute ssh' >/dev/null
	ufw allow 80/tcp comment 'task-per-minute http' >/dev/null
	ufw allow 443/tcp comment 'task-per-minute https' >/dev/null

	if ufw status | grep -qi inactive; then
		log "ufw is inactive; rules are prepared, firewall was not enabled automatically"
	else
		ufw reload >/dev/null
	fi
}

main() {
	require_root
	install_docker
	ensure_git
	ensure_app_user
	ensure_deploy_user
	ensure_app_dir
	ensure_env_file
	configure_ufw

	log "done"
	if deploy_user_enabled; then
		log "DEPLOY_USER=$DEPLOY_USER was added to docker/$APP_GROUP; re-login may be required for new groups"
	fi
	log "fill $ENV_FILE, then run:"
	log "cd $APP_DIR/deployment/docker"
	log "sudo -u $(compose_user) docker compose --env-file ../../.env up -d --remove-orphans"
}

main "$@"
