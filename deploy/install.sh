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
用法:
  bash install.sh --node-id=5 --panel=https://panel.example.com --token=secret

可选参数:
  --port=443
  --download-url=https://example.com/phantom-node
  --cf-enabled
  --cf-token=xxx
  --cf-zone=xxx
  --cf-record=node.example.com
EOF
}

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}安装脚本必须以 root 身份运行。${NC}"
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
            echo -e "${RED}未知参数: $arg${NC}"
            usage
            exit 1
            ;;
    esac
done

if [ -z "${NODE_ID:-}" ] || [ -z "${PANEL_HOST:-}" ] || [ -z "${PANEL_TOKEN:-}" ]; then
    echo -e "${RED}必须提供 --node-id、--panel 和 --token。${NC}"
    usage
    exit 1
fi

if [ "$CF_ENABLED" = "true" ]; then
    if [ -z "${CF_API_TOKEN:-}" ] || [ -z "${CF_ZONE_ID:-}" ] || [ -z "${CF_RECORD_NAME:-}" ]; then
        echo -e "${RED}启用 Cloudflare DNS 时必须提供 --cf-token、--cf-zone 和 --cf-record。${NC}"
        exit 1
    fi
fi

if ! command -v curl >/dev/null 2>&1; then
    echo -e "${RED}系统缺少 curl，无法继续安装。${NC}"
    exit 1
fi

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  phantom-node Xboard 节点安装程序${NC}"
echo -e "${GREEN}========================================${NC}"
echo -e "  节点 ID:      ${YELLOW}${NODE_ID}${NC}"
echo -e "  面板地址:     ${YELLOW}${PANEL_HOST}${NC}"
echo -e "  监听端口:     ${YELLOW}${LISTEN_PORT}${NC}"
echo -e "  Cloudflare:   ${YELLOW}${CF_ENABLED}${NC}"
echo ""

echo -e "${GREEN}[1/6] 下载 phantom-node 二进制...${NC}"
curl -fsSL -o "${INSTALL_DIR}/${SERVICE_NAME}" "${DOWNLOAD_URL}"
chmod +x "${INSTALL_DIR}/${SERVICE_NAME}"
echo -e "  完成: 已下载到 ${INSTALL_DIR}/${SERVICE_NAME}"

echo -e "${GREEN}[2/6] 写入环境变量文件...${NC}"
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
echo -e "  完成: 已写入 ${ENV_FILE}"

echo -e "${GREEN}[3/6] 创建 systemd 服务...${NC}"
cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Phantom Xboard Node (ID: ${NODE_ID})
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
echo -e "  完成: systemd 服务已创建"

echo -e "${GREEN}[4/6] 放行防火墙端口...${NC}"
if command -v ufw >/dev/null 2>&1; then
    ufw allow "${LISTEN_PORT}/tcp" >/dev/null 2>&1
    echo -e "  完成: 已通过 ufw 放行 TCP ${LISTEN_PORT}"
elif command -v iptables >/dev/null 2>&1; then
    iptables -I INPUT -p tcp --dport "${LISTEN_PORT}" -j ACCEPT 2>/dev/null || true
    echo -e "  完成: 已通过 iptables 放行 TCP ${LISTEN_PORT}"
else
    echo -e "  ${YELLOW}提示: 未检测到防火墙工具，如有需要请手动放行 TCP ${LISTEN_PORT}。${NC}"
fi

echo -e "${GREEN}[5/6] 应用网络优化参数...${NC}"
if sysctl net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -q bbr; then
    sysctl -w net.core.default_qdisc=fq >/dev/null 2>&1
    sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1
    echo -e "  完成: 已启用 BBR"
else
    echo -e "  ${YELLOW}提示: 当前内核未声明支持 BBR，跳过即时启用。${NC}"
fi

SYSCTL_CONF="/etc/sysctl.d/99-phantom-node.conf"
cat > "${SYSCTL_CONF}" <<'EOF'
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

sysctl -p "${SYSCTL_CONF}" >/dev/null 2>&1 || true
echo -e "  完成: 已写入 ${SYSCTL_CONF}"

LIMITS_CONF="/etc/security/limits.d/99-phantom-node.conf"
cat > "${LIMITS_CONF}" <<'EOF'
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF
echo -e "  完成: 已提升 nofile 限制"

echo -e "${GREEN}[6/6] 启动服务...${NC}"
systemctl enable --now "${SERVICE_NAME}"
sleep 2

if systemctl is-active --quiet "${SERVICE_NAME}"; then
    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  phantom-node 安装完成${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo -e "  查看日志: journalctl -u ${SERVICE_NAME} -f"
    echo -e "  停止服务: systemctl stop ${SERVICE_NAME}"
    echo -e "  重启服务: systemctl restart ${SERVICE_NAME}"
    echo ""
    echo -e "  ${YELLOW}建议重启一次系统，以确保所有内核优化参数持久生效。${NC}"
else
    echo -e "${RED}服务启动失败，请执行以下命令查看日志:${NC}"
    echo -e "  journalctl -u ${SERVICE_NAME} --no-pager -n 20"
    exit 1
fi
