#!/usr/bin/env bash
set -eu

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SERVICE_NAME="phantom-node"
INSTALL_DIR="/usr/local/bin"
ENV_FILE="/etc/phantom-node.env"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
BUILD_ROOT="/usr/local/src/${SERVICE_NAME}"
REPO_URL="https://github.com/miliyao/phantom-node.git"
RELEASE_REPO="miliyao/phantom-node"
REPO_REF="main"
RELEASE_VERSION="latest"
GO_VERSION="1.25.6"
GO_INSTALL_DIR="/usr/local/go"
GO_BINARY="/usr/local/go/bin/go"
BUILD_TAGS="with_utls"
LISTEN_PORT=443
DOWNLOAD_URL=""
INSTALL_FROM_SOURCE=false

usage() {
    cat <<'EOF'
Usage:
  bash install.sh --node-id=5 --panel=https://panel.example.com --token=secret

Optional:
  --port=443
  --version=latest|v0.1.0
  --ref=main
  --source
  --download-url=https://example.com/phantom-node
EOF
}

log_info() {
    echo -e "${GREEN}$1${NC}"
}

log_warn() {
    echo -e "${YELLOW}$1${NC}"
}

log_error() {
    echo -e "${RED}$1${NC}" >&2
}

require_root() {
    if [ "$(id -u)" -ne 0 ]; then
        log_error "This installer must run as root."
        exit 1
    fi
}

parse_args() {
    for arg in "$@"; do
        case "$arg" in
            --node-id=*) NODE_ID="${arg#*=}" ;;
            --panel=*) PANEL_HOST="${arg#*=}" ;;
            --token=*) PANEL_TOKEN="${arg#*=}" ;;
            --port=*) LISTEN_PORT="${arg#*=}" ;;
            --version=*) RELEASE_VERSION="${arg#*=}" ;;
            --ref=*) REPO_REF="${arg#*=}" ;;
            --source) INSTALL_FROM_SOURCE=true ;;
            --download-url=*) DOWNLOAD_URL="${arg#*=}" ;;
            --help|-h)
                usage
                exit 0
                ;;
            *)
                log_error "Unknown argument: $arg"
                usage
                exit 1
                ;;
        esac
    done
}

validate_args() {
    if [ -z "${NODE_ID:-}" ] || [ -z "${PANEL_HOST:-}" ] || [ -z "${PANEL_TOKEN:-}" ]; then
        log_error "Missing required arguments: --node-id, --panel, --token"
        usage
        exit 1
    fi
}

ensure_commands() {
    local missing=""
    for cmd in "$@"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing="$missing $cmd"
        fi
    done

    if [ -n "$missing" ]; then
        log_warn "Missing commands:$missing"
        install_packages $missing
    fi
}

install_packages() {
    if command -v apt-get >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -y
        apt-get install -y ca-certificates "$@"
        return
    fi

    if command -v dnf >/dev/null 2>&1; then
        dnf install -y ca-certificates "$@"
        return
    fi

    if command -v yum >/dev/null 2>&1; then
        yum install -y ca-certificates "$@"
        return
    fi

    if command -v apk >/dev/null 2>&1; then
        apk add --no-cache ca-certificates "$@"
        return
    fi

    log_error "Unable to install dependencies automatically."
    exit 1
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) GO_ARCH="amd64" ;;
        aarch64|arm64) GO_ARCH="arm64" ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac
}

ensure_go() {
    if command -v go >/dev/null 2>&1; then
        GO_CMD="$(command -v go)"
        return
    fi

    detect_arch
    install_go
    GO_CMD="$GO_BINARY"
}

install_go() {
    local go_tar="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    local go_url="https://go.dev/dl/${go_tar}"
    local tmp_tar="/tmp/${go_tar}"

    log_info "Installing Go ${GO_VERSION}..."
    curl -fsSL -o "$tmp_tar" "$go_url"
    rm -rf "$GO_INSTALL_DIR"
    tar -C /usr/local -xzf "$tmp_tar"
    rm -f "$tmp_tar"

    if ! "$GO_BINARY" version >/dev/null 2>&1; then
        log_error "Go installation failed."
        exit 1
    fi
}

prepare_source() {
    rm -rf "$BUILD_ROOT"
    mkdir -p "$BUILD_ROOT"
    log_info "Cloning repository..."
    git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$BUILD_ROOT"
}

build_binary_from_source() {
    log_info "Building binary from source..."
    local tmp_bin="${INSTALL_DIR}/${SERVICE_NAME}.new"
    (
        cd "$BUILD_ROOT"
        PATH="$(dirname "$GO_CMD"):$PATH" "$GO_CMD" build -tags "$BUILD_TAGS" -o "$tmp_bin" .
    )
    chmod +x "$tmp_bin"
    mv "$tmp_bin" "${INSTALL_DIR}/${SERVICE_NAME}"
}

