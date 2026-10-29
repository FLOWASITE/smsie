#!/bin/bash
# Cài smsie lên Mac chạy 24/7 + Cloudflare Tunnel smsie.jione.vn. Chạy: sudo bash cai-dat.sh [đường-dẫn-smsie.db-cũ]
set -euo pipefail
cd "$(dirname "$0")"
ARCH=$(uname -m); [ "$ARCH" = x86_64 ] && ARCH=amd64
mkdir -p /opt/smsie/logs /opt/smsie/backups
install -m 755 "smsie-$ARCH" /opt/smsie/smsie
[ -f /opt/smsie/config.yaml ] || install -m 600 config.yaml /opt/smsie/config.yaml
[ -n "${1:-}" ] && [ ! -f /opt/smsie/smsie.db ] && cp "$1" /opt/smsie/smsie.db
cp -R web /opt/smsie/ 2>/dev/null || true
install -m 644 vn.jione.smsie.plist /Library/LaunchDaemons/vn.jione.smsie.plist
launchctl bootout system/vn.jione.smsie 2>/dev/null || true
launchctl bootstrap system /Library/LaunchDaemons/vn.jione.smsie.plist
# Mac không được ngủ, mất điện có điện lại tự bật
pmset -a sleep 0 disksleep 0 autorestart 1 womp 1
sleep 3; curl -s -o /dev/null -w "smsie local: HTTP %{http_code}\n" http://127.0.0.1:18080/ || true
echo "Xong smsie. Bước tunnel (một lần, cần đăng nhập Cloudflare của jione.vn):"
echo "  brew install cloudflared && cloudflared tunnel login"
echo "  cloudflared tunnel create smsie && cloudflared tunnel route dns smsie smsie.jione.vn"
echo "  sudo cloudflared service install  # sau khi tạo ~/.cloudflared/config.yml như README"
