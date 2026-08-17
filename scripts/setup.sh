#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════
#  Vertex AI Proxy — 一键交互式部署脚本
#  兼容：Linux / macOS / Android (Termux) / WSL
#  用法：chmod +x setup.sh && ./setup.sh
# ══════════════════════════════════════════════════════════════
set -e

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
BOLD='\033[1m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY=""
for f in "$SCRIPT_DIR/vertex-proxy" "$SCRIPT_DIR/vertex-proxy.exe"; do
  [ -f "$f" ] && BINARY="$f" && break
done

# ---- 检测平台 ----
detect_platform() {
  PLATFORM="linux"
  IS_TERMUX=false
  IS_MACOS=false
  if [ -d "/data/data/com.termux" ]; then
    PLATFORM="android"; IS_TERMUX=true
  elif [ "$(uname)" = "Darwin" ]; then
    PLATFORM="macos"; IS_MACOS=true
  fi
}

# ---- Termux SSL 证书修复 ----
fix_termux_ssl() {
  if $IS_TERMUX; then
    if [ ! -f "$PREFIX/etc/tls/cert.pem" ]; then
      echo -e "${YELLOW}[!] 正在安装 CA 证书（Termux 需要）...${NC}"
      pkg install -y ca-certificates 2>/dev/null || true
    fi
    export SSL_CERT_FILE="$PREFIX/etc/tls/cert.pem"
    [ ! -f "$SSL_CERT_FILE" ] && export SSL_CERT_FILE="$PREFIX/etc/ssl/certs/ca-certificates.crt"
    echo -e "${GREEN}[✓] SSL 证书已配置${NC}"
  fi
}

# ---- 打印横幅 ----
print_banner() {
  echo ""
  echo -e "${CYAN}╔══════════════════════════════════════════════════╗${NC}"
  echo -e "${CYAN}║   Vertex AI Proxy — 交互式部署向导              ║${NC}"
  echo -e "${CYAN}╚══════════════════════════════════════════════════╝${NC}"
  echo ""
}

# ---- 询问函数 ----
ask() {
  local prompt="$1" default="$2"
  local answer
  printf "${BOLD}%s${NC} [%s]: " "$prompt" "$default"
  read -r answer
  echo "${answer:-$default}"
}

ask_yn() {
  local prompt="$1" default="$2"
  local answer
  printf "${BOLD}%s${NC} [%s]: " "$prompt" "$default"
  read -r answer
  answer="${answer:-$default}"
  [[ "$answer" =~ ^[Yy] ]]
}

# ---- 生成随机密钥 ----
gen_key() {
  if command -v openssl >/dev/null 2>&1; then
    echo "sk-$(openssl rand -hex 16)"
  else
    echo "sk-$(head -c 16 /dev/urandom 2>/dev/null | od -An -tx1 | tr -d ' \n' || echo "sk-$(date +%s)random")"
  fi
}

# ---- 生成随机管理员密码 ----
gen_admin_pass() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 12 | tr -d '/+=' | head -c 16
  else
    head -c 12 /dev/urandom 2>/dev/null | od -An -tx1 | tr -d ' \n' | head -c 16
  fi
}

# ══════════════════════════════════════════════════════════════
#  主流程
# ══════════════════════════════════════════════════════════════

detect_platform
fix_termux_ssl
print_banner

echo -e "${GREEN}检测到平台：${BOLD}$PLATFORM${NC}"
echo ""

# ---- 检查二进制 ----
if [ -z "$BINARY" ]; then
  echo -e "${RED}[✗] 未找到 vertex-proxy 二进制文件${NC}"
  echo "    请确保 vertex-proxy（或 vertex-proxy.exe）与本脚本在同一目录。"
  exit 1
fi
chmod +x "$BINARY" 2>/dev/null || true
echo -e "${GREEN}[✓] 找到主程序：$BINARY${NC}"
echo ""

# ---- 交互式配置 ----
echo -e "${CYAN}── 基本配置 ──${NC}"
PORT=$(ask "监听端口" "2156")

