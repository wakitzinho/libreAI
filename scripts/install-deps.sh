#!/usr/bin/env bash
#
# install-deps.sh — check for and install libreAI's build, test and optional
# deployment dependencies.
#
# The default profile covers everything needed by `make test`, `make build`
# and `make check-binary`:
#
#   go >= 1.26, make, git, curl, CA certificates, node >= 20, sqlite3, file, awk
#
# Optional deployment tools (Docker/Compose and Tailscale) are only checked or
# installed when requested with --docker, --tailscale or --all.
#
# NixOS has no imperative package manager. On NixOS this script prints a
# configuration.nix snippet instead of modifying the system. The repository
# also provides a flake dev shell (`nix develop`) with the same packages.

set -uo pipefail

MIN_GO="1.26"
MIN_NODE="20"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(dirname -- "$SCRIPT_DIR")

CORE_DEPS=(go make git curl ca-certificates)
RECOMMENDED_DEPS=(node sqlite3 file awk)
SELECTED_OPTIONAL=()

ASSUME_YES=0
CHECK_ONLY=0
DRY_RUN=0
PRINT_NIX=0
MANAGER=""
DISTRO_ID=""
DISTRO_NAME="unknown"
APT_UPDATED=0

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	C_RESET=$'\033[0m'
	C_GREEN=$'\033[32m'
	C_YELLOW=$'\033[33m'
	C_RED=$'\033[31m'
	C_BOLD=$'\033[1m'
else
	C_RESET=""
	C_GREEN=""
	C_YELLOW=""
	C_RED=""
	C_BOLD=""
fi

log() { printf '%s\n' "$*"; }
warn() { printf '%swarning:%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
err() { printf '%serror:%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }

usage() {
	cat <<'EOF'
Usage: scripts/install-deps.sh [options]

Check for libreAI's dependencies and install anything that is missing using
the system package manager. Go >= 1.26 and Node.js >= 20 are version-checked.

Options:
  --docker       also check/install Docker and Docker Compose
  --tailscale    also check/install Tailscale
  --all          include all optional deployment dependencies
  --check        report only; exit 1 if a selected dependency is missing
  --dry-run      print what would be installed, change nothing
  --print-nix    print a configuration.nix snippet and exit
  -y, --yes      do not ask for confirmation
  -h, --help     show this help

Environment:
  NO_COLOR       disable colored output

On NixOS nothing is installed. The script prints instructions for
`nix develop` and a block to paste into configuration.nix instead.
EOF
}

add_optional() {
	local key=$1 existing
	for existing in "${SELECTED_OPTIONAL[@]}"; do
		[ "$existing" = "$key" ] && return 0
	done
	SELECTED_OPTIONAL+=("$key")
}

has_optional() {
	local key=$1 existing
	for existing in "${SELECTED_OPTIONAL[@]}"; do
		[ "$existing" = "$key" ] && return 0
	done
	return 1
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--docker)
			add_optional docker
			add_optional compose
			;;
		--tailscale)
			add_optional tailscale
			;;
		--all)
			add_optional docker
			add_optional compose
			add_optional tailscale
			;;
		--check) CHECK_ONLY=1 ;;
		--dry-run) DRY_RUN=1 ;;
		--print-nix) PRINT_NIX=1 ;;
		-y | --yes) ASSUME_YES=1 ;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			err "unknown option: $1"
			usage >&2
			exit 2
			;;
		esac
		shift
	done
}

detect_distro() {
	if [ -r /etc/os-release ]; then
		# shellcheck disable=SC1091
		. /etc/os-release
		DISTRO_ID=${ID:-unknown}
		DISTRO_NAME=${PRETTY_NAME:-${NAME:-$DISTRO_ID}}
	fi
}

is_nixos() {
	[ -e /etc/NIXOS ] || command -v nixos-rebuild >/dev/null 2>&1
}

detect_manager() {
	if is_nixos; then
		MANAGER=nixos
		return
	fi
	local candidate
	for candidate in apt-get dnf pacman zypper apk xbps-install emerge; do
		if command -v "$candidate" >/dev/null 2>&1; then
			MANAGER=$candidate
			return
		fi
	done
	MANAGER=none
}

manager_label() {
	case "$MANAGER" in
	apt-get) echo "Debian/Ubuntu (apt)" ;;
	dnf) echo "Fedora/RHEL (dnf)" ;;
	pacman) echo "Arch Linux (pacman)" ;;
	zypper) echo "openSUSE (zypper)" ;;
	apk) echo "Alpine (apk)" ;;
	xbps-install) echo "Void Linux (xbps)" ;;
	emerge) echo "Gentoo (Portage)" ;;
	nixos) echo "NixOS (declarative)" ;;
	none) echo "unsupported package manager" ;;
	*) echo "$MANAGER" ;;
	esac
}

