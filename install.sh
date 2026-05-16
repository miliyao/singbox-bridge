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
REPO_REF="main"
GO_VERSION="1.25.6"
GO_INSTALL_DIR="/usr/local/go"
GO_BINARY="/usr/local/go/bin/go"
BUILD_TAGS="with_utls"
LISTEN_PORT=443
CF_ENABLED=false
DOWNLOAD_URL=""

usage() {
    cat <<'EOF'
用法:
  bash install.sh --node-id=5 --panel=https://panel.example.com --token=secret

可选参数:
  --port=443
  --ref=main
  --download-url=https://example.com/phantom-node
  --cf-enabled
  --cf-token=xxx
  --cf-zone=xxx
  --cf-record=node.example.com

示例:
  bash <(curl -fsSL https://raw.githubusercontent.com/miliyao/phantom-node/main/install.sh) \
    --node-id=5 \
    --panel=https://panel.example.com \
    --token=secret
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
        log_error "安装脚本必须以 root 身份运行。"
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
            --ref=*) REPO_REF="${arg#*=}" ;;
            --download-url=*) DOWNLOAD_URL="${arg#*=}" ;;
            --cf-enabled) CF_ENABLED=true ;;
            --cf-token=*) CF_API_TOKEN="${arg#*=}" ;;
            --cf-zone=*) CF_ZONE_ID="${arg#*=}" ;;
            --cf-record=*) CF_RECORD_NAME="${arg#*=}" ;;
            --help|-h)
                usage
                exit 0
                ;;
            *)
                log_error "未知参数: $arg"
                usage
                exit 1
                ;;
        esac
    done
}

validate_args() {
    if [ -z "${NODE_ID:-}" ] || [ -z "${PANEL_HOST:-}" ] || [ -z "${PANEL_TOKEN:-}" ]; then
        log_error "必须提供 --node-id、--panel 和 --token。"
        usage
        exit 1
    fi

    if [ "$CF_ENABLED" = "true" ]; then
        if [ -z "${CF_API_TOKEN:-}" ] || [ -z "${CF_ZONE_ID:-}" ] || [ -z "${CF_RECORD_NAME:-}" ]; then
            log_error "启用 Cloudflare DNS 时必须提供 --cf-token、--cf-zone 和 --cf-record。"
            exit 1
        fi
    fi
}

ensure_base_dependencies() {
    local missing=""
    for cmd in curl git tar; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing="$missing $cmd"
        fi
    done

    if ! command -v systemctl >/dev/null 2>&1; then
        log_error "当前系统未检测到 systemd/systemctl，脚本暂不支持非 systemd 环境。"
        exit 1
    fi

    if [ -n "$missing" ]; then
        log_warn "检测到缺少依赖:$missing"
        install_packages $missing
    fi
}

install_packages() {
    if command -v apt-get >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -y
        apt-get install -y ca-certificates curl git tar "$@"
        return
    fi

    if command -v dnf >/dev/null 2>&1; then
        dnf install -y ca-certificates curl git tar "$@"
        return
    fi

    if command -v yum >/dev/null 2>&1; then
        yum install -y ca-certificates curl git tar "$@"
        return
    fi

    if command -v apk >/dev/null 2>&1; then
        apk add --no-cache ca-certificates curl git tar "$@"
        return
    fi

    log_error "无法自动安装依赖，请先手动安装:$*"
    exit 1
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)
            GO_ARCH="amd64"
            ;;
        aarch64|arm64)
            GO_ARCH="arm64"
            ;;
        *)
            log_error "当前架构暂不支持自动安装 Go: $(uname -m)"
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

    log_info "[准备] 未检测到 Go，开始安装 Go ${GO_VERSION}..."
    curl -fsSL -o "$tmp_tar" "$go_url"
    rm -rf "$GO_INSTALL_DIR"
    tar -C /usr/local -xzf "$tmp_tar"
    rm -f "$tmp_tar"

    if ! grep -q '/usr/local/go/bin' /etc/profile 2>/dev/null; then
        cat >> /etc/profile <<'EOF'
export PATH=/usr/local/go/bin:$PATH
EOF
    fi

    if ! "$GO_BINARY" version >/dev/null 2>&1; then
        log_error "Go 安装失败。"
        exit 1
    fi
}

prepare_source() {
    rm -rf "$BUILD_ROOT"
    mkdir -p "$BUILD_ROOT"

    log_info "[1/7] 拉取 GitHub 仓库源码..."
    git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$BUILD_ROOT"
}

build_binary_from_source() {
    log_info "[2/7] 编译 phantom-node（构建标签: ${BUILD_TAGS}）..."
    (
        cd "$BUILD_ROOT"
        PATH="$(dirname "$GO_CMD"):$PATH" "$GO_CMD" build -tags "$BUILD_TAGS" -o "${INSTALL_DIR}/${SERVICE_NAME}" .
    )
    chmod +x "${INSTALL_DIR}/${SERVICE_NAME}"
}

download_binary() {
    log_info "[1/7] 下载 phantom-node 二进制..."
    curl -fsSL -o "${INSTALL_DIR}/${SERVICE_NAME}" "$DOWNLOAD_URL"
    chmod +x "${INSTALL_DIR}/${SERVICE_NAME}"
}