# API Key
DEFAULT_KEY=$(gen_key)
API_KEY=$(ask "API 密钥（sk- 开头，留空自动生成）" "$DEFAULT_KEY")
[ -z "$API_KEY" ] && API_KEY="$DEFAULT_KEY"
# 确保 sk- 前缀
[[ "$API_KEY" != sk-* ]] && API_KEY="sk-$API_KEY"

# 管理员密码
ADMIN_PASS=$(gen_admin_pass)
echo -e "  管理员密码将自动生成：${BOLD}$ADMIN_PASS${NC}"

echo ""
echo -e "${CYAN}── 网络配置 ──${NC}"
echo "  如果你的网络能直接访问 Google，选 n。"
echo "  如果在国内需要代理才能访问 Google，选 y。"
USE_PROXY=false
PROXY_URL=""
if ask_yn "是否需要配置代理？(y/n)" "n"; then
  USE_PROXY=true
  PROXY_URL=$(ask "代理地址（如 socks5://127.0.0.1:1080 或 http://127.0.0.1:7890）" "")
fi

echo ""
echo -e "${CYAN}── 高级选项 ──${NC}"
MAX_RETRIES=$(ask "请求失败重试次数" "2")

AUTO_START=false
if ask_yn "是否设置开机自启？(y/n)" "n"; then
  AUTO_START=true
fi

# ---- 创建配置 ----
echo ""
echo -e "${CYAN}── 正在创建配置 ──${NC}"

CONFIG_DIR="$SCRIPT_DIR/config"
mkdir -p "$CONFIG_DIR"

# config.json
cat > "$CONFIG_DIR/config.json" << EOF
{
  "port_api": $PORT,
  "max_retries": $MAX_RETRIES,
  "admin_password": "$ADMIN_PASS",
  "proxy_url": "$PROXY_URL"
}
EOF
echo -e "${GREEN}[✓] config/config.json${NC}"

# api_keys.txt
cat > "$CONFIG_DIR/api_keys.txt" << EOF
# 格式: 名称:密钥:备注
mykey:$API_KEY:部署脚本自动生成
EOF
echo -e "${GREEN}[✓] config/api_keys.txt${NC}"

# models.json（如果不存在）
if [ ! -f "$CONFIG_DIR/models.json" ]; then
  if [ -f "$SCRIPT_DIR/config/models.json" ]; then
    cp "$SCRIPT_DIR/config/models.json" "$CONFIG_DIR/models.json"
  else
    cp "$SCRIPT_DIR/../config/models.json" "$CONFIG_DIR/models.json" 2>/dev/null || true
  fi
  echo -e "${GREEN}[✓] config/models.json${NC}"
else
  echo -e "${YELLOW}[=] config/models.json 已存在，跳过${NC}"
fi

# ---- 同意使用规则 ----
echo ""
echo -e "${CYAN}── 使用规则 ──${NC}"
echo "  ⚠️  重要声明：本软件完全免费（PolyForm Noncommercial License 1.0.0）。"
echo "  仅供个人非商业学习与研究使用，严禁转售、收费代搭或用于商业营利。"
if [ -f "$SCRIPT_DIR/rules.txt" ]; then
  echo "  完整规则文件：$SCRIPT_DIR/rules.txt"
fi
mkdir -p "$CONFIG_DIR/state"
if [ ! -f "$CONFIG_DIR/state/.rules_agreed" ]; then
  if ask_yn "是否已仔细阅读并同意上述规则？(y/n)" "y"; then
    # 写入同意标记（含哈希 36800adeec862126），确保作为守护进程或开机自启时能正常启动
    echo -e "$(date -u +"%Y-%m-%dT%H:%M:%SZ")\t36800adeec862126" > "$CONFIG_DIR/state/.rules_agreed"
    echo -e "${GREEN}[✓] 已记录规则同意凭证${NC}"
  else
    echo -e "${RED}[✗] 未同意规则，部署已终止${NC}"
    exit 1
  fi
