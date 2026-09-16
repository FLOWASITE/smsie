# Spec: Lịch sử đảo SIM (khe = modem, SIM đi qua khe)

## Mục tiêu

Ghi lại tự động mỗi lần một thẻ SIM (ICCID) đổi khe vật lý, kèm số dư tại thời điểm phát hiện,
và làm cho số khe trên giao diện luôn phản ánh đúng khay thật thay vì bám theo ICCID gán tay.

## Bối cảnh vật lý (đã chốt)

- 32 modem Quectel EC20 nằm **cố định** trong khay, mỗi modem = một khe.
- Thao tác "đảo SIM" = rút thẻ SIM ra cắm sang modem khác. **IMEI ở lại khe, ICCID di chuyển.**
- Do đó danh tính khe = **IMEI** (đọc bằng `AT+GSN` lúc probe, không phụ thuộc COM/USB path).

## Hiện trạng trên `main`

- `modems` khóa chính ICCID; `slot_number` là cột gán tay per-ICCID → đảo SIM làm sơ đồ khe sai âm thầm, không có lịch sử.
- Worker probe đã đọc ICCID, IMEI, operator, sóng; `RequestBalance()` tự chạy `*101#` và `captureBalance()` parse số dư.

## Mô hình dữ liệu

```
modem_bays                       -- khay vật lý, hiệu chuẩn một lần
  imei          text PK
  slot_number   int  NULL UNIQUE -- 1..32; NULL = modem đã thấy nhưng chưa gán khe
  current_iccid text NULL        -- SIM đang ở khe; NULL = khe trống
  last_seen_at  datetime

sim_slot_events                  -- lịch sử; chỉ INSERT
  id            int PK
  detected_at   datetime  index
  iccid         text      index
  imei          text
  phone_number  text               -- copy từ modems.phone_number lúc phát hiện
  operator      text
  event         text               -- inserted | moved | removed
  from_slot     int NULL           -- NULL khi inserted
  to_slot       int NULL           -- NULL khi removed
  balance_vnd   int NULL           -- số dư *101# đọc ngay sau khi phát hiện; NULL nếu USSD lỗi/timeout
  port_name     text               -- COMx lúc đó, để debug
```

`modems.slot_number` giữ lại làm **cache** do hệ thống ghi từ `modem_bays`; bỏ khỏi `PATCH /modems/:iccid/profile`.

### Kế thừa lúc nâng cấp (một lần, chạy sau AutoMigrate)

Với mỗi dòng `modems` có `slot_number` và `imei` không rỗng → tạo `modem_bays(slot_number, imei, current_iccid=iccid)`.
Trùng slot hoặc trùng IMEI thì bỏ qua dòng sau, ghi warn. Không ghi event cho bước kế thừa.

## Luồng phát hiện

Móc vào `worker.go` ngay sau bước "8. Register in DB" (đã có ICCID, IMEI, operator, port). Logic quyết định là một hàm thuần
`decideSlotEvent(bays, imei, iccid) []event` để test được không cần modem:

```
bay := bays.byIMEI(imei)
không có bay          → tạo bay(slot=NULL, imei, current=iccid); event inserted(to_slot=NULL)   -- "modem lạ, chưa gán khe"
bay.current == iccid  → không làm gì, cập nhật last_seen_at
bay.current != iccid:
    old := bays.byICCID(iccid)          -- SIM này đang được ghi ở khe khác?
    nếu old != nil  → event moved(from=old.slot, to=bay.slot); old.current = NULL
    ngược lại       → event inserted(to=bay.slot)
    nếu bay.current != "" (SIM cũ của khe này chưa thấy đâu) → event removed(iccid=bay.current, from=bay.slot, to=NULL)
    bay.current = iccid; modems.slot_number = bay.slot
```

Sau khi ghi event cho `iccid` hiện tại (moved/inserted) → gọi `RequestBalance()`; khi `captureBalance()` nhận số dư,
UPDATE `balance_vnd` của event mới nhất chưa có số dư của ICCID đó trong 5 phút gần nhất.

`removed` cũng được ghi khi probe trả `+CME ERROR: 10` (SIM not inserted) trên modem đã có bay: `bay.current = NULL`.
**Không** ghi `removed` khi worker dừng vì rớt COM/khởi động lại — tránh rác.

