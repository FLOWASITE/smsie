# Cảnh báo số dư thấp + dự báo — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use `- [ ]`.

**Goal:** Backend tự đọc `*101#` hằng ngày, lưu chuỗi số dư, tính ngày hết tiền, cảnh báo qua trang Cảnh báo + webhook khi dưới ngưỡng / sắp hết.

**Architecture:** `balance_snapshots` ghi từ `captureBalance`; package thuần `internal/balance` (Forecast + Evaluate); `balance.Scheduler` goroutine (đọc theo giờ, đánh giá, chống lặp qua `balance_alerts`, bắn `WebhookService.DispatchText`); API `/balance/status`, `/balance/alerts`, `/balance/run`; UI đọc `level`/`days_left` từ API thay vì hằng số 20000.

**Tech Stack:** Go 1.25 · Gin · GORM · viper · vanilla JS/jQuery · `node --test`. Spec: `docs/specs/canh-bao-so-du.md`.

**Lệnh:** `go test -count=1 -tags nouac ./...` · `node --test web/static/js/operations-console.test.js` · `go build -tags nouac -o smsie.exe .` — từ `D:\smsie`, nhánh `feat/canh-bao-so-du` (tạo từ `main`).

Quy ước commit: `git -c user.name=FLOWASITE -c user.email=duyphuongvo00@gmail.com commit -m "<msg>" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"`.

---

## Cấu trúc file

| File | Trách nhiệm |
|---|---|
| `internal/config/config.go`, `config.yaml.example` | `BalanceConfig{Enabled, CheckHour, LowThresholdVND, ForecastDays, USSDCode}` + default |
| `internal/model/model.go` | `Modem.LowBalanceVND *int64`; `BalanceSnapshot`; `BalanceAlert` |
| `internal/balance/forecast.go` + `_test.go` (mới) | `Point{At, VND}`, `Forecast(points) (daysLeft float64, ok bool)` |
| `internal/balance/evaluate.go` + `_test.go` (mới) | `Input`, `Result{Level, ThresholdVND, ThresholdSource, DaysLeft *float64}`, `Evaluate(in, cfg)` thuần |
| `internal/repository/balance_repo.go` + `_test.go` (mới) | `AddSnapshot`, `RecentSnapshots(iccid, n)`, `AlertedWithin(iccid, d)`, `AddAlert`, `ListAlerts(page,size)` |
| `internal/worker/balance.go` | `captureBalance` → `AddSnapshot`; `RequestBalance` dùng `cfg.USSDCode` |
| `internal/worker/manager.go` | `ActiveWorkers() []*ModemWorker` |
| `internal/logic/webhook_service.go` | `DispatchText(iccid, text string)` |
| `internal/balance/scheduler.go` (mới) | `Scheduler{db, wm, webhooks, cfg}`; `Run(stop)`, `RunOnce(ctx)` (đọc + đánh giá) |
| `internal/api/balance_handler.go` + `_test.go` (mới) | `Status`, `Alerts`, `RunNow` |
| `internal/api/modem_handler.go` + `modem_profile_test.go` | `low_balance_vnd` trong `UpdateProfile` |
| `main.go` | AutoMigrate 2 bảng; khởi động scheduler; routes |
| `web/static/js/operations-console.js` + `.test.js`, `index.html`, `operations-console.css` | `balanceStatus` state; `describeBalanceLevel`, `balanceSparkline`; alerts, tray, popover, calibration, maintenance, KPI |
| `openapi/swagger.yaml`, `README.md`, `docs/specs/canh-bao-so-du.md` | docs |

---

### Task 1: Config + model + snapshot từ captureBalance

**Files:** `internal/config/config.go`, `config.yaml.example`, `internal/model/model.go`, `main.go` (AutoMigrate), `internal/repository/balance_repo.go` (+test), `internal/worker/balance.go`, `internal/worker/manager.go`, `internal/logic/webhook_service.go`.

- [ ] Config: thêm `Balance BalanceConfig \`mapstructure:"balance"\`` với struct

```go
type BalanceConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	CheckHour       int    `mapstructure:"check_hour"`
	LowThresholdVND int64  `mapstructure:"low_threshold_vnd"`
	ForecastDays    int    `mapstructure:"forecast_days"`
	USSDCode        string `mapstructure:"ussd_code"`
}
```
  Trong `LoadConfig` trước `ReadInConfig`: `viper.SetDefault("balance.enabled", true)`, `check_hour` 6, `low_threshold_vnd` 20000, `forecast_days` 7, `ussd_code` "*101#". Thêm khối `balance:` vào `config.yaml.example` (giống spec).