fi

# ---- 设置开机自启 ----
if $AUTO_START; then
  echo ""
  echo -e "${CYAN}── 设置开机自启 ──${NC}"

  if $IS_TERMUX; then
    # Termux: termux-boot
    echo -e "${YELLOW}Termux 开机自启需要安装 Termux:Boot 应用（F-Droid 有）。${NC}"
    mkdir -p ~/.termux/boot
    cat > ~/.termux/boot/start-vertex.sh << BOOT
#!/data/data/com.termux/files/usr/bin/bash
termux-wake-lock
cd "$SCRIPT_DIR"
export SSL_CERT_FILE="$SSL_CERT_FILE"
ulimit -n 65536
nohup ./vertex-proxy > vertex.log 2>&1 &
BOOT
    chmod +x ~/.termux/boot/start-vertex.sh
    echo -e "${GREEN}[✓] 已创建 Termux:Boot 启动脚本${NC}"
    echo -e "    请安装 Termux:Boot（F-Droid）并授予权限。"

  elif $IS_MACOS; then
    # macOS: launchd
    PLIST="$HOME/Library/LaunchAgents/com.vertex-proxy.plist"
    cat > "$PLIST" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.vertex-proxy</string>
  <key>ProgramArguments</key>
  <array><string>$BINARY</string></array>
  <key>WorkingDirectory</key><string>$SCRIPT_DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$SCRIPT_DIR/vertex.log</string>
  <key>StandardErrorPath</key><string>$SCRIPT_DIR/vertex.log</string>
</dict>
</plist>
PLIST
    launchctl load "$PLIST" 2>/dev/null || true
    echo -e "${GREEN}[✓] 已创建 launchd 服务${NC}"

  else
    # Linux: systemd
    SERVICE="/etc/systemd/system/vertex-proxy.service"
    if [ -w "/etc/systemd/system/" ] || [ "$(id -u)" = "0" ]; then
      cat > "$SERVICE" << SVC
[Unit]
Description=Vertex AI Proxy
After=network.target

[Service]
Type=simple
WorkingDirectory=$SCRIPT_DIR
ExecStart=$BINARY
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SVC
      systemctl daemon-reload 2>/dev/null || true
      systemctl enable vertex-proxy 2>/dev/null || true
      echo -e "${GREEN}[✓] 已创建 systemd 服务${NC}"
    else
      echo -e "${YELLOW}[!] 需要 root 权限创建 systemd 服务，请手动执行：${NC}"
      echo "    sudo cp vertex-proxy.service /etc/systemd/system/"
      echo "    sudo systemctl enable --now vertex-proxy"
    fi
  fi
fi

# ---- 启动服务 ----
echo ""
echo -e "${CYAN}══════════════════════════════════════════════════${NC}"
echo -e "${GREEN}${BOLD}  ✓ 部署完成！${NC}"
echo ""
echo -e "  ${BOLD}API 地址${NC}：http://127.0.0.1:${PORT}/v1"
echo -e "  ${BOLD}API 密钥${NC}：${API_KEY}"
echo -e "  ${BOLD}管理面板${NC}：http://127.0.0.1:${PORT}/admin/"
echo -e "  ${BOLD}管理密码${NC}：${ADMIN_PASS}"
echo ""
echo -e "  在客户端（Cherry Studio / SillyTavern 等）中："
echo -e "    API Key：${API_KEY}"
echo -e "    Base URL：http://你的IP:${PORT}/v1"
echo ""
echo -e "${CYAN}══════════════════════════════════════════════════${NC}"
echo ""

if ask_yn "现在启动服务？(y/n)" "y"; then
  echo ""
  echo -e "${GREEN}正在启动...${NC}"
  cd "$SCRIPT_DIR"
  exec "$BINARY"
else
  echo ""
  echo -e "手动启动：cd $SCRIPT_DIR && ./$BINARY"
fi
