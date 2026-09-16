# Spec: Cảnh báo số dư thấp + dự báo ngày hết tiền

## Mục tiêu

Backend tự đọc số dư `*101#` mỗi ngày cho SIM đang online, lưu chuỗi số dư, tính ngày dự kiến hết tiền,
và cảnh báo (trang Cảnh báo + webhook Telegram/Slack đã cấu hình cho SIM) khi số dư dưới ngưỡng hoặc sắp hết.

## Đã chốt với người dùng (16/09/2026)

- Backend tự chạy `*101#` **1 lần/ngày/SIM online** (USSD miễn phí — không phải automated SMS/gọi). Giờ chạy và bật/tắt trong `config.yaml`.
- Ngưỡng **mặc định toàn cục** trong config, **ghi đè từng SIM** trên UI.
- Lịch sử số dư lưu mỗi lần đọc được (bất kể nguồn: lịch, UI, đảo SIM).
- Dự báo = hồi quy tuyến tính trên ≤7 mốc gần nhất; chỉ hiện khi xu hướng giảm.
- Webhook mỗi SIM **tối đa 1 lần/ngày**; cảnh báo cũng hiện ở trang Cảnh báo và tô cam ô khe.

## Cấu hình (`config.yaml`)

```yaml
balance:
  enabled: true          # tắt = không tự đọc *101#, cảnh báo vẫn tính từ dữ liệu có sẵn
  check_hour: 6          # giờ địa phương chạy đọc số dư hằng ngày (0–23)
  low_threshold_vnd: 20000
  forecast_days: 7       # cảnh báo khi dự báo hết tiền trong ≤ N ngày
  ussd_code: "*101#"     # mã USSD kiểm tra số dư (nhà mạng VN)
```

## Mô hình dữ liệu

```
modems.low_balance_vnd  bigint NULL    -- ghi đè ngưỡng cho SIM; NULL = dùng config

balance_snapshots              -- chuỗi số dư, append-only
  id, iccid index, balance_vnd, read_at index

balance_alerts                 -- nhật ký cảnh báo đã bắn (chống lặp)
  id, iccid index, kind ('low' | 'forecast'), balance_vnd, days_left real NULL, sent_at index
```

`captureBalance()` (worker) sau khi `UpdateBalance` → INSERT `balance_snapshots`.

## Luồng

1. **Lịch đọc**: goroutine `balance.Scheduler` tính mốc `check_hour` kế tiếp; đến giờ → duyệt worker online có `Registration` = registered → `RequestBalance()` cách nhau 3 s (tránh dồn USSD). Bỏ qua SIM đã đọc trong 20 giờ qua.
2. **Đánh giá** (chạy 15 phút sau mốc đọc, và ngay lúc khởi động sau 2 phút): với mỗi modem có số dư:
   - `nguong = modem.low_balance_vnd ?? config.low_threshold_vnd`
   - `days_left, ok = Forecast(7 mốc gần nhất)` — hồi quy tuyến tính balance ~ time; `ok` khi ≥2 mốc, trải ≥ 24 h, slope < 0; `days_left = balance / (-slope)`.
   - `level`: `low` nếu balance < nguong; `forecast` nếu ok && days_left ≤ forecast_days; `unknown` nếu chưa có số dư; else `ok`.
   - Nếu level ∈ {low, forecast} và chưa có `balance_alerts` cho ICCID trong 24 h → INSERT alert + bắn webhook của ICCID (text: `⚠️ SIM {phone|iccid} (khe N): số dư {balance} đ, dưới ngưỡng {nguong} đ` hoặc `… dự kiến hết tiền sau ≈{days} ngày`).
3. **Webhook**: `WebhookService.DispatchText(iccid, text)` — dùng cùng bảng `webhooks`, cùng định dạng payload, bỏ qua template.

## API

- `GET /api/v1/balance/status` — mọi user; lọc theo quyền xem SIM. Mỗi phần tử: `iccid, phone_number, slot_number, balance_vnd, balance_updated_at, threshold_vnd, threshold_source ('sim'|'config'), days_left (null nếu không dự báo), level, snapshots [{read_at, balance_vnd}] (≤7)`.
- `GET /api/v1/balance/alerts?page=&page_size=` — admin; trả `{data, total, page, page_size}`.
- `PATCH /api/v1/modems/:iccid/profile` nhận thêm `low_balance_vnd` (int ≥ 0 hoặc `null` để về mặc định).
- `POST /api/v1/balance/run` — admin; chạy ngay chu kỳ đọc + đánh giá (để kiểm thử/ép chạy).

## Giao diện

1. **Trang Cảnh báo**: thẻ `danger` "Khe N · 0987… số dư 12.000 đ dưới ngưỡng 20.000 đ" / `warning` "… dự kiến hết tiền sau ≈3 ngày"; nút "Đọc số dư ngay" (POST /balance/run, admin).
2. **Bản đồ khay**: `.bal.low` theo `level` từ `/balance/status` (không còn hằng 20000 trong JS); popover thêm dòng "Dự kiến hết: ≈N ngày".
3. **Hiệu chuẩn**: cột "Ngưỡng" — input số (admin), trống = mặc định config; hiển thị `(mặc định)` khi NULL.
4. **Bảo trì**: dòng số dư thêm "· ≈N ngày nữa hết" và sparkline text 7 mốc `52k → 48k → 41k`.
5. KPI tổng quan: ô "SIM sắp hết tiền" = số SIM level low|forecast.

## Kiểm thử

- `go test`: `Forecast` (không đủ mốc, tăng, giảm đều, giảm rồi nạp tiền), `Evaluate` (ngưỡng SIM thắng config; chống lặp 24 h; level).
- `go test` API: status lọc theo quyền; profile nhận `low_balance_vnd` null/0/âm→400.
- `node --test`: `describeBalanceLevel` (nhãn + tone), `balanceSparkline`.

## Ngoài phạm vi

Không gửi SMS/gọi. Không tự nạp tiền. Không parse số dư định dạng mới (dùng `parseBalanceVND` sẵn có).