- [ ] Model: `Modem` thêm `LowBalanceVND *int64 \`gorm:"column:low_balance_vnd" json:"low_balance_vnd,omitempty"\``; thêm

```go
type BalanceSnapshot struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ICCID      string    `gorm:"column:iccid;size:32;index" json:"iccid"`
	BalanceVND int64     `gorm:"column:balance_vnd" json:"balance_vnd"`
	ReadAt     time.Time `gorm:"index" json:"read_at"`
}

const (
	BalanceAlertLow      = "low"
	BalanceAlertForecast = "forecast"
)

type BalanceAlert struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ICCID      string    `gorm:"column:iccid;size:32;index" json:"iccid"`
	Kind       string    `gorm:"size:16" json:"kind"`
	BalanceVND int64     `gorm:"column:balance_vnd" json:"balance_vnd"`
	DaysLeft   *float64  `gorm:"column:days_left" json:"days_left,omitempty"`
	SentAt     time.Time `gorm:"index" json:"sent_at"`
}
```
  AutoMigrate thêm `&model.BalanceSnapshot{}, &model.BalanceAlert{}`.
- [ ] `internal/repository/balance_repo.go`:

```go
type BalanceRepository struct{ db *gorm.DB }
func NewBalanceRepository(db *gorm.DB) *BalanceRepository
func (r *BalanceRepository) AddSnapshot(iccid string, vnd int64, at time.Time) error
func (r *BalanceRepository) RecentSnapshots(iccid string, n int) ([]model.BalanceSnapshot, error) // ORDER BY read_at DESC, id DESC LIMIT n, trả về đã đảo lại tăng dần theo thời gian
func (r *BalanceRepository) AlertedWithin(iccid string, d time.Duration) (bool, error)
func (r *BalanceRepository) AddAlert(a *model.BalanceAlert) error
func (r *BalanceRepository) ListAlerts(page, size int) ([]model.BalanceAlert, int64, error) // sent_at DESC
```
  Test (in-memory sqlite như `bay_repo_test.go`): RecentSnapshots trả đúng thứ tự tăng dần và giới hạn n; AlertedWithin true/false theo thời gian.
- [ ] `worker/balance.go`: `captureBalance` sau `UpdateBalance` gọi `repository.NewBalanceRepository(db)`… — worker không giữ `db`; thêm field `balanceRepo *repository.BalanceRepository` vào `ModemWorker`, khởi tạo trong `NewModemWorker`, gọi `AddSnapshot(iccid, value, updatedAt)` (warn on error). `RequestBalance` dùng `config.AppConfig.Balance.USSDCode` (fallback `*101#` nếu rỗng).
- [ ] `worker/manager.go`: 

```go
// ActiveWorkers trả snapshot các worker chưa dừng (không giữ lock khi caller dùng).
func (m *Manager) ActiveWorkers() []*ModemWorker {
	m.mu.RLock(); defer m.mu.RUnlock()
	out := make([]*ModemWorker, 0, len(m.workers))
	for _, w := range m.workers { if !w.IsStopped() { out = append(out, w) } }
	return out
}
```
- [ ] `logic/webhook_service.go`: 

```go
// DispatchText gửi một thông điệp tự do tới mọi webhook của ICCID (bỏ qua template, payload cùng định dạng).
func (s *WebhookService) DispatchText(iccid, text string) {
	webhooks, err := s.repo.FindByICCID(iccid)
	if err != nil { logger.Log.Errorf(...); return }
	for _, wh := range webhooks { go s.sendWebhook(wh, &model.SMS{ICCID: iccid, Content: text, Timestamp: time.Now(), Type: "alert"}) }
}
```
  `sendWebhook` giữ nguyên nhưng **bỏ qua template khi `sms.Type == "alert"`** (template SMS dùng `{{.Phone}}` sẽ ra chuỗi rỗng vô nghĩa).
- [ ] Build + test xanh. Commit `feat(balance): config, snapshot số dư, ActiveWorkers, DispatchText`.

---

### Task 2: `internal/balance` thuần — Forecast + Evaluate

**Files:** `internal/balance/forecast.go`, `forecast_test.go`, `evaluate.go`, `evaluate_test.go`.

- [ ] Test Forecast trước:

