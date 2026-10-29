#!/bin/bash
# Chạy tunnel Cloudflare (token + ingress trong tunnel.yml → 127.0.0.1:18080) thành dịch vụ riêng, không đụng ~/.cloudflared/config.yml.
# Lấy token: cloudflared tunnel token <tên-tunnel> > ~/.cloudflared/<tên>.token. Chạy: sudo bash cai-tunnel.sh <file-token>
set -euo pipefail
cd "$(dirname "$0")"
[ -s "${1:-}" ] || { echo "Thiếu file token"; exit 1; }
mkdir -p /opt/smsie/logs
install -m 600 -o root "$1" /opt/smsie/tunnel.token
install -m 644 tunnel.yml /opt/smsie/tunnel.yml
install -m 644 vn.jione.smsie-tunnel.plist /Library/LaunchDaemons/vn.jione.smsie-tunnel.plist
launchctl bootout system/vn.jione.smsie-tunnel 2>/dev/null && sleep 3 || true
launchctl bootstrap system /Library/LaunchDaemons/vn.jione.smsie-tunnel.plist
sleep 5; grep -E "Registered tunnel connection|ERR" /opt/smsie/logs/tunnel.err.log | tail -4 || true