is_present() {
	case "$1" in
	ca-certificates)
		[ -f /etc/ssl/certs/ca-certificates.crt ] ||
			[ -f /etc/pki/tls/certs/ca-bundle.crt ] ||
			[ -f /etc/ssl/cert.pem ]
		;;
	compose)
		{ command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; } ||
			command -v docker-compose >/dev/null 2>&1
		;;
	*)
		command -v "$1" >/dev/null 2>&1
		;;
	esac
}

go_version() {
	go version 2>/dev/null | awk '{print $3}' | sed -e 's/^go//' -e 's/-.*$//'
}

node_version() {
	node --version 2>/dev/null | sed 's/^v//'
}

version_ge() {
	local IFS=.
	local -a actual_parts minimum_parts
	local i max part_actual part_minimum
	read -r -a actual_parts <<< "$1"
	read -r -a minimum_parts <<< "$2"
	max=${#actual_parts[@]}
	[ ${#minimum_parts[@]} -gt "$max" ] && max=${#minimum_parts[@]}
	for ((i = 0; i < max; i++)); do
		part_actual=${actual_parts[i]:-0}
		part_minimum=${minimum_parts[i]:-0}
		[[ "$part_actual" =~ ^[0-9]+$ ]] || part_actual=0
		[[ "$part_minimum" =~ ^[0-9]+$ ]] || part_minimum=0
		if [ "$part_actual" -gt "$part_minimum" ]; then
			return 0
		elif [ "$part_actual" -lt "$part_minimum" ]; then
			return 1
		fi
	done
	return 0
}

dep_state() {
	local key=$1 version
	is_present "$key" || {
		echo missing
		return
	}
	case "$key" in
	go)
		version=$(go_version)
		if [ -n "$version" ] && ! version_ge "$version" "$MIN_GO"; then
			echo old
		else
			echo present
		fi
		;;
	node)
		version=$(node_version)
		if [ -n "$version" ] && ! version_ge "$version" "$MIN_NODE"; then
			echo old
		else
			echo present
		fi
		;;
	*) echo present ;;
	esac
}

dep_label() {
	case "$1" in
	ca-certificates) echo "CA certificates" ;;
	compose) echo "docker compose" ;;
	sqlite3) echo "sqlite3" ;;
	*) echo "$1" ;;
	esac
}

dep_detail() {
	case "$1" in
	go) printf '%s' "$(go_version)" ;;
	node) printf '%s' "$(node_version)" ;;
	compose)
		if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
			printf 'plugin'
		elif command -v docker-compose >/dev/null 2>&1; then
			printf 'standalone'
		fi
		;;
	*) printf '' ;;
	esac
}

all_selected_deps() {
	printf '%s\n' "${CORE_DEPS[@]}" "${RECOMMENDED_DEPS[@]}" "${SELECTED_OPTIONAL[@]}"
}

any_selected_missing() {
	local key
	while IFS= read -r key; do
		[ -n "$key" ] || continue
		if [ "$(dep_state "$key")" != present ]; then
			return 0
		fi
	done < <(all_selected_deps)
	return 1
}

print_report() {
	local key state detail label
	log "${C_BOLD}libreAI dependency check${C_RESET}"
	log "  Host:    $DISTRO_NAME"
	log "  Manager: $(manager_label)"
	if [ ${#SELECTED_OPTIONAL[@]} -gt 0 ]; then
		log "  Profile: build/test + ${SELECTED_OPTIONAL[*]}"
	else
		log "  Profile: build/test"
	fi
	log ""
	while IFS= read -r key; do
		[ -n "$key" ] || continue
		state=$(dep_state "$key")
		detail=$(dep_detail "$key")
		label=$(dep_label "$key")
		case "$state" in
		present)
			printf '  %s[ok]%s      %s' "$C_GREEN" "$C_RESET" "$label"
			[ -n "$detail" ] && printf ' %s' "$detail"
			printf '\n'
			;;
		old)
			printf '  %s[too old]%s %s %s (need >= %s)\n' \
				"$C_YELLOW" "$C_RESET" "$label" "$detail" \
				"$([ "$key" = go ] && echo "$MIN_GO" || echo "$MIN_NODE")"
			;;
		*)
			printf '  %s[missing]%s %s\n' "$C_RED" "$C_RESET" "$label"
			;;
		esac
	done < <(all_selected_deps)
}