```go
func pts(vals ...int64) []Point { // mỗi mốc cách nhau 1 ngày, bắt đầu 2026-09-01
	base := time.Date(2026, 9, 1, 6, 0, 0, 0, time.Local)
	out := make([]Point, len(vals))
	for i, v := range vals { out[i] = Point{At: base.AddDate(0, 0, i), VND: v} }
	return out
}
TestForecastNeedsTwoPointsSpanningOneDay: pts(50000) → ok=false; hai mốc cách 1 giờ → ok=false
TestForecastIgnoresRisingBalance: pts(10000, 20000, 30000) → ok=false
TestForecastLinearDecline: pts(70000, 60000, 50000) → ok=true, daysLeft ≈ 5 (±0.01)
TestForecastUsesLastSevenPoints: 10 mốc, 3 đầu tăng mạnh rồi 7 cuối giảm 10k/ngày → daysLeft tính theo 7 cuối
TestForecastAfterTopUpStillDeclining: pts(30000, 20000, 80000, 70000, 60000) → ok=true (slope tổng thể có thể dương → ok=false; CHỌN: dùng 7 mốc cuối nhưng nếu có mốc tăng, cắt chuỗi từ mốc tăng cuối cùng trở đi: [80000,70000,60000] → 6 ngày). Ghi rõ trong code: "đoạn giảm gần nhất".
```

- [ ] `forecast.go`:

```go
package balance

type Point struct { At time.Time; VND int64 }

// Forecast hồi quy tuyến tính VND ~ giờ trên đoạn giảm gần nhất của ≤7 mốc cuối.
// ok=false khi <2 mốc, trải <24h, hoặc slope ≥ 0. daysLeft = VND_cuối / (-slope_theo_ngày).
func Forecast(points []Point) (daysLeft float64, ok bool)
```
  Thuật toán: sort theo At; lấy 7 cuối; tìm chỉ số i lớn nhất sao cho VND[i] > VND[i-1] (tăng) → dùng [i:]; cần ≥2 mốc và At[last]-At[first] ≥ 24h; slope = cov(t,v)/var(t) với t tính bằng ngày (float64); ok = slope < 0; daysLeft = float64(VND_last)/(-slope); nếu daysLeft < 0 → 0.
- [ ] Test Evaluate trước:

```go
cfg := Config{LowThresholdVND: 20000, ForecastDays: 7}
TestEvaluateUnknownWithoutBalance: Input{HasBalance:false} → Level "unknown"
TestEvaluateSimThresholdOverridesConfig: Input{HasBalance:true, VND: 25000, SimThreshold: ptr(30000)} → "low", ThresholdSource "sim", ThresholdVND 30000
TestEvaluateForecastWithinDays: VND 100000, Points giảm 20k/ngày → days 5 → "forecast", DaysLeft≈5
TestEvaluateOK: VND 100000, Points tăng → "ok", DaysLeft nil
TestEvaluateLowBeatsForecast: cả hai điều kiện → "low" (DaysLeft vẫn điền)
```

- [ ] `evaluate.go`:

```go
type Config struct { LowThresholdVND int64; ForecastDays int }
type Input struct { HasBalance bool; VND int64; SimThreshold *int64; Points []Point }
type Result struct { Level string; ThresholdVND int64; ThresholdSource string; DaysLeft *float64 }
const (LevelUnknown="unknown"; LevelOK="ok"; LevelLow="low"; LevelForecast="forecast")
func Evaluate(in Input, cfg Config) Result
```
- [ ] Test xanh, commit `feat(balance): Forecast + Evaluate thuần`.

---

### Task 3: Scheduler + API + profile + main

**Files:** `internal/balance/scheduler.go` (+ `scheduler_test.go` cho phần đánh giá với DB in-memory), `internal/api/balance_handler.go` (+test), `internal/api/modem_handler.go`, `internal/api/modem_profile_test.go`, `main.go`.

- [ ] `scheduler.go`:

