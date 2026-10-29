# smsie trên Mac (24/7) + smsie.jione.vn

1. Chép `smsie-mac.tar.gz` sang Mac, giải nén, cắm khay modem vào Mac. Hoặc build ngay trên Mac (`brew install go`), từ gốc repo:
   `CGO_ENABLED=0 go build -tags nouac -o deploy/mac/smsie-arm64 . && cp -R web deploy/mac/`, rồi tạo `deploy/mac/config.yaml`
   từ `config.yaml.example` với `port: "127.0.0.1:18080"`, `mode: "release"`, `exclude_ports: ["/dev/cu.J237", "/dev/cu.debug-console"]`.
2. `sudo bash cai-dat.sh /đường/dẫn/smsie.db` — DB lấy từ máy Windows
   (`C:\Users\Admin\Documents\ChatGPT\New project\smsie\smsie.db`, dừng service Windows trước khi chép).
   Cài vào `/opt/smsie`, launchd `vn.jione.smsie` tự chạy lại khi chết/khởi động; tắt ngủ, mất điện tự bật.
3. Tunnel: dùng lại tunnel `sms.jione` (DNS smsie.jione.vn, sms.jione.vn đã trỏ sẵn), chạy thành dịch vụ riêng
   `vn.jione.smsie-tunnel` — không dùng `cloudflared service install` (đè `~/.cloudflared/config.yml` của tunnel khác trên máy):
   ```
   cloudflared tunnel token sms.jione > ~/.cloudflared/sms.jione.token   # cần cert.pem tài khoản jione.vn
   sudo bash cai-tunnel.sh ~/.cloudflared/sms.jione.token
   ```
   Ingress nằm ở `tunnel.yml` (tunnel cấu hình local, dashboard không có). Tắt connector trên Windows
   (`Stop-Service cloudflared; Set-Service cloudflared -StartupType Disabled`) để không chia traffic hai máy.
4. Nên bật Cloudflare Access cho smsie.jione.vn (Zero Trust → Applications) — trang có OTP/SMS.

Log: `/opt/smsie/logs/`. Khởi động lại: `sudo launchctl kickstart -k system/vn.jione.smsie`.
Khe SIM: tự suy khe chỉ hiểu tên COM — trên Mac hiệu chuẩn khe lại ở tab Khe SIM.
