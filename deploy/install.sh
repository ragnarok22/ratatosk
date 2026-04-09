#!/usr/bin/env bash
set -e

# ---------------------------------------------------------------------------
# Ratatosk Relay Server — Automated Installer
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/ragnarok22/ratatosk/main/deploy/install.sh | sudo bash
#
# This script installs the latest Ratatosk relay server binary, creates the
# systemd service, and scaffolds a starter configuration file.
# ---------------------------------------------------------------------------

REPO="ragnarok22/ratatosk"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/ratatosk"
LOG_DIR="/var/log/ratatosk"
DATA_DIR="/var/lib/ratatosk"
SERVICE_FILE="/etc/systemd/system/ratatosk.service"
BINARY_NAME="ratatosk-server"

# -- Colors ----------------------------------------------------------------

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

info()    { printf "${BLUE}-> %s${RESET}\n" "$*"; }
success() { printf "${GREEN}-> %s${RESET}\n" "$*"; }
warn()    { printf "${YELLOW}[WARN] %s${RESET}\n" "$*"; }
error()   { printf "${RED}[ERROR] %s${RESET}\n" "$*" >&2; }

# -- Root check ------------------------------------------------------------

if [[ $EUID -ne 0 ]]; then
    error "This script must be run as root (use sudo)"
    exit 1
fi

# -- OS detection ----------------------------------------------------------

OS=$(uname -s)
if [[ "$OS" != "Linux" ]]; then
    error "This installer only supports Linux (detected: ${OS})"
    error "Ratatosk relay server requires systemd on a Linux VPS."
    exit 1
fi

# -- Architecture detection ------------------------------------------------

UNAME_ARCH=$(uname -m)
case "$UNAME_ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *)
        error "Unsupported architecture: ${UNAME_ARCH}"
        error "Ratatosk server binaries are available for x86_64 (amd64)."
        exit 1
        ;;
esac

if [[ "$ARCH" == "arm64" ]]; then
    error "The relay server binary is not yet available for arm64."
    error "Server builds currently target linux/amd64 only."
    exit 1
fi

# -- Dependency check ------------------------------------------------------

if ! command -v curl &>/dev/null; then
    error "'curl' is required but not installed."
    error "Install it with: apt install curl  (Debian/Ubuntu)"
    error "                  yum install curl  (RHEL/CentOS)"
    exit 1
fi

HAS_SETCAP=true
if ! command -v setcap &>/dev/null; then
    HAS_SETCAP=false
    warn "'setcap' not found (libcap2-bin). The systemd service handles"
    warn "privileged ports via AmbientCapabilities, so this is optional."
    warn "Install it if you need to run the binary outside systemd:"
    warn "  apt install libcap2-bin  (Debian/Ubuntu)"
    warn "  yum install libcap      (RHEL/CentOS)"
fi

# -- Fetch latest release version -----------------------------------------

info "Fetching latest release version..."
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": *"//;s/".*//')

if [[ -z "$VERSION" ]]; then
    error "Could not determine the latest release version."
    error "Check your internet connection or try again later."
    exit 1
fi

info "Latest release: ${VERSION}"

# -- Download binary -------------------------------------------------------

DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_NAME}-linux-${ARCH}"

info "Downloading ${BINARY_NAME} ${VERSION}..."
curl -fsSL -o "/tmp/${BINARY_NAME}" "$DOWNLOAD_URL"

mv "/tmp/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
success "Installed binary to ${INSTALL_DIR}/${BINARY_NAME}"

# -- Set capabilities (optional) ------------------------------------------

if [[ "$HAS_SETCAP" == true ]]; then
    setcap 'cap_net_bind_service=+ep' "${INSTALL_DIR}/${BINARY_NAME}"
    success "Granted CAP_NET_BIND_SERVICE to ${BINARY_NAME}"
fi

# -- Create system user (idempotent) --------------------------------------

if ! id -u ratatosk &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin ratatosk
    success "Created system user 'ratatosk'"
else
    info "System user 'ratatosk' already exists"
fi

# -- Create directories (idempotent) --------------------------------------

mkdir -p "${CONFIG_DIR}" "${LOG_DIR}" "${DATA_DIR}"
chown ratatosk:ratatosk "${CONFIG_DIR}" "${LOG_DIR}" "${DATA_DIR}"
success "Directories ready: ${CONFIG_DIR}, ${LOG_DIR}, ${DATA_DIR}"

# -- Install systemd service file -----------------------------------------

info "Installing systemd service..."
curl -fsSL -o "${SERVICE_FILE}" \
    "https://raw.githubusercontent.com/${REPO}/main/deploy/ratatosk.service"
systemctl daemon-reload
success "Systemd service installed and daemon reloaded"

# -- Done ------------------------------------------------------------------

printf "\n"
printf "${GREEN}${BOLD}=======================================${RESET}\n"
printf "${GREEN}${BOLD}  Ratatosk installed successfully!${RESET}\n"
printf "${GREEN}${BOLD}=======================================${RESET}\n"
printf "\n"
printf "  Binary:   ${INSTALL_DIR}/${BINARY_NAME}\n"
printf "  Service:  ${SERVICE_FILE}\n"
printf "  Logs:     journalctl -u ratatosk -f\n"
printf "\n"
printf "${YELLOW}${BOLD}  Run the interactive setup wizard to configure your server:${RESET}\n"
printf "\n"
printf "       ${BOLD}sudo ratatosk-server init${RESET}\n"
printf "\n"
printf "${BOLD}Next steps:${RESET}\n"
printf "  1. Configure DNS records (see deployment guide)\n"
printf "  2. Run the setup wizard:  ${BOLD}sudo ratatosk-server init${RESET}\n"
printf "  3. Open firewall ports if needed: 443/tcp, 7000/tcp, 8081/tcp\n"
printf "  4. Start the server:\n"
printf "       ${BOLD}sudo systemctl enable --now ratatosk${RESET}\n"
printf "  5. Verify it's running:\n"
printf "       sudo systemctl status ratatosk\n"
printf "\n"