```go
type Scheduler struct {
	db       *gorm.DB
	wm       *worker.Manager
	webhooks *logic.WebhookService
	cfg      config.BalanceConfig
	repo     *repository.BalanceRepository
	runMu    sync.Mutex
}
func NewScheduler(db *gorm.DB, wm *worker.Manager, webhooks *logic.WebhookService, cfg config.BalanceConfig) *Scheduler
// Run: vòng lặp tới khi stop đóng. Sau 2 phút kể từ khởi động → Evaluate(). Mỗi ngày tại cfg.CheckHour:00 địa phương → ReadAll() rồi 15 phút sau Evaluate(). Nếu !cfg.Enabled thì chỉ Evaluate hằng ngày (không ReadAll).
func (s *Scheduler) Run(stop <-chan struct{})
// ReadAll: với mỗi wm.ActiveWorkers(): rt,ok := w.RuntimeModemState(); bỏ qua nếu !ok, rt.Status!="online", rt.Registration không phải registered (xem giá trị thực trong worker: grep "Registration =" để lấy chuỗi), hoặc modems.balance_updated_at trong 20h qua. Gọi w.RequestBalance() (bỏ qua ErrBalanceCheckInProgress), sleep 3s giữa các SIM. Trả số SIM đã yêu cầu.
func (s *Scheduler) ReadAll() int
// Evaluate: với mỗi modem trong DB: build Input từ modem.BalanceUpdatedAt!=nil, BalanceVND, LowBalanceVND, RecentSnapshots(iccid,7); r := balance.Evaluate; nếu r.Level ∈ {low, forecast} && !AlertedWithin(iccid, 24h) → AddAlert + webhooks.DispatchText(iccid, text). Trả []StatusItem để API dùng chung.
func (s *Scheduler) Evaluate() ([]StatusItem, error)
type StatusItem struct { ICCID, PhoneNumber string; SlotNumber *int; BalanceVND int64; BalanceUpdatedAt *time.Time; ThresholdVND int64; ThresholdSource string; DaysLeft *float64; Level string; Snapshots []model.BalanceSnapshot }
// Status: như Evaluate nhưng KHÔNG ghi alert/webhook (API gọi).
func (s *Scheduler) Status() ([]StatusItem, error)
```
  Text webhook: `⚠️ SIM {phone hoặc iccid}{" (khe N)" nếu có}: số dư {vnd} đ, dưới ngưỡng {thr} đ` / `⏳ SIM …: số dư {vnd} đ, dự kiến hết tiền sau ≈{days:.0f} ngày`. Định dạng VND bằng dấu chấm nghìn (viết helper nhỏ `fmtVND`).
  Test `scheduler_test.go` (sqlite in-memory, `wm := worker.NewManager(db)`, webhooks với repo rỗng): modem A VND 10000 updated now → Evaluate lần 1 tạo 1 alert kind low; lần 2 ngay sau → không tạo thêm; modem B không có balance → level unknown, không alert.
- [ ] `balance_handler.go`: `NewBalanceHandler(db, sched)`; `Status` (GET; lọc: admin thấy hết, user thường chỉ ICCID được `allowedICCIDsForPermission(db, user, PermViewSMS)` — mirror `BayHandler.List`), `Alerts` (admin; page/page_size như slot-events), `RunNow` (admin; `go func(){ sched.ReadAll(); time.Sleep(20*time.Second); sched.Evaluate() }()` — trả 202 `{requested: n}` với n từ ReadAll chạy đồng bộ, rồi Evaluate chạy nền sau 20s). Test: Status lọc theo quyền; Alerts 403 cho non-admin.
- [ ] `UpdateProfile`: nhận `LowBalanceVND *int64` với `json:"low_balance_vnd"`; để phân biệt "null = về mặc định" và "không gửi", đọc raw JSON: bind vào `map[string]json.RawMessage` trước, nếu có key `low_balance_vnd` → nếu `null` → `updates["low_balance_vnd"] = nil`, else parse int64, <0 → 400. Test: null → NULL; 15000 → 15000; -1 → 400.
- [ ] `main.go`: `webhookSvc := logic.NewWebhookService(repository.NewWebhookRepository(db))` (nếu chưa có instance dùng chung — worker tự tạo riêng; tạo một cái mới cho scheduler là đủ); `sched := balance.NewScheduler(db, wm, webhookSvc, config.AppConfig.Balance)`; `go sched.Run(schedStop)` với `schedStop` đóng ở defer; routes: `authGroup.GET("/balance/status", balh.Status)`, `adminGroup.GET("/balance/alerts", balh.Alerts)`, `adminGroup.POST("/balance/run", balh.RunNow)`.
- [ ] Build, test xanh. Commit `feat(balance): scheduler đọc *101# hằng ngày, đánh giá + webhook; API status/alerts/run; ngưỡng per-SIM`.

---

### Task 4: UI

**Files:** `web/static/js/operations-console.js` (+test), `web/templates/index.html`, `web/static/css/operations-console.css`.

- [ ] Helpers thuần + test:

