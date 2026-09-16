# Spec: 4 mục cuối roadmap — số thuê bao · audit trail · báo cáo tháng · sao lưu tự động

Chốt với người dùng 16/09/2026: "làm" cả bốn. Mỗi mục độc lập, cùng một nhánh `feat/roadmap-4`, một PR.

---

## 1. Tự đọc số thuê bao

**Mục tiêu:** SIM cắm vào là biết số, không phải gõ tay; đổi số (đảo SIM) thì có lịch sử.

**Cách:** mỗi nhà mạng có mã USSD tra số; hệ thống gửi USSD rồi bắt trả lời trong 90 giây.

```yaml
phone_lookup:
  enabled: true
  codes:                 # theo tên nhà mạng như worker đọc được (mccmnc.json / +COPS)
    Viettel: "*098#"
    Vinaphone: "*110#"
    Mobifone: "*0#"
    Vietnamobile: "*102#"
```

- Worker: `RequestPhoneNumber() error` — tra mã theo `Operator` (so sánh không phân biệt hoa thường, chứa chuỗi); không có mã → lỗi `chưa cấu hình mã tra số cho <operator>`. Gửi `AT+CUSD=1,"<code>",15`, mở cửa sổ bắt 90 s (`phoneLookupUntil time.Time`).
- Bắt: trong cửa sổ, mọi `+CUSD:` và SMS nhận được đều qua `parsePhoneNumber(text)`: regex `(?:\+?84|0)(\d{9})\b` → chuẩn hoá `0XXXXXXXXX`; số đầu tiên hợp lệ (đầu 03/05/07/08/09) thắng. Ngoài cửa sổ không parse (tránh nhặt số trong tin OTP).
- Lưu: khác `modems.phone_number` → UPDATE + INSERT `phone_number_history(iccid, old_phone, new_phone, source='ussd'|'manual', at)`. Sửa tay qua `/profile` cũng ghi `manual`.
- Kích hoạt: (a) lúc probe xong, nếu `phone_number` rỗng và đã đăng ký mạng và `enabled`; (b) `POST /api/v1/modems/:iccid/phone-lookup` (quyền `send_at` — vì là lệnh AT chủ động) → 202, UI poll `/modems/:iccid` 5 s × 18 như kiểm tra số dư; (c) sau sự kiện đảo SIM (`inserted`/`moved`) nếu số rỗng.
- API thêm: `GET /api/v1/modems/:iccid/phone-history` (view_sms).
- UI: nút "Đọc số" cạnh ô số thuê bao ở Hiệu chuẩn + Bảo trì; card SIM ở Lịch sử khe hiện "Số trước: …" nếu có lịch sử.
- Test: `parsePhoneNumber` (84…, 0…, +84, số OTP 6 số không nhặt, đầu số lạ bỏ); cửa sổ đóng → không ghi; đổi số → history.

---

## 2. Nhật ký hành động (audit trail)

**Mục tiêu:** ai làm gì, lúc nào, với SIM nào — thật, không preview.

```
audit_logs
  id, at index, username (hoặc 'apikey:<name>' / 'system'), user_id NULL, api_key_id NULL,
  action text index   -- mã hành động ổn định (bảng dưới)
  iccid text index NULL, target text (ví dụ số ĐT, IMEI, user id), detail text (JSON rút gọn, đã bỏ mật khẩu/khoá),
  status int, ip text
```

- Middleware `Audit(db)` gắn sau `AuthMiddleware` trên `authGroup`/`adminGroup`: chỉ ghi request có method POST/PATCH/DELETE (GET không ghi). Ghi **sau** handler (`c.Next()`), lấy `c.Writer.Status()`. `action` map từ `c.FullPath()` + method, ví dụ:
  - `POST /api/v1/modems/:iccid/send` → `sms.send` (target = số nhận, detail = 60 ký tự đầu)
  - `POST …/call/dial` → `call.dial`; `…/call/hangup` → `call.hangup`; `…/at` → `at.exec` (detail = lệnh); `…/balance-check` → `balance.check`; `…/phone-lookup` → `phone.lookup`; `…/reboot` → `modem.reboot`
  - `PATCH …/profile` → `modem.profile` (detail = các key gửi lên); `PATCH /bays/:imei` → `bay.assign`; `POST /balance/run` → `balance.run`; `POST /keepalive/run` → `keepalive.run`
  - users/apikeys/webhooks: `user.create|delete|permissions`, `apikey.create|rotate|delete`, `webhook.create|delete`; `POST /auth/change_password` → `auth.password` (detail rỗng)
  - route không có trong bảng → `action = "<METHOD> <FullPath>"` (vẫn ghi).
- Body: đọc tối đa 4 KB, parse JSON nếu được, **xoá** key chứa `password`, `secret`, `key`, `token`; `content` của SMS cắt 60 ký tự. Không parse được → không lưu detail.
- Hệ thống: `audit.Record(db, Entry{Username:"system", Action:"keepalive.send", ICCID, Target: số đích, Status})` từ keepalive service khi `sent`/`failed`; cảnh báo webhook không ghi (đã có bảng riêng).
- API `GET /api/v1/audit?page=&page_size=&iccid=&username=&action=&from=&to=` — admin.
- UI Nhật ký: bảng thật (thời gian · người · hành động (nhãn Việt) · đối tượng · kết quả), bộ lọc người/hành động/khoảng ngày, phân trang 50, nút CSV. Bỏ các dòng suy diễn từ SMS.
- Giữ dữ liệu: `audit.keep_days` (config, mặc định 365) — dọn lúc khởi động + hằng ngày.

