# 4 mục cuối roadmap — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`. Spec: `docs/specs/roadmap-4-muc-cuoi.md` — đọc mục tương ứng trước mỗi task; spec là nguồn sự thật cho hành vi, plan chỉ chốt file/tên.

**Nhánh:** `feat/roadmap-4` từ `main` (`05b3f87`). **Lệnh:** `go test -count=1 -tags nouac ./...` · `node --test web/static/js/operations-console.test.js` · `go build -tags nouac -o smsie.exe .`. **Commit:** `git -c user.name=FLOWASITE -c user.email=duyphuongvo00@gmail.com commit -m "<msg>" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"`.

Mẫu có sẵn để bắt chước (đừng viết lại): config defaults trong `internal/config/config.go`; repo + test sqlite in-memory `internal/repository/*_repo_test.go`; bẫy `MAX(datetime)` trên SQLite → `id IN (SELECT MAX(id)…)`; lọc quyền `internal/api/balance_handler.go` `Status`; goroutine hằng ngày `internal/balance/scheduler.go` `nextCheck`; `captureBalance` trong `internal/worker/balance.go` (cửa sổ/bắt USSD); `UpdateProfile` RawMessage; UI helpers thuần export cuối `operations-console.js` + `node --test`.

---

### Task A: Tự đọc số thuê bao

**Files:** `internal/config/config.go`, `config.yaml.example`, `internal/model/model.go` (`PhoneNumberHistory`), `internal/repository/modem_repo.go` (`SetPhoneNumber(iccid, phone, source) error` — transaction: đọc cũ, nếu khác thì UPDATE + INSERT history; `PhoneHistory(iccid)`), `internal/worker/phone_lookup.go` (+test: `parsePhoneNumber`, `lookupCodeFor(operator, codes)`), `internal/worker/worker.go` (field `phoneLookupUntil time.Time` + mutex nhỏ; gọi sau probe khi phone rỗng & regCode 1/5 & enabled; gọi sau Observe khi có event và phone rỗng), `internal/worker/worker.go` URC handler `+CUSD:` → `w.capturePhoneNumber(line)` trước `captureBalance`; `internal/worker/worker_logic.go` nơi lưu SMS nhận → cũng gọi `capturePhoneNumber(content)`; `internal/api/modem_handler.go` (`PhoneLookup` POST, `PhoneHistory` GET; `UpdateProfile` phone_number đi qua `SetPhoneNumber(…,"manual")`), `main.go` routes, UI (nút "Đọc số" ở Hiệu chuẩn + Bảo trì, poll như `pollBalanceResult`; card Lịch sử khe "Số trước").

- [ ] Config + defaults (4 mã VN như spec; `enabled: true`).
- [ ] Model + repo + test (đổi số ghi history; cùng số không ghi; `manual` từ profile).
- [ ] `phone_lookup.go`: `parsePhoneNumber(text) (string, bool)` — regex `(?:\+?84|0)(\d{9})\b`, đầu số hợp lệ `3|5|7|8|9`; `lookupCodeFor`; `RequestPhoneNumber()`; `capturePhoneNumber(text) bool` (chỉ khi `now < phoneLookupUntil`; thành công → đóng cửa sổ, `repo.SetPhoneNumber(iccid, phone, "ussd")`, cập nhật `modem.PhoneNumber` runtime). Test parse 6 ca.
- [ ] Móc worker (3 chỗ kích hoạt + 2 chỗ bắt). `logger.Log` nil-guard nếu test worker chưa init logger.
- [ ] API + routes + test (202; 404 worker offline → 409 `modem offline`; history 200).
- [ ] UI + node test cho `describePhoneLookup(state)`; bump cache-bust `20260916-phone`.
- [ ] Commit `feat(phone): tự tra số thuê bao qua USSD, lịch sử đổi số`.

### Task B: Audit trail

**Files:** `internal/model/model.go` (`AuditLog`), `internal/audit/audit.go` (+test: `Record(db, Entry)`, `Middleware(db)`, `actionFor(method, fullPath) string`, `scrub(body []byte) string`, `Prune(db, keepDays)`), `internal/api/audit_handler.go` (+test `List`), `internal/config/config.go` (`Audit.KeepDays` default 365), `main.go` (middleware trên authGroup/adminGroup **sau** AuthMiddleware; prune lúc boot + hằng ngày cùng giờ backup hoặc goroutine 24h; route), `internal/keepalive/service.go` (Record `keepalive.send`), UI (trang Nhật ký thật + bộ lọc + CSV; xoá suy diễn từ SMS; giữ thẻ slot events? → BỎ, chỉ audit_logs), test JS `describeAudit`.