```js
function describeBalanceLevel(item) → { tone: 'danger'|'warning'|'ok'|'muted', label }
  low → {tone:'danger', label:`Số dư ${opsMoney(vnd)} dưới ngưỡng ${opsMoney(thr)}`}
  forecast → {tone:'warning', label:`Dự kiến hết tiền sau ≈${Math.round(days)} ngày`}
  ok → {tone:'ok', label: days!=null ? `Còn ≈${Math.round(days)} ngày` : 'Số dư ổn'}
  unknown → {tone:'muted', label:'Chưa đọc số dư'}
function balanceSparkline(snapshots) → 'chuỗi "52k → 48k → 41k"' (≤7, k = Math.round(vnd/1000)); rỗng → ''
```
  Test: 4 level; sparkline 3 mốc; sparkline rỗng.
- [ ] `opsState.balanceStatus = []` + map `opsBalanceByICCID()`; `loadOperationsData` thêm `$.get('/api/v1/balance/status')` (4 GET).
- [ ] `opsAlerts()`: với mỗi item level low/forecast → push `{level: tone→'danger'|'warning', title: \`Khe ${slot ?? '—'} · ${phone||iccid}\`, note: label}`. Trang Cảnh báo (`#view-alerts` header) thêm nút admin `#btn-balance-run` "Đọc số dư ngay" → POST `/balance/run` → toast text "Đã yêu cầu đọc N SIM, đánh giá sau 20 giây" rồi `setTimeout(loadOperationsData, 25000)`.
- [ ] Tray `bayCard`: bỏ hằng `20000`; `low` class khi `level === 'low'`, thêm class `forecast` (viền cam nhạt) khi `forecast`; popover thêm dòng "Dự kiến hết" = label.
- [ ] Hiệu chuẩn: thêm cột "Ngưỡng" sau "Số thuê bao": admin → `<input type="number" min="0" step="1000" placeholder="mặc định">` giá trị `low_balance_vnd ?? ''`; change → PATCH profile `{low_balance_vnd: value === '' ? null : Number(value)}`; non-admin → text `${thr} đ (mặc định|riêng)`.
- [ ] Bảo trì: dòng số dư thêm ` · ${describeBalanceLevel(item).label}` và dòng mới `balanceSparkline(item.snapshots)` (class `ops-list-note mono`).
- [ ] KPI tổng quan: đổi ô "Cảnh báo" note thành `${n} SIM sắp hết tiền` với n = số item low|forecast.
- [ ] CSS: `.bay.forecast { border-color:#f3cf8a; }`, `.alert-card .alert-note-mono`.
- [ ] Verify bằng Playwright như các task trước (server :8090, seed `balance_snapshots` giảm dần cho 1 SIM, set `low_balance_vnd` cho SIM khác; chụp `.playwright-mcp/balance-alerts.png`, `balance-cal.png`). Commit `feat(balance-ui): cảnh báo số dư, dự báo, ngưỡng per-SIM, sparkline`.

---

### Task 5: Docs + PR

- [ ] swagger: `/balance/status`, `/balance/alerts`, `/balance/run`, `low_balance_vnd` trong profile; schemas `BalanceStatusItem`, `BalanceAlert`, `BalanceSnapshot`.
- [ ] README: mục "Balance alerts" (config keys, 1 USSD/ngày/SIM, webhook 1 lần/ngày). `config.yaml.example` đã có khối balance từ Task 1.
- [ ] Commit `docs: cảnh báo số dư`, push, PR `feat: cảnh báo số dư thấp + dự báo ngày hết tiền` (body tiếng Việt, link spec/plan, kết thúc bằng `🤖 Generated with [Claude Code](https://claude.com/claude-code)`), không auto-merge.

## Self-review
- Spec ↔ plan: config ✓(T1) · model ✓(T1) · snapshot từ captureBalance ✓(T1) · lịch đọc + đánh giá + chống lặp + webhook ✓(T3) · API 4 endpoint ✓(T3) · UI 5 mục ✓(T4) · kiểm thử Forecast/Evaluate/API/node ✓.
- Tên dùng xuyên task: `BalanceRepository{AddSnapshot, RecentSnapshots, AlertedWithin, AddAlert, ListAlerts}`, `balance.Forecast/Evaluate/Config/Input/Result/Point`, `Scheduler{Run, ReadAll, Evaluate, Status}`, `StatusItem` JSON snake_case (`threshold_vnd, threshold_source, days_left, level, snapshots`), `WebhookService.DispatchText`, `Manager.ActiveWorkers`.