download_binary() {
    local url="$1"
    local label="$2"
    local tmp_bin="${INSTALL_DIR}/${SERVICE_NAME}.new"

    log_info "Downloading ${label}..."
    curl -fL --retry 3 --connect-timeout 15 -o "$tmp_bin" "$url"
    chmod +x "$tmp_bin"
    mv "$tmp_bin" "${INSTALL_DIR}/${SERVICE_NAME}"
}

release_asset_url() {
    local asset_name="${SERVICE_NAME}-linux-${GO_ARCH}"
    if [ "$RELEASE_VERSION" = "latest" ]; then
        echo "https://github.com/${RELEASE_REPO}/releases/latest/download/${asset_name}"
        return
    fi
    echo "https://github.com/${RELEASE_REPO}/releases/download/${RELEASE_VERSION}/${asset_name}"
}

try_download_release_binary() {
    detect_arch

    local url
    url="$(release_asset_url)"

    if download_binary "$url" "GitHub Release binary (${GO_ARCH}, ${RELEASE_VERSION})"; then
        return 0
    fi

    rm -f "${INSTALL_DIR}/${SERVICE_NAME}.new"
    return 1
}

build_from_source() {
    ensure_commands git tar
    ensure_go
    prepare_source
    build_binary_from_source
}

install_binary() {
    if [ -n "$DOWNLOAD_URL" ]; then
        download_binary "$DOWNLOAD_URL" "custom binary"
        return
    fi

    if [ "$INSTALL_FROM_SOURCE" = "true" ]; then
        log_info "Installing from source..."
        build_from_source
        return
    fi

    if try_download_release_binary; then
        return
    fi

    log_warn "Release download failed, falling back to source build."
    build_from_source
}

write_env_file() {
    log_info "Writing environment file..."
    cat > "$ENV_FILE" <<EOF
PANEL_HOST=${PANEL_HOST}
PANEL_TOKEN=${PANEL_TOKEN}
NODE_ID=${NODE_ID}
LISTEN_PORT=${LISTEN_PORT}
EOF
    chmod 600 "$ENV_FILE"
}

write_service_file() {
    log_info "Writing systemd service..."
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Phantom Xboard Node (ID: ${NODE_ID})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${SERVICE_NAME}
EnvironmentFile=${ENV_FILE}
Restart=always
RestartSec=5
LimitNOFILE=65535
WorkingDirectory=${BUILD_ROOT}

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
}

configure_firewall() {
    log_info "Opening TCP port..."
    if command -v ufw >/dev/null 2>&1; then
        ufw allow "${LISTEN_PORT}/tcp" >/dev/null 2>&1 || true
        return
    fi

    if command -v firewall-cmd >/dev/null 2>&1; then
        firewall-cmd --permanent --add-port="${LISTEN_PORT}/tcp" >/dev/null 2>&1 || true
        firewall-cmd --reload >/dev/null 2>&1 || true
        return
    fi

    if command -v iptables >/dev/null 2>&1; then
        iptables -I INPUT -p tcp --dport "${LISTEN_PORT}" -j ACCEPT 2>/dev/null || true
        return
    fi

    log_warn "No firewall tool detected; open TCP ${LISTEN_PORT} manually if needed."
}

enable_bbr() {
    log_info "Applying BBR tuning..."

    if ! sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -q bbr; then
        log_warn "BBR support not detected, skipping."
        return
    fi

    cat > /etc/sysctl.d/99-phantom-node.conf <<'EOF'
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF

    sysctl -p /etc/sysctl.d/99-phantom-node.conf >/dev/null 2>&1 || true
}

start_service() {
    log_info "Starting service..."
    systemctl enable --now "$SERVICE_NAME"

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_info "Installation complete."
        return
    fi

    log_error "Service failed to start."
    exit 1
}

print_summary() {
    echo -e "${GREEN}phantom-node installer${NC}"
    echo -e "  node id: ${YELLOW}${NODE_ID}${NC}"
    echo -e "  panel:   ${YELLOW}${PANEL_HOST}${NC}"
    echo -e "  listen:  ${YELLOW}${LISTEN_PORT}${NC}"
}

main() {
    require_root
    parse_args "$@"
    validate_args
    ensure_commands curl systemctl git tar
    print_summary

    mkdir -p "$INSTALL_DIR" "$BUILD_ROOT"
    install_binary
    write_env_file
    write_service_file
    configure_firewall
    enable_bbr
    start_service
}

main "$@"