package_for() {
	local key=$1
	case "$MANAGER:$key" in
	# Debian / Ubuntu
	apt-get:go) echo golang-go ;;
	apt-get:make) echo make ;;
	apt-get:git) echo git ;;
	apt-get:curl) echo curl ;;
	apt-get:ca-certificates) echo ca-certificates ;;
	apt-get:node) echo nodejs ;;
	apt-get:sqlite3) echo sqlite3 ;;
	apt-get:file) echo file ;;
	apt-get:awk) echo gawk ;;
	apt-get:docker) echo docker.io ;;
	apt-get:compose) echo docker-compose-plugin ;;
	apt-get:tailscale) echo tailscale ;;
	# Fedora / RHEL
	dnf:go) echo golang ;;
	dnf:make) echo make ;;
	dnf:git) echo git ;;
	dnf:curl) echo curl ;;
	dnf:ca-certificates) echo ca-certificates ;;
	dnf:node) echo nodejs ;;
	dnf:sqlite3) echo sqlite ;;
	dnf:file) echo file ;;
	dnf:awk) echo gawk ;;
	dnf:docker) echo docker ;;
	dnf:compose) echo docker-compose ;;
	dnf:tailscale) echo tailscale ;;
	# Arch Linux
	pacman:go) echo go ;;
	pacman:make) echo make ;;
	pacman:git) echo git ;;
	pacman:curl) echo curl ;;
	pacman:ca-certificates) echo ca-certificates ;;
	pacman:node) echo nodejs ;;
	pacman:sqlite3) echo sqlite ;;
	pacman:file) echo file ;;
	pacman:awk) echo gawk ;;
	pacman:docker) echo docker ;;
	pacman:compose) echo docker-compose ;;
	pacman:tailscale) echo tailscale ;;
	# openSUSE
	zypper:go) echo go ;;
	zypper:make) echo make ;;
	zypper:git) echo git ;;
	zypper:curl) echo curl ;;
	zypper:ca-certificates) echo ca-certificates ;;
	zypper:node) echo nodejs ;;
	zypper:sqlite3) echo sqlite3 ;;
	zypper:file) echo file ;;
	zypper:awk) echo gawk ;;
	zypper:docker) echo docker ;;
	zypper:compose) echo docker-compose ;;
	zypper:tailscale) echo tailscale ;;
	# Alpine
	apk:go) echo go ;;
	apk:make) echo make ;;
	apk:git) echo git ;;
	apk:curl) echo curl ;;
	apk:ca-certificates) echo ca-certificates ;;
	apk:node) echo nodejs ;;
	apk:sqlite3) echo sqlite ;;
	apk:file) echo file ;;
	apk:awk) echo gawk ;;
	apk:docker) echo docker ;;
	apk:compose) echo docker-cli-compose ;;
	apk:tailscale) echo tailscale ;;
	# Void Linux
	xbps-install:go) echo go ;;
	xbps-install:make) echo make ;;
	xbps-install:git) echo git ;;
	xbps-install:curl) echo curl ;;
	xbps-install:ca-certificates) echo ca-certificates ;;
	xbps-install:node) echo nodejs ;;
	xbps-install:sqlite3) echo sqlite ;;
	xbps-install:file) echo file ;;
	xbps-install:awk) echo gawk ;;
	xbps-install:docker) echo docker ;;
	xbps-install:compose) echo docker-compose ;;
	xbps-install:tailscale) echo tailscale ;;
	# Gentoo
	emerge:go) echo dev-lang/go ;;
	emerge:make) echo sys-devel/make ;;
	emerge:git) echo dev-vcs/git ;;
	emerge:curl) echo net-misc/curl ;;
	emerge:ca-certificates) echo app-misc/ca-certificates ;;
	emerge:node) echo net-libs/nodejs ;;
	emerge:sqlite3) echo dev-db/sqlite ;;
	emerge:file) echo sys-apps/file ;;
	emerge:awk) echo sys-apps/gawk ;;
	emerge:docker) echo app-containers/docker ;;
	emerge:compose) echo app-containers/docker-compose ;;
	emerge:tailscale) echo net-vpn/tailscale ;;
	*) echo "" ;;
	esac
}

collect_missing() {
	local target=$1
	shift
	local -n out=$target
	out=()
	local key
	for key in "$@"; do
		[ "$(dep_state "$key")" = present ] || out+=("$key")
	done
}

