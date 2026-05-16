#!/bin/bash
set -eu

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

LISTEN_PORT=443
CF_ENABLED=false
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="phantom-node"
ENV_FILE="/etc/phantom-node.env"
DOWNLOAD_URL="https://s.fjjxu.plus/phantom/phantom-node"

usage() {
    cat <<'EOF'
Usage:
  bash install.sh --node-id=5 --panel=https://panel.example.com --token=secret

Optional arguments:
  --port=443
  --download-url=https://example.com/phantom-node
  --cf-enabled
  --cf-token=xxx
  --cf-zone=xxx
  --cf-record=node.example.com
EOF
}

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}This installer must be run as root.${NC}"
    exit 1
fi

for arg in "$@"; do
    case "$arg" in
        --node-id=*) NODE_ID="${arg#*=}" ;;
        --panel=*) PANEL_HOST="${arg#*=}" ;;
        --token=*) PANEL_TOKEN="${arg#*=}" ;;
        --port=*) LISTEN_PORT="${arg#*=}" ;;
        --cf-enabled) CF_ENABLED=true ;;
        --cf-token=*) CF_API_TOKEN="${arg#*=}" ;;
        --cf-zone=*) CF_ZONE_ID="${arg#*=}" ;;
        --cf-record=*) CF_RECORD_NAME="${arg#*=}" ;;
        --download-url=*) DOWNLOAD_URL="${arg#*=}" ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown argument: $arg${NC}"
            usage
            exit 1
            ;;
    esac
done

if [ -z "${NODE_ID:-}" ] || [ -z "${PANEL_HOST:-}" ] || [ -z "${PANEL_TOKEN:-}" ]; then
    echo -e "${RED}--node-id, --panel, and --token are required.${NC}"
    usage
    exit 1
fi

if [ "$CF_ENABLED" = "true" ]; then
    if [ -z "${CF_API_TOKEN:-}" ] || [ -z "${CF_ZONE_ID:-}" ] || [ -z "${CF_RECORD_NAME:-}" ]; then
        echo -e "${RED}Cloudflare mode requires --cf-token, --cf-zone, and --cf-record.${NC}"
        exit 1
    fi
fi

if ! command -v curl >/dev/null 2>&1; then
    echo -e "${RED}curl is required but not installed.${NC}"
    exit 1
fi

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  phantom-node installer${NC}"
echo -e "${GREEN}========================================${NC}"
echo -e "  Node ID:      ${YELLOW}${NODE_ID}${NC}"
echo -e "  Panel:        ${YELLOW}${PANEL_HOST}${NC}"
echo -e "  Listen port:  ${YELLOW}${LISTEN_PORT}${NC}"
echo -e "  CF DNS:       ${YELLOW}${CF_ENABLED}${NC}"
echo ""

echo -e "${GREEN}[1/6] Downloading phantom-node...${NC}"
curl -fsSL -o "${INSTALL_DIR}/${SERVICE_NAME}" "${DOWNLOAD_URL}"
chmod +x "${INSTALL_DIR}/${SERVICE_NAME}"
echo -e "  OK: downloaded to ${INSTALL_DIR}/${SERVICE_NAME}"

echo -e "${GREEN}[2/6] Writing environment file...${NC}"
cat > "${ENV_FILE}" <<EOF
PANEL_HOST=${PANEL_HOST}
PANEL_TOKEN=${PANEL_TOKEN}
NODE_ID=${NODE_ID}
LISTEN_PORT=${LISTEN_PORT}
CF_ENABLED=${CF_ENABLED}
EOF

if [ "$CF_ENABLED" = "true" ]; then
    cat >> "${ENV_FILE}" <<EOF
CF_API_TOKEN=${CF_API_TOKEN}
CF_ZONE_ID=${CF_ZONE_ID}
CF_RECORD_NAME=${CF_RECORD_NAME}
EOF
fi

chmod 600 "${ENV_FILE}"
echo -e "  OK: wrote ${ENV_FILE}"

echo -e "${GREEN}[3/6] Creating systemd service...${NC}"
cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Phantom Gateway Node (ID: ${NODE_ID})
After=network.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${SERVICE_NAME}
EnvironmentFile=${ENV_FILE}
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
echo -e "  OK: created systemd unit"

echo -e "${GREEN}[4/6] Opening firewall port...${NC}"
if command -v ufw >/dev/null 2>&1; then
    ufw allow "${LISTEN_PORT}/tcp" >/dev/null 2>&1
    echo -e "  OK: allowed TCP ${LISTEN_PORT} with ufw"
elif command -v iptables >/dev/null 2>&1; then
    iptables -I INPUT -p tcp --dport "${LISTEN_PORT}" -j ACCEPT 2>/dev/null || true
    echo -e "  OK: allowed TCP ${LISTEN_PORT} with iptables"
else
    echo -e "  ${YELLOW}WARN: no firewall tool found, open TCP ${LISTEN_PORT} manually if needed.${NC}"
fi

echo -e "${GREEN}[5/6] Applying network tuning...${NC}"
if sysctl net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -q bbr; then
    sysctl -w net.core.default_qdisc=fq >/dev/null 2>&1
    sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1
    echo -e "  OK: enabled BBR"
else
    echo -e "  ${YELLOW}WARN: kernel does not advertise BBR support, skipping live enable.${NC}"
fi

SYSCTL_CONF="/etc/sysctl.d/99-phantom-node.conf"
cat > "${SYSCTL_CONF}" <<'EOF'
# phantom-node network tuning
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

sysctl -p "${SYSCTL_CONF}" >/dev/null 2>&1 || true
echo -e "  OK: wrote ${SYSCTL_CONF}"

LIMITS_CONF="/etc/security/limits.d/99-phantom-node.conf"
cat > "${LIMITS_CONF}" <<'EOF'
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF
echo -e "  OK: raised nofile limits"

echo -e "${GREEN}[6/6] Starting service...${NC}"
systemctl enable --now "${SERVICE_NAME}"
sleep 2

if systemctl is-active --quiet "${SERVICE_NAME}"; then
    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  phantom-node installation complete${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo -e "  Logs:    journalctl -u ${SERVICE_NAME} -f"
    echo -e "  Stop:    systemctl stop ${SERVICE_NAME}"
    echo -e "  Restart: systemctl restart ${SERVICE_NAME}"
    echo ""
    echo -e "  ${YELLOW}A reboot is recommended so all kernel tuning becomes persistent.${NC}"
else
    echo -e "${RED}Service failed to start. Inspect logs with:${NC}"
    echo -e "  journalctl -u ${SERVICE_NAME} --no-pager -n 20"
    exit 1
fi
