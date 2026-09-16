# Spec: Nuôi SIM (keep-alive)

## Mục tiêu

Giữ SIM khỏi bị nhà mạng thu hồi vì không phát sinh cước: định kỳ tự gửi **một SMS nội mạng** từ SIM cần nuôi
sang một SIM khác trong cùng khay (cùng nhà mạng), ghi nhật ký, cảnh báo khi thất bại, có trần chi phí.

## Đã chốt với người dùng (16/09/2026)

- Hành động = **SMS nội mạng sang SIM khác trong khay** (phương án A). Không gọi, không USSD.
- **Tắt toàn cục mặc định** (`keepalive.enabled: false`); **từng SIM phải bật tay** (`modems.keepalive_enabled`).
- Chu kỳ mặc định **25 ngày** kể từ hoạt động phát sinh cước gần nhất (SMS `sent` hoặc `received` đều tính, keepalive thành công cũng tính).
- Chạy sau chu kỳ đọc số dư hằng ngày; ghi `keepalive_runs`; thất bại → cảnh báo; trần **3 lần/SIM/tháng**.

## Cấu hình

```yaml
keepalive:
  enabled: false        # công tắc tổng — phải bật tường minh vì tốn tiền
  interval_days: 25     # gửi khi không có SMS đi/đến trong N ngày
  max_per_month: 3      # trần số lần gửi mỗi SIM mỗi tháng dương lịch
  message: "keepalive {{.Date}}"   # nội dung; {{.Date}} = yyyy-mm-dd
  run_hour: 7           # giờ chạy hằng ngày (sau giờ đọc số dư)
```

## Dữ liệu

```
modems.keepalive_enabled   bool NOT NULL DEFAULT false   -- bật/tắt từng SIM (UI Hiệu chuẩn / Bảo trì)
modems.keepalive_interval  int  NULL                     -- ghi đè chu kỳ; NULL = config

keepalive_runs                                          -- nhật ký, append-only
  id, iccid index, target_iccid, target_phone, status ('sent'|'failed'|'skipped'),
  reason text, ran_at index
```

## Chọn SIM đích

Trong `modem_bays.current_iccid` khác ICCID nguồn, ưu tiên: (1) cùng `operator` (chuỗi nhà mạng runtime),
(2) có `phone_number`, (3) đang online. Xoay vòng: chọn SIM ít được làm đích nhất trong 30 ngày (đếm `keepalive_runs.target_iccid`).
Không có ứng viên → `skipped` với reason `không có SIM cùng nhà mạng có số điện thoại trong khay`.

## Luồng

Mỗi ngày lúc `run_hour` (và không chạy lúc boot):
1. `cfg.enabled == false` → không làm gì.
2. Với mỗi modem `keepalive_enabled` đang online + registered:
   - `last_activity` = max(`MAX(sms.created_at)` mọi loại của ICCID, `MAX(keepalive_runs.ran_at) WHERE status='sent'`); NULL → dùng `first_seen_at`.
   - Nếu `now - last_activity < interval` → bỏ qua (không ghi run).
   - Nếu số run `sent` trong tháng dương lịch này ≥ `max_per_month` → ghi `skipped` reason `đã đạt trần N lần/tháng`, cảnh báo 1 lần/tháng.
   - Chọn đích; gửi qua `worker.SendSMS(target_phone, message)`; thành công → `sent` (SMS đã được worker lưu bảng `sms` type `sent` như bình thường); lỗi → `failed` + reason.
3. `failed`/`skipped` → `sim_alerts` kind `keepalive` (dùng lại bảng của SIM chết, remind 7 ngày) + webhook `🔁 SIM …: nuôi SIM thất bại — {reason}`.
4. Cách 5 s giữa các SIM.

Tuyệt đối không gửi khi `enabled=false` ở bất kỳ tầng nào; API `POST /keepalive/run` chỉ chạy được khi `cfg.enabled`.

## API

- `GET /api/v1/keepalive/status` — mọi user, lọc quyền: `[{iccid, phone_number, slot_number, enabled, interval_days, last_activity_at, next_due_at, sent_this_month, last_run:{status, reason, ran_at, target_phone}}]`.
- `GET /api/v1/keepalive/runs?iccid=&page=&page_size=` — admin.
- `POST /api/v1/keepalive/run` — admin; `{iccid?}`: chạy ngay cho một SIM (bỏ qua điều kiện interval nhưng **vẫn** tôn trọng trần tháng) hoặc cả khay theo đúng luật; 409 khi `cfg.enabled=false`.
- `PATCH /api/v1/modems/:iccid/profile` nhận thêm `keepalive_enabled` (bool), `keepalive_interval` (int ≥1 | null).

## Giao diện

- **Bảo trì** (trang "Lịch duy trì" hiện là preview): thay khối preview bằng dữ liệu thật — mỗi SIM: công tắc "Nuôi SIM", chu kỳ, "Hoạt động gần nhất", "Lần nuôi kế tiếp", lần chạy cuối (trạng thái + lý do), nút "Nuôi ngay" (admin, chỉ khi công tắc tổng bật). Banner đầu trang khi `keepalive.enabled=false`: "Công tắc tổng đang TẮT trong config.yaml — không SIM nào được gửi".
- KPI Bảo trì: "Lịch đang bật" = số SIM bật; "Lần chạy kế tiếp" = `run_hour` hôm nay/mai; "Ngân sách tháng" = tổng `sent` tháng này × ~300 đ (ghi rõ ước tính).
- **Cảnh báo**: thẻ `warning` cho kind `keepalive`.
- **Hiệu chuẩn**: cột "Nuôi" = checkbox (admin).

## Kiểm thử

- `go test` thuần: `pickTarget` (cùng nhà mạng, có số, xoay vòng), `isDue` (interval, last_activity NULL → first_seen), trần tháng.
- Service với sqlite + fake sender (interface `Sender{ SendSMS(iccid, phone, msg) error }`): enabled=false → không gọi sender; due + có đích → 1 run `sent`; sender lỗi → `failed` + alert; trần → `skipped`.
- API: profile nhận 2 trường; run 409 khi tắt.
- `node --test`: `describeKeepalive(item)`.

## Ngoài phạm vi

Không gọi điện. Không nạp tiền. Không gửi ra ngoài khay.
