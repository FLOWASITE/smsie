# Spec: Cảnh báo SIM "chết"

## Mục tiêu

Phát hiện sớm SIM sắp bị nhà mạng thu hồi / đã hỏng: im lặng quá lâu, không đăng ký mạng dù modem online,
hoặc bị rút khỏi khay và quên. Cảnh báo ở trang Cảnh báo + webhook của SIM, nhắc lại mỗi 7 ngày.

## Ba dấu hiệu (mỗi cái một `kind`)

| kind | Điều kiện | Mặc định |
|---|---|---|
| `no_sms` | không có SMS `received` nào trong `no_sms_days` ngày **và** SIM có ít nhất 1 SMS từng nhận hoặc đã thấy ≥ `no_sms_days` ngày (tránh báo SIM mới cắm) | 30 ngày |
| `unregistered` | modem đang online nhưng `last_registered_at` cách hiện tại ≥ `unregistered_hours` (hoặc NULL và `first_seen_at` ≥ ngưỡng đó) | 24 giờ |
| `absent` | SIM không ở khe nào (`modem_bays.current_iccid` không có) và sự kiện `removed` gần nhất ≥ `absent_days` | 7 ngày |

## Cấu hình

```yaml
sim_health:
  enabled: true
  no_sms_days: 30
  unregistered_hours: 24
  absent_days: 7
  remind_days: 7        # nhắc lại cùng một (SIM, kind) sau N ngày
```

## Dữ liệu

```
modems.last_registered_at datetime NULL  -- worker ghi khi CREG 1/5, tối đa 1 lần/10 phút
modems.first_seen_at      datetime NULL  -- lần đầu thấy ICCID (backfill = MIN(sms.timestamp) hoặc now lúc migrate)

sim_alerts                                -- nhật ký cảnh báo sức khoẻ SIM, chống lặp
  id, iccid index, kind ('no_sms'|'unregistered'|'absent'), detail text, sent_at index
```

`last_sms_received_at` không lưu — tính bằng `MAX(timestamp) WHERE type='received'` theo ICCID (một query gộp).

## Luồng

- Worker `checkSignal`: khi `regCode` là 1/5 và lần ghi trước cách ≥10 phút → `UPDATE modems SET last_registered_at = now`.
- Worker probe (sau Upsert): nếu `first_seen_at` NULL → set now.
- `simhealth.Evaluate(now, cfg, inputs)` thuần: nhận danh sách `Input{ICCID, Online, LastRegisteredAt, FirstSeenAt, LastSMSAt, InBay, LastRemovedAt}` → `[]Finding{ICCID, Kind, Detail}`.
- Scheduler số dư có thêm bước `simhealth` trong cùng chu kỳ Evaluate (2 phút sau boot + hằng ngày): collect inputs (modems + bays + sms MAX + sim_slot_events removed MAX) → Evaluate → với mỗi finding chưa có `sim_alerts` cùng (iccid, kind) trong `remind_days` → INSERT + `DispatchText`.
- Text: `🪦 SIM {phone|iccid} (khe N): không nhận SMS nào 31 ngày — có thể bị thu hồi` / `📡 … không đăng ký mạng 26 giờ dù modem online` / `📤 … đã rút khỏi khay 9 ngày`.

## API

- `GET /api/v1/sim-health` — mọi user, lọc theo quyền xem SIM: `[{iccid, phone_number, slot_number, online, last_registered_at, last_sms_at, first_seen_at, in_bay, last_removed_at, findings:[{kind, detail}]}]`.
- `GET /api/v1/sim-health/alerts?page=&page_size=` — admin.

## Giao diện

- Trang Cảnh báo: thẻ `danger` cho `no_sms`/`unregistered`, `warning` cho `absent`; tiêu đề `Khe N · phone`, note = detail.
- Bản đồ khay: ô có finding thêm chấm đỏ nhỏ góc dưới (class `sick`), popover thêm dòng "Sức khoẻ: …".
- Bảo trì: dòng "SMS cuối: dd/mm · đăng ký mạng: dd/mm hh:mm".
- KPI Cảnh báo note: `${n} SIM sắp hết tiền · ${m} SIM có dấu hiệu chết`.

## Kiểm thử

- `go test`: `simhealth.Evaluate` bảng trường hợp (mới cắm chưa đủ ngày → không báo; im 31 ngày → no_sms; online + không đăng ký 25h → unregistered; offline không báo unregistered; vắng khay 8 ngày → absent; vắng 3 ngày → không).
- Scheduler: nhắc lại đúng chu kỳ 7 ngày (đã báo 3 ngày trước → không báo lại; 8 ngày → báo).
- API: lọc theo quyền.
- `node --test`: `describeHealthFinding`.

## Ngoài phạm vi

Không tự nhắn/gọi để "nuôi" SIM (mục roadmap riêng). Không tự xoá SIM khỏi hệ thống.