packages_for_keys() {
	local target=$1
	shift
	local -n out=$target
	out=()
	local key package existing found
	for key in "$@"; do
		package=$(package_for "$key")
		if [ -z "$package" ]; then
			warn "no package name known for '$key' on $(manager_label); install it manually"
			continue
		fi
		found=0
		for existing in "${out[@]}"; do
			if [ "$existing" = "$package" ]; then
				found=1
				break
			fi
		done
		[ "$found" -eq 1 ] || out+=("$package")
	done
}

run_priv() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	elif command -v sudo >/dev/null 2>&1; then
		sudo "$@"
	else
		err "root privileges are required, but sudo is not installed"
		return 1
	fi
}

run_install() {
	local packages=("$@")
	[ ${#packages[@]} -eq 0 ] && return 0
	case "$MANAGER" in
	apt-get)
		if [ "$APT_UPDATED" -eq 0 ]; then
			if ! run_priv apt-get update; then
				warn "apt-get update failed; trying the existing package lists"
			fi
			APT_UPDATED=1
		fi
		run_priv apt-get install -y --no-install-recommends "${packages[@]}"
		;;
	dnf) run_priv dnf install -y "${packages[@]}" ;;
	pacman) run_priv pacman -S --needed --noconfirm "${packages[@]}" ;;
	zypper) run_priv zypper --non-interactive install "${packages[@]}" ;;
	apk) run_priv apk add --no-cache "${packages[@]}" ;;
	xbps-install) run_priv xbps-install -Sy "${packages[@]}" ;;
	emerge) run_priv emerge --noreplace "${packages[@]}" ;;
	*)
		err "do not know how to install packages with $MANAGER"
		return 1
		;;
	esac
}