Ràng buộc: `modem_bays.slot_number` unique 1..32 khi không NULL; một IMEI một bay.

Modem lạ mà ICCID đang được ghi ở khe khác → khe cũ giữ `current_iccid` cũ cho tới lần probe sau (chấp nhận).

## API

- `GET /api/v1/slot-events?iccid=&slot=&from=&to=&page=&page_size=` — admin, hoặc user có `CanViewSMS` trên ICCID đó.
- `GET /api/v1/bays` — 32 khe + bay chưa gán slot; trả `slot_number, imei, current_iccid, last_seen_at` + trạng thái runtime của modem.
- `PATCH /api/v1/bays/:imei { "slot_number": n | null }` — admin; 409 nếu slot đã có IMEI khác.
- `PATCH /api/v1/modems/:iccid/profile` — bỏ `slot_number`.
- Webhook (tuỳ chọn, mặc định tắt): sự kiện `moved/inserted/removed` bắn qua webhook đã có với template `{{.Event}} {{.ICCID}} {{.FromSlot}}→{{.ToSlot}}`.

## Giao diện

1. **Bản đồ khay 8×4** (thay `mini-slot-grid` ở Tổng quan và là nội dung chính của trang Khe SIM):
   ô = khe, tô theo trạng thái `online / sóng yếu / không SIM / ngoại tuyến / vừa đảo trong 24h` (viền nhấn), hover hiện ICCID·số ĐT·số dư.
   Bay chưa gán slot xếp riêng dải "Modem chưa gán khe" với ô chọn số khe.
2. **Chi tiết SIM → tab "Lịch sử khe"**: dòng thời gian `giờ · khe cũ → khe mới · số dư`, lọc theo khoảng ngày, xuất CSV.
3. **Trang Khe SIM → Hiệu chuẩn**: bảng 32 dòng `khe · IMEI · SIM hiện tại · lần thấy cuối`, ô chọn IMEI (danh sách modem đang online chưa có khe).
4. **Nhật ký (Audit)** hiện thêm các event đảo SIM.

## Kiểm thử

- `go test`: bảng trường hợp cho `decideSlotEvent` (modem lạ · không đổi · inserted · moved · removed · đảo chéo A↔B trong một vòng quét).
- `go test`: kế thừa migration với dữ liệu trùng slot/IMEI.
- `node --test`: render bản đồ 8×4 đủ 32 ô và dải chưa gán.

## Ngoài phạm vi (giữ nguyên)

Automated calls/SMS vẫn tắt. Không đụng SIP/UAC. Không đổi cơ chế probe AT.

## Roadmap (đã chọn, mỗi mục một spec riêng, theo thứ tự)

1. **Cảnh báo số dư thấp + dự báo ngày hết tiền** — ngưỡng per-SIM; dự báo từ chuỗi `balance_vnd` theo thời gian (hồi quy tuyến tính trên 7 mốc gần nhất); bắn webhook Telegram đã có.
2. **Cảnh báo SIM "chết"** — không nhận SMS > N ngày hoặc `registration` ≠ registered liên tục > M giờ; N/M cấu hình; mục Cảnh báo trên console.
3. **Lịch "nuôi SIM"** — định kỳ USSD `*101#` (không tốn tiền) hoặc 1 SMS nội mạng theo lịch per-SIM; ghi vào `sim_slot_events` với `event=keepalive`. **Cần admin bật tường minh** vì spec cũ cố ý tắt automated SMS.
4. **Tự đọc số thuê bao** — parse SMS tổng đài (Viettel/Vina/Mobi) chứa "số thuê bao", giống `parseBalanceVND`; ghi lịch sử số theo ICCID.
5. **Audit trail thật** — bảng `audit_logs(user_id, action, iccid, payload, at)`; ghi ở middleware cho send SMS / dial / AT / đổi khe / đổi profile.
6. **Báo cáo tháng CSV/Excel** — SMS in/out, cuộc gọi, số dư, sự kiện đảo; endpoint `GET /reports/monthly?month=`.
7. **Bản đồ khay 8×4** — nằm trong spec này (mục Giao diện 1).
8. **Sao lưu tự động + khôi phục** — timer hằng ngày ghi snapshot ra thư mục giữ N bản; hoàn thiện `restore-backup.ps1` với kiểm tra `PRAGMA integrity_check` trước khi thay file và restart service.