- [ ] Model + `audit.go` + test (POST ghi, GET không, password bị xoá, status, action map ≥ 8 route, unknown route fallback, prune).
- [ ] Middleware wiring: `c.Request.Body` đọc ≤4 KB rồi trả lại `io.NopCloser(bytes.NewReader(...))` cho handler; user lấy từ `c.Get("user")` / `c.Get("api_key")` (xem `getActor`). Chạy `Record` trong goroutine để không chặn.
- [ ] Handler `GET /audit` admin, filter + phân trang (`page_size` ≤ 500).
- [ ] UI Nhật ký + node test; cache-bust `20260916-audit`.
- [ ] Commit `feat(audit): nhật ký hành động thật (middleware), API, trang Nhật ký`.

### Task C: Báo cáo tháng

**Files:** `internal/report/monthly.go` (+test với sqlite seed: `Monthly(db, month time.Time, allowed []string|nil) (Report, error)`, `WriteCSV(w, Report)`), `internal/api/report_handler.go` (+test quyền & csv BOM), `main.go` route `authGroup.GET("/reports/monthly")`, UI Báo cáo (month picker, bảng, tổng, CSV; KPI/biểu đồ theo tháng), node test `formatReportRow`.

- [ ] `monthly.go` + test (seed 2 SIM, tháng có/không dữ liệu, balance start/end qua id đầu/cuối trong khoảng `[đầu tháng, đầu tháng sau)` local).
- [ ] Handler + route + test.
- [ ] UI + cache-bust `20260916-report`.
- [ ] Commit `feat(report): báo cáo tháng theo SIM, CSV`.

### Task D: Sao lưu tự động + khôi phục

**Files:** `internal/config/config.go` (`Backup{Enabled, Dir, Hour, Keep}` defaults true/"backups"/3/14), `config.yaml.example`, `internal/backup/backup.go` (+test với thư mục tạm + sqlite file thật: `Run(db, dir, keep) (Result, error)` = VACUUM INTO + integrity + prune; `Integrity(path) error`; `List(dir)`; `StagePath(dsn) string` = dsn+".restore-pending"; `ApplyPending(dsn, dir) (applied bool, err)` gọi TRƯỚC khi mở DB; `Scheduler.Run(stop)`), `internal/logic/webhook_service.go` (`Broadcast(text)`), `internal/api/admin_backup.go` (thêm `List`, `RunNow`, `Get(name)` (chống path traversal: chỉ tên file `^smsie-\d{8}-\d{6}\.db$` hoặc `pre-restore-…`), `Restore` (multipart hoặc `{name}`; validate → stage → audit → 202; nếu `os.Getenv("SMSIE_SERVICE")=="1"` → `time.AfterFunc(2s, func(){ os.Exit(3) })`)), `main.go` (ApplyPending trước initDB; scheduler; routes), `deploy/windows/smsie-service.xml` (`<env name="SMSIE_SERVICE" value="1"/>`), `deploy/windows/restore-backup.ps1` (stage + Restart-Service), `deploy/windows/README.md` (mô tả), UI Báo cáo khối Sao lưu + modal Khôi phục + poll `/ping`.

- [ ] `backup.go` + test (Run tạo file hợp lệ; prune giữ `keep`; Integrity phát hiện file rác; ApplyPending: có pending → đổi tên + tạo pre-restore; không có → false).
- [ ] Handler + routes + test (tên file bẩn → 400; restore file rác → 400; restore hợp lệ → pending tồn tại + 202).
- [ ] main.go + scheduler + deploy scripts.
- [ ] UI + cache-bust `20260916-backup`; verify Playwright: Sao lưu ngay → xuất hiện trong danh sách; Khôi phục bản vừa tạo → 202, file `smsie-dev.db.restore-pending` xuất hiện; khởi động lại server tay → log `restore.applied`, file pending biến mất, có `backups/pre-restore-*.db`.
- [ ] Commit `feat(backup): sao lưu hằng ngày + integrity + prune; khôi phục qua staging, áp lúc khởi động`.

### Task E: Docs + PR

- [ ] swagger cho mọi endpoint mới (phone-lookup, phone-history, audit, reports/monthly, admin/backups*, admin/restore); README (4 bullet) + README.zh; `docs/specs/persistent-operations.md` bỏ câu "Restore remains disabled".
- [ ] Commit `docs: 4 mục cuối roadmap`, push, PR `feat: tự đọc số thuê bao · audit trail · báo cáo tháng · sao lưu tự động + khôi phục`; no auto-merge.

## Self-review
Mỗi mục spec có task; tên xuyên task: `ModemRepository.SetPhoneNumber`, `audit.Record/Middleware`, `report.Monthly/WriteCSV`, `backup.Run/ApplyPending/StagePath`, `WebhookService.Broadcast`. Task B phải xong trước D (D ghi audit) — thứ tự A→B→C→D→E.