write_env_file() {
    log_info "[3/7] 写入环境变量文件..."
    cat > "$ENV_FILE" <<EOF
PANEL_HOST=${PANEL_HOST}
PANEL_TOKEN=${PANEL_TOKEN}
NODE_ID=${NODE_ID}
LISTEN_PORT=${LISTEN_PORT}
CF_ENABLED=${CF_ENABLED}
EOF

    if [ "$CF_ENABLED" = "true" ]; then
        cat >> "$ENV_FILE" <<EOF
CF_API_TOKEN=${CF_API_TOKEN}
CF_ZONE_ID=${CF_ZONE_ID}
CF_RECORD_NAME=${CF_RECORD_NAME}
EOF
    fi

    chmod 600 "$ENV_FILE"
}

write_service_file() {
    log_info "[4/7] 创建 systemd 服务..."
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
    log_info "[5/7] 放行防火墙端口..."
    if command -v ufw >/dev/null 2>&1; then
        ufw allow "${LISTEN_PORT}/tcp" >/dev/null 2>&1 || true
        echo -e "  已通过 ufw 放行 TCP ${LISTEN_PORT}"
        return
    fi

    if command -v firewall-cmd >/dev/null 2>&1; then
        firewall-cmd --permanent --add-port="${LISTEN_PORT}/tcp" >/dev/null 2>&1 || true
        firewall-cmd --reload >/dev/null 2>&1 || true
        echo -e "  已通过 firewalld 放行 TCP ${LISTEN_PORT}"
        return
    fi

    if command -v iptables >/dev/null 2>&1; then
        iptables -I INPUT -p tcp --dport "${LISTEN_PORT}" -j ACCEPT 2>/dev/null || true
        echo -e "  已通过 iptables 放行 TCP ${LISTEN_PORT}"
        return
    fi

    log_warn "未检测到防火墙工具，如有需要请手动放行 TCP ${LISTEN_PORT}。"
}

apply_sysctl_tuning() {
    local sysctl_conf="/etc/sysctl.d/99-phantom-node.conf"
    local limits_conf="/etc/security/limits.d/99-phantom-node.conf"

    log_info "[6/7] 应用网络优化参数..."
    if sysctl net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -q bbr; then
        sysctl -w net.core.default_qdisc=fq >/dev/null 2>&1 || true
        sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || true
        echo -e "  已尝试启用 BBR"
    else
        log_warn "当前内核未声明支持 BBR，跳过即时启用。"
    fi

    cat > "$sysctl_conf" <<'EOF'
# phantom-node 网络优化
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_max_orphans = 65535
net.ipv4.tcp_max_syn_backlog = 8192
net.core.somaxconn = 32768
fs.file-max = 1048576
net.netfilter.nf_conntrack_max = 1048576
net.nf_conntrack_max = 1048576
EOF

    sysctl -p "$sysctl_conf" >/dev/null 2>&1 || true

    cat > "$limits_conf" <<'EOF'
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF
}

start_service() {
    log_info "[7/7] 启动服务..."
    systemctl enable --now "$SERVICE_NAME"
    sleep 2

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        echo ""
        echo -e "${GREEN}========================================${NC}"
        echo -e "${GREEN}  phantom-node 已安装完成${NC}"
        echo -e "${GREEN}========================================${NC}"
        echo -e "  GitHub 仓库: ${YELLOW}${REPO_URL}${NC}"
        echo -e "  当前分支:    ${YELLOW}${REPO_REF}${NC}"
        echo -e "  查看日志:    ${YELLOW}journalctl -u ${SERVICE_NAME} -f${NC}"
        echo -e "  停止服务:    ${YELLOW}systemctl stop ${SERVICE_NAME}${NC}"
        echo -e "  重启服务:    ${YELLOW}systemctl restart ${SERVICE_NAME}${NC}"
        echo ""
        log_warn "建议重启一次系统，以确保所有内核优化参数持久生效。"
        return
    fi

    log_error "服务启动失败，请执行以下命令查看日志:"
    echo -e "  journalctl -u ${SERVICE_NAME} --no-pager -n 50"
    exit 1
}

print_summary() {
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  phantom-node Xboard 一键部署${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo -e "  节点 ID:      ${YELLOW}${NODE_ID}${NC}"
    echo -e "  面板地址:     ${YELLOW}${PANEL_HOST}${NC}"
    echo -e "  监听端口:     ${YELLOW}${LISTEN_PORT}${NC}"
    echo -e "  Cloudflare:   ${YELLOW}${CF_ENABLED}${NC}"
    echo ""
}

main() {
    require_root
    parse_args "$@"
    validate_args
    ensure_base_dependencies
    print_summary

    mkdir -p "$INSTALL_DIR" "$BUILD_ROOT"

    if [ -n "$DOWNLOAD_URL" ]; then
        download_binary
    else
        ensure_go
        prepare_source
        build_binary_from_source
    fi

    write_env_file
    write_service_file
    configure_firewall
    apply_sysctl_tuning
    start_service
}

main "$@"
