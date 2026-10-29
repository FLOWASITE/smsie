# smsie trên Mac (24/7) + smsie.jione.vn

1. Chép `smsie-mac.tar.gz` sang Mac, giải nén, cắm khay modem vào Mac.
2. `sudo bash cai-dat.sh /đường/dẫn/smsie.db` — DB lấy từ máy Windows
   (`C:\Users\Admin\Documents\ChatGPT\New project\smsie\smsie.db`, dừng service Windows trước khi chép).
   Cài vào `/opt/smsie`, launchd `vn.jione.smsie` tự chạy lại khi chết/khởi động; tắt ngủ, mất điện tự bật.
3. Tunnel (một lần):
   ```
   brew install cloudflared && cloudflared tunnel login
   cloudflared tunnel create smsie
   cloudflared tunnel route dns smsie smsie.jione.vn
   ```
   `~/.cloudflared/config.yml`:
   ```
   tunnel: smsie
   credentials-file: /Users/<user>/.cloudflared/<TUNNEL_ID>.json
   ingress:
     - hostname: smsie.jione.vn
       service: http://127.0.0.1:18080
     - service: http_status:404
   ```
   rồi `sudo cloudflared service install`.
4. Nên bật Cloudflare Access cho smsie.jione.vn (Zero Trust → Applications) — trang có OTP/SMS.

Log: `/opt/smsie/logs/`. Khởi động lại: `sudo launchctl kickstart -k system/vn.jione.smsie`.
Khe SIM: tự suy khe chỉ hiểu tên COM — trên Mac hiệu chuẩn khe lại ở tab Khe SIM.