install_group() {
	local label=$1
	shift
	local keys=("$@")
	local packages=()
	packages_for_keys packages "${keys[@]}"
	[ ${#packages[@]} -eq 0 ] && return 0
	log "Installing $label: ${keys[*]}"
	if ! run_install "${packages[@]}"; then
		warn "could not install $label (${keys[*]})"
		return 1
	fi
	return 0
}

print_dry_run_group() {
	local label=$1
	shift
	local keys=("$@")
	local packages=()
	packages_for_keys packages "${keys[@]}"
	[ ${#packages[@]} -eq 0 ] && return 0
	printf '  would install %s: %s\n' "$label" "${keys[*]}"
	printf '    packages: %s\n' "${packages[*]}"
}

print_manual_requirements() {
	cat <<'EOF' >&2

Install these with your platform's package manager, then re-run this script:

  Required to build/test:
    Go 1.26 or newer, GNU make, git, curl, CA certificates
  Recommended:
    Node.js 20 or newer, sqlite3, file, gawk (or another awk)
  Optional deployment:
    Docker + Docker Compose, Tailscale
EOF
}

print_nix_block() {
	local with_header=${1:-1}
	if [ "$with_header" -eq 1 ]; then
		cat <<'EOF'
# Paste this inside the top-level { ... } of /etc/nixos/configuration.nix,
# then run: sudo nixos-rebuild switch
#
# Alternatively, enter the repository's development shell without changing
# the system:
#   cd /path/to/libreAI && nix develop

EOF
	fi
	cat <<'EOF'
  environment.systemPackages = with pkgs; [
    # libreAI build and test dependencies
    go
    gnumake
    git
    curl
    cacert
    nodejs
    sqlite
    file
    gawk
EOF
	if has_optional docker; then
		cat <<'EOF'

    # Optional deployment tools
    docker
    docker-compose
EOF
	else
		cat <<'EOF'

    # Optional deployment tools (uncomment to enable)
    # docker
    # docker-compose
EOF
	fi
	if has_optional tailscale; then
		echo "    tailscale"
	else
		echo "    # tailscale"
	fi
	cat <<'EOF'
  ];
EOF
	if has_optional docker; then
		echo ""
		echo "  virtualisation.docker.enable = true;"
	else
		echo ""
		echo "  # virtualisation.docker.enable = true; # required for Docker"
	fi
	if has_optional tailscale; then
		echo ""
		echo "  services.tailscale.enable = true;"
	else
		echo ""
		echo "  # services.tailscale.enable = true;    # required for Tailscale"
	fi
}

print_nixos_instructions() {
	cat <<EOF

${C_YELLOW}NixOS does not install packages imperatively.${C_RESET} Use one of these:

  1. Enter the flake development shell (nothing is added to the system):

       cd "$REPO_ROOT"
       nix develop

  2. Add the block below to configuration.nix, then run:

       sudo nixos-rebuild switch

EOF
	print_nix_block 0
	cat <<EOF

Docker and Tailscale also need the system options shown at the bottom of the
block. API keys still belong in /etc/libreai/libreai.env, never in the Nix
store.
EOF
}

print_next_steps() {
	cat <<EOF

Next steps:
  cp .env.example .env && chmod 600 .env   # add a provider API key
  make test
  make build
  ./bin/libreai
EOF
}

confirm_install() {
	[ "$ASSUME_YES" -eq 1 ] && return 0
	if [ ! -t 0 ]; then
		err "refusing to modify the system without an interactive terminal; re-run with --yes"
		return 1
	fi
	printf 'Install the missing packages now? [Y/n] '
	local answer
	read -r answer
	case "$answer" in
	[nN]*) return 1 ;;
	*) return 0 ;;
	esac
}

main() {
	parse_args "$@"

	if [ "$PRINT_NIX" -eq 1 ]; then
		print_nix_block
		exit 0
	fi

	detect_distro
	detect_manager
	print_report
	log ""

	if [ "$CHECK_ONLY" -eq 1 ]; then
		if any_selected_missing; then
			err "some selected dependencies are missing or too old"
			exit 1
		fi
		log "${C_GREEN}All selected dependencies are available.${C_RESET}"
		exit 0
	fi

	if ! any_selected_missing; then
		log "${C_GREEN}All selected dependencies are already installed.${C_RESET}"
		print_next_steps
		exit 0
	fi

	if [ "$MANAGER" = nixos ]; then
		print_nixos_instructions
		exit 0
	fi

	if [ "$MANAGER" = none ]; then
		err "no supported package manager was found"
		print_manual_requirements
		print_nix_block >&2
		exit 1
	fi

	local core_missing=() recommended_missing=() optional_missing=()
	collect_missing core_missing "${CORE_DEPS[@]}"
	collect_missing recommended_missing "${RECOMMENDED_DEPS[@]}"
	if [ ${#SELECTED_OPTIONAL[@]} -gt 0 ]; then
		collect_missing optional_missing "${SELECTED_OPTIONAL[@]}"
	fi

	if [ "$DRY_RUN" -eq 1 ]; then
		log "Dry run; nothing will be changed."
		if [ ${#core_missing[@]} -gt 0 ]; then
			print_dry_run_group "build/test dependencies" "${core_missing[@]}"
		fi
		if [ ${#recommended_missing[@]} -gt 0 ]; then
			print_dry_run_group "recommended dependencies" "${recommended_missing[@]}"
		fi
		if [ ${#optional_missing[@]} -gt 0 ]; then
			print_dry_run_group "optional deployment dependencies" "${optional_missing[@]}"
		fi
		exit 0
	fi

	if ! confirm_install; then
		warn "aborted; missing dependencies were not installed"
		print_manual_requirements
		exit 1
	fi

	if [ ${#core_missing[@]} -gt 0 ]; then
		install_group "build/test dependencies" "${core_missing[@]}"
	fi
	if [ ${#recommended_missing[@]} -gt 0 ]; then
		install_group "recommended dependencies" "${recommended_missing[@]}"
	fi
	if [ ${#optional_missing[@]} -gt 0 ]; then
		install_group "optional deployment dependencies" "${optional_missing[@]}"
	fi

	log ""
	log "Rechecking..."
	print_report
	log ""

	collect_missing core_missing "${CORE_DEPS[@]}"
	collect_missing recommended_missing "${RECOMMENDED_DEPS[@]}"
	optional_missing=()
	if [ ${#SELECTED_OPTIONAL[@]} -gt 0 ]; then
		collect_missing optional_missing "${SELECTED_OPTIONAL[@]}"
	fi

	if [ ${#core_missing[@]} -gt 0 ]; then
		err "required dependencies are still missing or too old: ${core_missing[*]}"
		if has_dep core_missing go; then
			err "the Go package from $(manager_label) is missing or older than $MIN_GO; install it from https://go.dev/dl/ or use GOTOOLCHAIN=auto with Go 1.21+"
		fi
		print_manual_requirements
		exit 1
	fi

	if [ ${#recommended_missing[@]} -gt 0 ]; then
		warn "recommended dependencies were not installed: ${recommended_missing[*]}"
		warn "the application builds, but frontend tests or binary checks may be skipped until these are present"
	fi
	if [ ${#optional_missing[@]} -gt 0 ]; then
		warn "optional deployment dependencies were not installed: ${optional_missing[*]}"
	fi

	log "${C_GREEN}Required dependencies are available.${C_RESET}"
	print_next_steps
}

has_dep() {
	local target=$1 needle=$2
	local -n list=$target
	local item
	for item in "${list[@]}"; do
		[ "$item" = "$needle" ] && return 0
	done
	return 1
}

main "$@"