---

## 3. Báo cáo tháng

**Mục tiêu:** một bảng theo SIM cho tháng chọn, tải CSV, nhìn được xu hướng chi phí.

- `GET /api/v1/reports/monthly?month=YYYY-MM` (mặc định tháng hiện tại; admin thấy hết, user thường lọc theo quyền xem SIM) →

```json
{ "month":"2026-09", "rows":[{ "iccid","phone_number","slot_number",
   "sms_received","sms_sent","sms_failed",
   "calls","call_seconds",
   "balance_start","balance_end","balance_delta",      // snapshot đầu/cuối tháng; null nếu không có
   "slot_events","keepalive_sent","alerts" }],
  "totals":{ …cùng khoá số… } }
```
  Nguồn: `sms` (type + status), `call_recordings` (count, sum duration), `balance_snapshots` (MIN/MAX theo read_at trong tháng — lấy bằng `id` đầu/cuối), `sim_slot_events`, `keepalive_runs status=sent`, `balance_alerts + sim_alerts`.
- `…&format=csv` → file `smsie-bao-cao-YYYY-MM.csv` (BOM, dấu phẩy, tiêu đề tiếng Việt).
- UI Báo cáo: ô chọn tháng (`<input type="month">`), bảng + dòng tổng, nút "Tải CSV tháng"; giữ 4 KPI + biểu đồ trạng thái SMS hiện có nhưng tính theo tháng chọn.

---

## 4. Sao lưu tự động + khôi phục

**Mục tiêu:** không ai phải nhớ bấm sao lưu; khôi phục an toàn, có đường lùi.

```yaml
backup:
  enabled: true
  dir: "backups"      # tương đối gốc app
  hour: 3
  keep: 14            # giữ N bản mới nhất
```

- Hằng ngày `hour:00` (và không chạy lúc boot): `VACUUM INTO backups/smsie-YYYYMMDD-HHMMSS.db` → mở file vừa tạo bằng driver sqlite, `PRAGMA integrity_check` phải trả `ok` → nếu lỗi: xoá file, `logger.Error`, ghi `sim_alerts`? KHÔNG (không phải SIM) → ghi `audit_logs` action `backup.failed` username `system` + webhook tới **mọi** webhook (không có ICCID) — thêm `WebhookService.Broadcast(text)`. Thành công → xoá bản cũ ngoài `keep`, audit `backup.ok` (detail = tên file, size).
- Chỉ SQLite; driver khác → log warn một lần, không chạy.
- API admin: `GET /api/v1/admin/backups` (danh sách tên, size, thời gian), `POST /api/v1/admin/backups/run` (chạy ngay, đồng bộ, trả kết quả), `GET /api/v1/admin/backups/:name` (tải), `POST /api/v1/admin/restore` (multipart file ≤ 512 MB hoặc `{name}` chọn bản có sẵn).
- **Khôi phục** (cơ chế một chỗ, dùng cho cả API lẫn script):
  1. API kiểm header `SQLite format 3\0` + `PRAGMA integrity_check` trên file tải lên (ghi tạm) → lỗi 400.
  2. Ghi file thành `<dsn>.restore-pending` cạnh DB thật, audit `restore.staged`, trả 202 `{restart_required:true}`.
  3. Nếu chạy dưới Windows Service (env `SMSIE_SERVICE=1` do `smsie-service.xml` đặt) → sau 2 s `os.Exit(3)`; WinSW `onfailure=restart` sẽ khởi động lại. Không phải service → trả `restart_required` và ghi log "khởi động lại tay".
  4. **Lúc khởi động**, trước khi mở DB: nếu có `<dsn>.restore-pending` → chép DB hiện tại thành `backups/pre-restore-YYYYMMDD-HHMMSS.db`, đổi tên pending → dsn, log + audit `restore.applied` (sau khi DB mở).
  5. `restore-backup.ps1` sửa thành: kiểm header, copy sang `<dsn>.restore-pending`, `Restart-Service smsie` — không tự thay file nữa (Go làm ở bước 4). `install-service.ps1`/`smsie-service.xml` thêm `<env name="SMSIE_SERVICE" value="1"/>`.
- UI Báo cáo (chỗ đã có nút Sao lưu): khối "Sao lưu": bản mới nhất, lịch, danh sách 14 bản (tải), nút "Sao lưu ngay"; nút **Khôi phục** bật lên: chọn bản có sẵn hoặc tải file → xác nhận (modal ghi rõ "app sẽ khởi động lại, mất ~10 s") → gọi API → hiện "Đã xếp lịch khôi phục, đang khởi động lại…" rồi poll `/ping` tới khi sống lại.

---

## Kiểm thử chung

- Go: `parsePhoneNumber`; audit middleware (ghi POST, không ghi GET, xoá password, status code); action map; monthly report với sqlite seed (đếm đúng, balance start/end, CSV có BOM); backup run tạo file + integrity + prune; restore staging + apply-on-boot (hàm thuần nhận đường dẫn, test với thư mục tạm).
- Node: `describeAudit(action)` nhãn Việt; `formatReportRow`.

## Ngoài phạm vi

MySQL backup. Xoá SIM tự động. Gửi báo cáo qua email.
