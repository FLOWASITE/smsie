# Lịch sử đảo SIM — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Khe = modem (IMEI), SIM (ICCID) đi qua khe; mỗi lần probe modem tự phát hiện SIM đổi khe, ghi `sim_slot_events` kèm số dư `*101#`, và UI hiện bản đồ khay 8×4 + lịch sử khe + hiệu chuẩn.

**Architecture:** Hai bảng mới `modem_bays` (khe ↔ IMEI, SIM hiện tại) và `sim_slot_events` (append-only). Một hàm thuần `DecideSlotEvents` trong `internal/slotlog` quyết định inserted/moved/removed từ trạng thái bays; repository ghi trong một transaction; worker gọi sau bước "Register in DB" rồi `RequestBalance()`; `captureBalance` điền `balance_vnd` cho event mới nhất còn thiếu. API mới `/bays`, `/slot-events`; JS thay `renderMiniSlots`/`renderSlotGrid` bằng khay 8×4 đọc từ `/bays`, thêm tab lịch sử khe và hiệu chuẩn.

**Tech Stack:** Go 1.25 · Gin · GORM (SQLite/MySQL) · vanilla JS + jQuery · `node --test`. Spec: `docs/specs/sim-slot-history.md`. Mockup: `docs/mockups/sim-slot-history.html`.

**Lệnh chuẩn (chạy từ gốc repo `D:\smsie`, nhánh `feat/sim-slot-history`):**
- Go test: `go test -tags nouac ./...`
- JS test: `node --test web/static/js/operations-console.test.js`
- Build: `go build -tags nouac -o smsie.exe .`

---

## Cấu trúc file

| File | Trách nhiệm |
|---|---|
| `internal/model/model.go` | thêm struct `ModemBay`, `SimSlotEvent` |
| `internal/slotlog/decide.go` (mới) | hàm thuần `DecideSlotEvents` — logic inserted/moved/removed, không đụng DB |
| `internal/slotlog/decide_test.go` (mới) | bảng trường hợp |
| `internal/repository/bay_repo.go` (mới) | `BayRepository`: `Observe` (transaction: quyết định + ghi event + cập nhật bay + `modems.slot_number`), `MarkEmpty`, `AssignSlot`, `List`, `FillBalance`, `ListEvents`, `MigrateFromModems` |
| `internal/repository/bay_repo_test.go` (mới) | test Observe, MigrateFromModems, FillBalance |
| `internal/worker/worker.go` | gọi `bayRepo.Observe` sau "Register in DB"; gọi `MarkEmpty` khi không đọc được ICCID |
| `internal/worker/balance.go` | `captureBalance` gọi thêm `bayRepo.FillBalance` |
| `internal/api/bay_handler.go` (mới) | `GET /bays`, `PATCH /bays/:imei`, `GET /slot-events` |
| `internal/api/bay_handler_test.go` (mới) | test 3 endpoint |
| `internal/api/modem_handler.go` | bỏ `slot_number` khỏi `UpdateProfile` |
| `internal/api/modem_profile_test.go` | cập nhật test |
| `main.go` | AutoMigrate 2 bảng + `MigrateFromModems`; đăng ký route |
| `web/static/js/operations-console.js` | `renderTray`, `renderBayCalibration`, `renderSlotHistory`; bỏ `previewMappings` localStorage |
| `web/static/js/operations-console.test.js` | test `buildTrayCells`, `groupSlotEventsByDay` |
| `web/templates/index.html` | view `slots` có 3 tab; mini-slot ở overview thành tray thu nhỏ |
| `web/static/css/operations-console.css` | style `.tray`, `.bay`, `.timeline`, `.cal-*` lấy từ mockup |
| `openapi/swagger.yaml` | 3 endpoint mới, bỏ `slot_number` khỏi profile |
| `README.md` | mục "SIM slot history" |

---

### Task 1: Model + migration

**Files:**
- Modify: `internal/model/model.go` (cuối file)
- Modify: `main.go:260` (`autoMigrateSchema`)

- [ ] **Step 1: Thêm hai struct vào cuối `internal/model/model.go`**

```go
// ModemBay là một khe vật lý trong khay: khe = modem (IMEI) cố định, SIM (ICCID) đi qua khe.
type ModemBay struct {
	IMEI         string     `gorm:"primaryKey;column:imei;size:32" json:"imei"`
	SlotNumber   *int       `gorm:"column:slot_number;uniqueIndex" json:"slot_number,omitempty"` // NULL = modem đã thấy nhưng chưa gán khe
	CurrentICCID string     `gorm:"column:current_iccid;size:32;index" json:"current_iccid,omitempty"` // "" = khe trống
	LastSeenAt   *time.Time `gorm:"column:last_seen_at" json:"last_seen_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

const (
	SlotEventInserted = "inserted"
	SlotEventMoved    = "moved"
	SlotEventRemoved  = "removed"
)

// SimSlotEvent là lịch sử append-only: SIM nào vào/rời/đổi khe lúc nào, số dư bao nhiêu.
type SimSlotEvent struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	DetectedAt  time.Time `gorm:"index" json:"detected_at"`
	ICCID       string    `gorm:"column:iccid;size:32;index" json:"iccid"`
	IMEI        string    `gorm:"column:imei;size:32" json:"imei"`
	PhoneNumber string    `gorm:"size:20" json:"phone_number,omitempty"`
	Operator    string    `gorm:"size:64" json:"operator,omitempty"`
	Event       string    `gorm:"size:16;index" json:"event"`
	FromSlot    *int      `gorm:"column:from_slot" json:"from_slot,omitempty"`
	ToSlot      *int      `gorm:"column:to_slot" json:"to_slot,omitempty"`
	BalanceVND  *int64    `gorm:"column:balance_vnd" json:"balance_vnd,omitempty"`
	PortName    string    `gorm:"size:32" json:"port_name,omitempty"`
}
```

- [ ] **Step 2: Đưa vào AutoMigrate ở `main.go`**

Thay dòng `return db.AutoMigrate(...)` trong `autoMigrateSchema` bằng:

```go
	return db.AutoMigrate(&model.User{}, &model.Modem{}, &model.SMS{}, &model.CallRecording{}, &model.Webhook{}, &model.UserModemPermission{}, &model.APIKey{}, &model.ModemBay{}, &model.SimSlotEvent{})
```

- [ ] **Step 3: Build + test hiện có vẫn xanh**

Run: `go build -tags nouac -o smsie.exe . && go test -tags nouac ./...`
Expected: `ok` cho mọi package.

- [ ] **Step 4: Commit**

```bash
git add internal/model/model.go main.go
git commit -m "feat(slot): model ModemBay + SimSlotEvent"
```

---

### Task 2: Hàm thuần `DecideSlotEvents`

**Files:**
- Create: `internal/slotlog/decide.go`
- Create: `internal/slotlog/decide_test.go`

- [ ] **Step 1: Viết test trước**

```go
package slotlog

import (
	"testing"

	"github.com/pccr10001/smsie/internal/model"
)

func slot(n int) *int { return &n }

func bays(list ...model.ModemBay) []model.ModemBay { return list }

func TestDecideUnknownModemInsertsWithoutSlot(t *testing.T) {
	d := DecideSlotEvents(bays(), "IMEI-A", "ICCID-1")
	if d.Bay.IMEI != "IMEI-A" || d.Bay.SlotNumber != nil || d.Bay.CurrentICCID != "ICCID-1" {
		t.Fatalf("bay = %+v", d.Bay)
	}
	if len(d.Events) != 1 || d.Events[0].Event != model.SlotEventInserted || d.Events[0].ToSlot != nil {
		t.Fatalf("events = %+v", d.Events)
	}
	if d.VacatedBay != nil {
		t.Fatalf("no bay should be vacated")
	}
}

func TestDecideSameSimNoEvent(t *testing.T) {
	d := DecideSlotEvents(bays(model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15), CurrentICCID: "ICCID-1"}), "IMEI-A", "ICCID-1")
	if len(d.Events) != 0 {
		t.Fatalf("expected no events, got %+v", d.Events)
	}
}

func TestDecideInsertedIntoEmptyBay(t *testing.T) {
	d := DecideSlotEvents(bays(model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15)}), "IMEI-A", "ICCID-1")
	if len(d.Events) != 1 || d.Events[0].Event != model.SlotEventInserted || *d.Events[0].ToSlot != 15 {
		t.Fatalf("events = %+v", d.Events)
	}
}

func TestDecideMovedBetweenBays(t *testing.T) {
	d := DecideSlotEvents(bays(
		model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15), CurrentICCID: "ICCID-1"},
		model.ModemBay{IMEI: "IMEI-B", SlotNumber: slot(16)},
	), "IMEI-B", "ICCID-1")
	if len(d.Events) != 1 {
		t.Fatalf("events = %+v", d.Events)
	}
	e := d.Events[0]
	if e.Event != model.SlotEventMoved || *e.FromSlot != 15 || *e.ToSlot != 16 || e.ICCID != "ICCID-1" {
		t.Fatalf("event = %+v", e)
	}
	if d.VacatedBay == nil || d.VacatedBay.IMEI != "IMEI-A" || d.VacatedBay.CurrentICCID != "" {
		t.Fatalf("vacated = %+v", d.VacatedBay)
	}
}

func TestDecideSwapEmitsRemovedForPreviousOccupant(t *testing.T) {
	// Khe 16 đang có ICCID-2, nay thấy ICCID-1 (đang ghi ở khe 15) → moved 15→16 + removed ICCID-2 khỏi 16.
	d := DecideSlotEvents(bays(
		model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15), CurrentICCID: "ICCID-1"},
		model.ModemBay{IMEI: "IMEI-B", SlotNumber: slot(16), CurrentICCID: "ICCID-2"},
	), "IMEI-B", "ICCID-1")
	if len(d.Events) != 2 {
		t.Fatalf("events = %+v", d.Events)
	}
	if d.Events[0].Event != model.SlotEventRemoved || d.Events[0].ICCID != "ICCID-2" || *d.Events[0].FromSlot != 16 || d.Events[0].ToSlot != nil {
		t.Fatalf("removed = %+v", d.Events[0])
	}
	if d.Events[1].Event != model.SlotEventMoved || d.Events[1].ICCID != "ICCID-1" {
		t.Fatalf("moved = %+v", d.Events[1])
	}
	if d.Bay.CurrentICCID != "ICCID-1" {
		t.Fatalf("bay = %+v", d.Bay)
	}
}
```

- [ ] **Step 2: Chạy để thấy fail**

Run: `go test -tags nouac ./internal/slotlog/`
Expected: FAIL — `undefined: DecideSlotEvents`.

- [ ] **Step 3: Viết `internal/slotlog/decide.go`**

```go
// Package slotlog quyết định sự kiện đổi khe của SIM từ trạng thái các khe.
// Thuần: không DB, không thời gian — caller điền DetectedAt/PhoneNumber/Operator/PortName.
package slotlog

import "github.com/pccr10001/smsie/internal/model"

type Decision struct {
	Bay        model.ModemBay   // trạng thái mới của khe có IMEI này (caller upsert)
	VacatedBay *model.ModemBay  // khe cũ của ICCID (đã xoá CurrentICCID), nil nếu không có
	Events     []model.SimSlotEvent
}

// DecideSlotEvents nhận toàn bộ bays hiện có, IMEI và ICCID vừa probe được.
func DecideSlotEvents(bays []model.ModemBay, imei, iccid string) Decision {
	var bay *model.ModemBay
	var old *model.ModemBay
	for i := range bays {
		if bays[i].IMEI == imei {
			bay = &bays[i]
		} else if bays[i].CurrentICCID == iccid {
			old = &bays[i]
		}
	}

	if bay == nil {
		b := model.ModemBay{IMEI: imei, CurrentICCID: iccid}
		return Decision{Bay: b, Events: []model.SimSlotEvent{{ICCID: iccid, IMEI: imei, Event: model.SlotEventInserted}}}
	}
	if bay.CurrentICCID == iccid {
		return Decision{Bay: *bay}
	}

	d := Decision{Bay: *bay}
	if bay.CurrentICCID != "" {
		d.Events = append(d.Events, model.SimSlotEvent{ICCID: bay.CurrentICCID, IMEI: imei, Event: model.SlotEventRemoved, FromSlot: bay.SlotNumber})
	}
	if old != nil {
		vacated := *old
		vacated.CurrentICCID = ""
		d.VacatedBay = &vacated
		d.Events = append(d.Events, model.SimSlotEvent{ICCID: iccid, IMEI: imei, Event: model.SlotEventMoved, FromSlot: old.SlotNumber, ToSlot: bay.SlotNumber})
	} else {
		d.Events = append(d.Events, model.SimSlotEvent{ICCID: iccid, IMEI: imei, Event: model.SlotEventInserted, ToSlot: bay.SlotNumber})
	}
	d.Bay.CurrentICCID = iccid
	return d
}
```

- [ ] **Step 4: Test xanh**

Run: `go test -tags nouac ./internal/slotlog/ -v`
Expected: 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slotlog
git commit -m "feat(slot): DecideSlotEvents — inserted/moved/removed thuần"
```

---

### Task 3: `BayRepository`

**Files:**
- Create: `internal/repository/bay_repo.go`
- Create: `internal/repository/bay_repo_test.go`

- [ ] **Step 1: Test trước**

```go
package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func newBayTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.SimSlotEvent{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func intp(n int) *int { return &n }

func TestMigrateFromModemsCopiesSlotToBayOnce(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.Modem{ICCID: "ICCID-1", IMEI: "IMEI-A", SlotNumber: intp(15)})
	db.Create(&model.Modem{ICCID: "ICCID-2", IMEI: "IMEI-A", SlotNumber: intp(16)}) // trùng IMEI → bỏ qua
	db.Create(&model.Modem{ICCID: "ICCID-3", IMEI: "", SlotNumber: intp(17)})       // không IMEI → bỏ qua
	repo := NewBayRepository(db)
	if err := repo.MigrateFromModems(); err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateFromModems(); err != nil { // idempotent
		t.Fatal(err)
	}
	var bays []model.ModemBay
	db.Find(&bays)
	if len(bays) != 1 || bays[0].IMEI != "IMEI-A" || *bays[0].SlotNumber != 15 || bays[0].CurrentICCID != "ICCID-1" {
		t.Fatalf("bays = %+v", bays)
	}
	var n int64
	db.Model(&model.SimSlotEvent{}).Count(&n)
	if n != 0 {
		t.Fatalf("migration must not write events, got %d", n)
	}
}

func TestObserveWritesEventsAndSyncsSlotCache(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.Modem{ICCID: "ICCID-1", IMEI: "IMEI-A", PhoneNumber: "0987654321"})
	db.Create(&model.Modem{ICCID: "ICCID-2", IMEI: "IMEI-B"})
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intp(15), CurrentICCID: "ICCID-1"})
	db.Create(&model.ModemBay{IMEI: "IMEI-B", SlotNumber: intp(16), CurrentICCID: "ICCID-2"})
	repo := NewBayRepository(db)

	events, err := repo.Observe(Observation{IMEI: "IMEI-B", ICCID: "ICCID-1", Operator: "Viettel", PortName: "COM22", At: time.Date(2026, 9, 15, 14, 22, 8, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Event != model.SlotEventMoved || events[1].PhoneNumber != "0987654321" || events[1].Operator != "Viettel" || events[1].PortName != "COM22" {
		t.Fatalf("events = %+v", events)
	}
	var m model.Modem
	db.First(&m, "iccid = ?", "ICCID-1")
	if m.SlotNumber == nil || *m.SlotNumber != 16 {
		t.Fatalf("modems.slot_number cache = %v", m.SlotNumber)
	}
	var a, b model.ModemBay
	db.First(&a, "imei = ?", "IMEI-A")
	db.First(&b, "imei = ?", "IMEI-B")
	if a.CurrentICCID != "" || b.CurrentICCID != "ICCID-1" {
		t.Fatalf("bays a=%+v b=%+v", a, b)
	}
	var stored int64
	db.Model(&model.SimSlotEvent{}).Count(&stored)
	if stored != 2 {
		t.Fatalf("stored events = %d", stored)
	}
}

func TestFillBalanceOnlyRecentEventMissingBalance(t *testing.T) {
	db := newBayTestDB(t)
	old := time.Now().Add(-10 * time.Minute)
	recent := time.Now().Add(-1 * time.Minute)
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventMoved, DetectedAt: old})
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventInserted, DetectedAt: recent})
	repo := NewBayRepository(db)
	if err := repo.FillBalance("ICCID-1", 48500, time.Now()); err != nil {
		t.Fatal(err)
	}
	var evs []model.SimSlotEvent
	db.Order("detected_at").Find(&evs)
	if evs[0].BalanceVND != nil {
		t.Fatalf("old event must stay empty: %+v", evs[0])
	}
	if evs[1].BalanceVND == nil || *evs[1].BalanceVND != 48500 {
		t.Fatalf("recent event must be filled: %+v", evs[1])
	}
}

func TestMarkEmptyWritesRemoved(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intp(15), CurrentICCID: "ICCID-1"})
	repo := NewBayRepository(db)
	if err := repo.MarkEmpty("IMEI-A", "COM17", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkEmpty("IMEI-A", "COM17", time.Now()); err != nil { // lần 2 không ghi thêm
		t.Fatal(err)
	}
	var evs []model.SimSlotEvent
	db.Find(&evs)
	if len(evs) != 1 || evs[0].Event != model.SlotEventRemoved || *evs[0].FromSlot != 15 || evs[0].ICCID != "ICCID-1" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestAssignSlotRejectsTakenSlot(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intp(15)})
	db.Create(&model.ModemBay{IMEI: "IMEI-B"})
	repo := NewBayRepository(db)
	if err := repo.AssignSlot("IMEI-B", intp(15)); err != ErrSlotTaken {
		t.Fatalf("expected ErrSlotTaken, got %v", err)
	}
	if err := repo.AssignSlot("IMEI-B", intp(16)); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignSlot("IMEI-A", nil); err != nil {
		t.Fatal(err)
	}
	var a model.ModemBay
	db.First(&a, "imei = ?", "IMEI-A")
	if a.SlotNumber != nil {
		t.Fatalf("expected unassigned, got %v", *a.SlotNumber)
	}
}
```

- [ ] **Step 2: Chạy để thấy fail**

Run: `go test -tags nouac ./internal/repository/ -run 'Migrate|Observe|FillBalance|MarkEmpty|AssignSlot'`
Expected: FAIL — `undefined: NewBayRepository`.

- [ ] **Step 3: Viết `internal/repository/bay_repo.go`**

```go
package repository

import (
	"errors"
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/slotlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrSlotTaken = errors.New("slot already assigned to another modem")

type BayRepository struct {
	db *gorm.DB
}

func NewBayRepository(db *gorm.DB) *BayRepository { return &BayRepository{db: db} }

// Observation là kết quả probe một modem: worker gọi Observe sau khi đã lưu modems.
type Observation struct {
	IMEI     string
	ICCID    string
	Operator string
	PortName string
	At       time.Time
}

// Observe so sánh (IMEI, ICCID) với bays, ghi event nếu đổi, đồng bộ modems.slot_number. Trả events đã ghi.
func (r *BayRepository) Observe(o Observation) ([]model.SimSlotEvent, error) {
	if o.IMEI == "" || o.ICCID == "" {
		return nil, nil
	}
	var written []model.SimSlotEvent
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var bays []model.ModemBay
		if err := tx.Find(&bays).Error; err != nil {
			return err
		}
		d := slotlog.DecideSlotEvents(bays, o.IMEI, o.ICCID)
		d.Bay.LastSeenAt = &o.At
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "imei"}},
			DoUpdates: clause.AssignmentColumns([]string{"current_iccid", "last_seen_at"}),
		}).Create(&d.Bay).Error; err != nil {
			return err
		}
		if d.VacatedBay != nil {
			if err := tx.Model(&model.ModemBay{}).Where("imei = ?", d.VacatedBay.IMEI).Update("current_iccid", "").Error; err != nil {
				return err
			}
		}
		if len(d.Events) == 0 {
			return nil
		}
		var modem model.Modem
		tx.First(&modem, "iccid = ?", o.ICCID)
		for i := range d.Events {
			e := &d.Events[i]
			e.DetectedAt = o.At
			e.PortName = o.PortName
			if e.ICCID == o.ICCID {
				e.PhoneNumber = modem.PhoneNumber
				e.Operator = o.Operator
			}
		}
		if err := tx.Create(&d.Events).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Modem{}).Where("iccid = ?", o.ICCID).Update("slot_number", d.Bay.SlotNumber).Error; err != nil {
			return err
		}
		written = d.Events
		return nil
	})
	return written, err
}

// MarkEmpty ghi removed khi modem báo không có SIM; lần gọi lặp không ghi thêm.
func (r *BayRepository) MarkEmpty(imei, portName string, at time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var bay model.ModemBay
		if err := tx.First(&bay, "imei = ?", imei).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if bay.CurrentICCID == "" {
			return nil
		}
		ev := model.SimSlotEvent{DetectedAt: at, ICCID: bay.CurrentICCID, IMEI: imei, Event: model.SlotEventRemoved, FromSlot: bay.SlotNumber, PortName: portName}
		if err := tx.Create(&ev).Error; err != nil {
			return err
		}
		return tx.Model(&bay).Updates(map[string]interface{}{"current_iccid": "", "last_seen_at": at}).Error
	})
}

// FillBalance điền số dư vào event mới nhất của ICCID trong 5 phút gần nhất còn thiếu số dư.
func (r *BayRepository) FillBalance(iccid string, balance int64, at time.Time) error {
	var ev model.SimSlotEvent
	err := r.db.Where("iccid = ? AND balance_vnd IS NULL AND detected_at >= ?", iccid, at.Add(-5*time.Minute)).
		Order("detected_at DESC").First(&ev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.db.Model(&ev).Update("balance_vnd", balance).Error
}

// AssignSlot gán/bỏ gán số khe cho IMEI (hiệu chuẩn).
func (r *BayRepository) AssignSlot(imei string, slot *int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if slot != nil {
			var n int64
			tx.Model(&model.ModemBay{}).Where("slot_number = ? AND imei <> ?", *slot, imei).Count(&n)
			if n > 0 {
				return ErrSlotTaken
			}
		}
		res := tx.Model(&model.ModemBay{}).Where("imei = ?", imei).Update("slot_number", slot)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&model.Modem{}).Where("iccid = (SELECT current_iccid FROM modem_bays WHERE imei = ?)", imei).Update("slot_number", slot).Error
	})
}

func (r *BayRepository) List() ([]model.ModemBay, error) {
	var bays []model.ModemBay
	err := r.db.Order("slot_number IS NULL, slot_number").Find(&bays).Error
	return bays, err
}

type EventFilter struct {
	ICCID    string
	Slot     *int
	From, To *time.Time
	Page     int
	PageSize int
}

func (r *BayRepository) ListEvents(f EventFilter) ([]model.SimSlotEvent, int64, error) {
	q := r.db.Model(&model.SimSlotEvent{})
	if f.ICCID != "" {
		q = q.Where("iccid = ?", f.ICCID)
	}
	if f.Slot != nil {
		q = q.Where("from_slot = ? OR to_slot = ?", *f.Slot, *f.Slot)
	}
	if f.From != nil {
		q = q.Where("detected_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("detected_at < ?", *f.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	var evs []model.SimSlotEvent
	err := q.Order("detected_at DESC, id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&evs).Error
	return evs, total, err
}

// MigrateFromModems chạy một lần lúc khởi động: copy slot_number gán tay theo ICCID sang bay theo IMEI.
// Bỏ qua dòng không IMEI, trùng IMEI hoặc trùng slot; idempotent (bay đã có thì không đụng).
func (r *BayRepository) MigrateFromModems() error {
	var modems []model.Modem
	if err := r.db.Where("slot_number IS NOT NULL AND imei <> ''").Order("slot_number").Find(&modems).Error; err != nil {
		return err
	}
	seenIMEI := map[string]bool{}
	for _, m := range modems {
		if seenIMEI[m.IMEI] {
			continue
		}
		seenIMEI[m.IMEI] = true
		var n int64
		r.db.Model(&model.ModemBay{}).Where("imei = ? OR slot_number = ?", m.IMEI, *m.SlotNumber).Count(&n)
		if n > 0 {
			continue
		}
		if err := r.db.Create(&model.ModemBay{IMEI: m.IMEI, SlotNumber: m.SlotNumber, CurrentICCID: m.ICCID}).Error; err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Test xanh**

Run: `go test -tags nouac ./internal/repository/ -v -run 'Migrate|Observe|FillBalance|MarkEmpty|AssignSlot'`
Expected: 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repository/bay_repo.go internal/repository/bay_repo_test.go
git commit -m "feat(slot): BayRepository — Observe/MarkEmpty/FillBalance/AssignSlot/MigrateFromModems"
```

---

### Task 4: Móc vào worker + balance

**Files:**
- Modify: `internal/worker/worker.go:94-115` (`NewModemWorker`), `:345-348` (ICCID rỗng), `:452-459` (sau Upsert)
- Modify: `internal/worker/balance.go:35-50` (`captureBalance`)
- Modify: `main.go` (gọi `MigrateFromModems` sau `autoMigrateSchema`)

- [ ] **Step 1: Thêm field `bayRepo` vào struct `ModemWorker` và khởi tạo**

Trong struct (cạnh `repo *repository.ModemRepository`):

```go
	bayRepo        *repository.BayRepository
```

Trong `NewModemWorker`, cạnh `repo: repository.NewModemRepository(db),`:

```go
		bayRepo:         repository.NewBayRepository(db),
```

- [ ] **Step 2: Đổi thứ tự đọc IMEI lên trước ICCID và ghi removed khi không có SIM**

Hiện IMEI đọc ở bước 4 (sau ICCID). Cắt khối "4. Get IMEI" (`var imei string … }` ~13 dòng) dán lên **ngay trước** "3. Get ICCID". Rồi thay:

```go
		if iccid == "" {
			logger.Log.Errorf("[%s] Failed to get ICCID", w.PortName)
			return
		}
```

bằng:

```go
		if iccid == "" {
			logger.Log.Errorf("[%s] Failed to get ICCID", w.PortName)
			if imei != "" {
				if err := w.bayRepo.MarkEmpty(imei, w.PortName, time.Now()); err != nil {
					logger.Log.Warnf("[%s] Failed to mark bay empty: %v", w.PortName, err)
				}
			}
			return
		}
```

- [ ] **Step 3: Gọi Observe sau khi Upsert modem thành công**

Thay khối:

```go
		if err := w.repo.Upsert(persist); err != nil {
			logger.Log.Errorf("Failed to save modem %s: %v", iccid, err)
		} else {
			w.setModem(modem)
			logger.Log.Infof("Modem registered: %s (%s) Op: %s Sig: %d%%", iccid, w.PortName, operator, signal)
		}
```

bằng:

```go
		if err := w.repo.Upsert(persist); err != nil {
			logger.Log.Errorf("Failed to save modem %s: %v", iccid, err)
		} else {
			w.setModem(modem)
			logger.Log.Infof("Modem registered: %s (%s) Op: %s Sig: %d%%", iccid, w.PortName, operator, signal)
			events, err := w.bayRepo.Observe(repository.Observation{IMEI: imei, ICCID: iccid, Operator: operator, PortName: w.PortName, At: time.Now()})
			if err != nil {
				logger.Log.Warnf("[%s] Slot observe failed: %v", w.PortName, err)
			}
			for _, e := range events {
				logger.Log.Infof("[%s] Slot event %s: %s %v -> %v", w.PortName, e.Event, e.ICCID, derefInt(e.FromSlot), derefInt(e.ToSlot))
			}
			if len(events) > 0 {
				if err := w.RequestBalance(); err != nil {
					logger.Log.Warnf("[%s] Balance check after slot event failed: %v", w.PortName, err)
				}
			}
		}
```

Thêm helper cuối `worker.go`:

```go
func derefInt(p *int) interface{} {
	if p == nil {
		return "-"
	}
	return *p
}
```

- [ ] **Step 4: `captureBalance` điền số dư vào event**

Trong `internal/worker/balance.go`, sau dòng `logger.Log.Infof("[%s] Balance updated for %s: %d VND", ...)` và trước `return true`:

```go
	if err := w.bayRepo.FillBalance(iccid, value, updatedAt); err != nil {
		logger.Log.Warnf("[%s] Failed to attach balance to slot event: %v", w.PortName, err)
	}
```

- [ ] **Step 5: Kế thừa dữ liệu lúc khởi động (`main.go`)**

Ngay sau chỗ gọi `autoMigrateSchema(db)` thành công (trong hàm khởi tạo DB, trước khi tạo admin), thêm:

```go
	if err := repository.NewBayRepository(db).MigrateFromModems(); err != nil {
		logger.Log.Fatalf("Failed to migrate slot numbers to modem bays: %v", err)
	}
```

Đảm bảo `main.go` import `"github.com/pccr10001/smsie/internal/repository"`.

- [ ] **Step 6: Build + toàn bộ test**

Run: `go build -tags nouac -o smsie.exe . && go test -tags nouac ./...`
Expected: build OK, mọi package `ok`. (Test worker hiện có dùng `NewModemWorker(port, db, manager)` với DB in-memory — `bayRepo` được khởi tạo cùng nên không vỡ.)

- [ ] **Step 7: Commit**

```bash
git add internal/worker/worker.go internal/worker/balance.go main.go
git commit -m "feat(slot): worker ghi sự kiện đổi khe sau probe, kèm số dư *101#"
```

---

### Task 5: API `/bays`, `/slot-events`; bỏ `slot_number` khỏi profile

**Files:**
- Create: `internal/api/bay_handler.go`
- Create: `internal/api/bay_handler_test.go`
- Modify: `internal/api/modem_handler.go:391-472`
- Modify: `internal/api/modem_profile_test.go`
- Modify: `main.go:144-173`

- [ ] **Step 1: Test trước**

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

func newBayHandlerTest(t *testing.T) (*BayHandler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Modem{}, &model.ModemBay{}, &model.SimSlotEvent{}, &model.UserModemPermission{}); err != nil {
		t.Fatal(err)
	}
	return NewBayHandler(db, worker.NewManager(db)), db
}

func intptr(n int) *int { return &n }

func TestListBaysIncludesRuntimeAndUnassigned(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.Modem{ICCID: "ICCID-1", IMEI: "IMEI-A", PhoneNumber: "0987654321", BalanceVND: 48500})
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intptr(16), CurrentICCID: "ICCID-1"})
	db.Create(&model.ModemBay{IMEI: "IMEI-Z"})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bays", nil)
	c.Set("user", &model.User{Role: "admin", AllowedModems: "*"})
	h.List(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body []bayView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || *body[0].SlotNumber != 16 || body[0].PhoneNumber != "0987654321" || body[0].BalanceVND != 48500 || body[0].Status != "offline" {
		t.Fatalf("body = %+v", body)
	}
	if body[1].SlotNumber != nil {
		t.Fatalf("unassigned bay must be last: %+v", body[1])
	}
}

func TestAssignBaySlotConflict(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intptr(15)})
	db.Create(&model.ModemBay{IMEI: "IMEI-B"})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/bays/IMEI-B", bytes.NewBufferString(`{"slot_number":15}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "imei", Value: "IMEI-B"}}
	c.Set("user", &model.User{Role: "admin"})
	h.Assign(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListSlotEventsRespectsViewPermission(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventMoved, DetectedAt: time.Now(), FromSlot: intptr(15), ToSlot: intptr(16)})
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-2", Event: model.SlotEventInserted, DetectedAt: time.Now(), ToSlot: intptr(3)})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/slot-events?iccid=ICCID-2", nil)
	c.Set("user", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	h.ListEvents(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user without view_sms on ICCID-2 must get 403, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/slot-events?slot=16", nil)
	c.Set("user", &model.User{Role: "admin", AllowedModems: "*"})
	h.ListEvents(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data  []model.SimSlotEvent `json:"data"`
		Total int64                `json:"total"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Total != 1 || body.Data[0].ICCID != "ICCID-1" {
		t.Fatalf("body = %+v", body)
	}
}
```

- [ ] **Step 2: Chạy để thấy fail**

Run: `go test -tags nouac ./internal/api/ -run 'Bay|SlotEvents'`
Expected: FAIL — `undefined: NewBayHandler`.

- [ ] **Step 3: Viết `internal/api/bay_handler.go`**

```go
package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

type BayHandler struct {
	db   *gorm.DB
	wm   *worker.Manager
	repo *repository.BayRepository
}

func NewBayHandler(db *gorm.DB, wm *worker.Manager) *BayHandler {
	return &BayHandler{db: db, wm: wm, repo: repository.NewBayRepository(db)}
}

// bayView = khe + SIM đang ở khe + trạng thái runtime của modem.
type bayView struct {
	model.ModemBay
	PhoneNumber      string     `json:"phone_number,omitempty"`
	Operator         string     `json:"operator,omitempty"`
	BalanceVND       int64      `json:"balance_vnd"`
	BalanceUpdatedAt *time.Time `json:"balance_updated_at,omitempty"`
	PortName         string     `json:"port_name,omitempty"`
	SignalStrength   int        `json:"signal_strength"`
	Status           string     `json:"status"` // online | offline | empty
	LastEventAt      *time.Time `json:"last_event_at,omitempty"`
}

func (h *BayHandler) List(c *gin.Context) {
	bays, err := h.repo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list bays"})
		return
	}
	var modems []model.Modem
	h.db.Find(&modems)
	byICCID := map[string]model.Modem{}
	for _, m := range modems {
		byICCID[m.ICCID] = m
	}
	var lastEvents []model.SimSlotEvent
	h.db.Raw("SELECT iccid, MAX(detected_at) AS detected_at FROM sim_slot_events WHERE event <> ? GROUP BY iccid", model.SlotEventRemoved).Scan(&lastEvents)
	lastEventAt := map[string]time.Time{}
	for _, e := range lastEvents {
		lastEventAt[e.ICCID] = e.DetectedAt
	}

	out := make([]bayView, 0, len(bays))
	for _, b := range bays {
		v := bayView{ModemBay: b, Status: "empty"}
		if b.CurrentICCID != "" {
			v.Status = "offline"
			if m, ok := byICCID[b.CurrentICCID]; ok {
				v.PhoneNumber = m.PhoneNumber
				v.BalanceVND = m.BalanceVND
				v.BalanceUpdatedAt = m.BalanceUpdatedAt
			}
			if w := h.wm.GetWorkerByICCID(b.CurrentICCID); w != nil {
				if rt, ok := w.RuntimeModemState(); ok {
					v.Status = rt.Status
					v.Operator = rt.Operator
					v.PortName = rt.PortName
					v.SignalStrength = rt.SignalStrength
				}
			}
			if t, ok := lastEventAt[b.CurrentICCID]; ok {
				tt := t
				v.LastEventAt = &tt
			}
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, out)
}

func (h *BayHandler) Assign(c *gin.Context) {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}
	imei := strings.TrimSpace(c.Param("imei"))
	var req struct {
		SlotNumber *int `json:"slot_number"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid bay data"})
		return
	}
	if req.SlotNumber != nil && (*req.SlotNumber < 1 || *req.SlotNumber > 32) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slot_number must be between 1 and 32"})
		return
	}
	switch err := h.repo.AssignSlot(imei, req.SlotNumber); {
	case errors.Is(err, repository.ErrSlotTaken):
		c.JSON(http.StatusConflict, gin.H{"error": "slot already assigned to another modem"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Modem bay not found"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign slot"})
	default:
		var bay model.ModemBay
		h.db.First(&bay, "imei = ?", imei)
		c.JSON(http.StatusOK, bay)
	}
}

func (h *BayHandler) ListEvents(c *gin.Context) {
	f := repository.EventFilter{ICCID: strings.TrimSpace(c.Query("iccid"))}
	if f.ICCID != "" && !enforceICCIDPermission(c, h.db, f.ICCID, PermViewSMS) {
		return
	}
	if f.ICCID == "" {
		actor, ok := getActor(c)
		if !ok || actor.User == nil || actor.User.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required to list all slot events"})
			return
		}
	}
	if s := c.Query("slot"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slot must be a number"})
			return
		}
		f.Slot = &n
	}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if s := c.Query(name); s != "" {
			t, err := time.ParseInLocation("2006-01-02", s, time.Local)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be YYYY-MM-DD"})
				return
			}
			if name == "to" {
				t = t.Add(24 * time.Hour)
			}
			*dst = &t
		}
	}
	f.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	f.PageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if f.PageSize > 500 {
		f.PageSize = 500
	}
	evs, total, err := h.repo.ListEvents(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list slot events"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": evs, "total": total, "page": f.Page, "page_size": f.PageSize})
}
```

- [ ] **Step 4: Test xanh**

Run: `go test -tags nouac ./internal/api/ -run 'Bay|SlotEvents' -v`
Expected: 3 PASS. Nếu `TestListSlotEventsRespectsViewPermission` fail ở 403: đọc `actorCanAccessICCIDPermission` (`permissions.go:224`) — user non-admin với `AllowedModems: "ICCID-1"` không có rule `UserModemPermission` cho ICCID-2 nên phải bị từ chối; nếu hàm đó trả 404 thay vì 403 thì sửa test theo mã thật.

- [ ] **Step 5: Bỏ `slot_number` khỏi `UpdateProfile`**

Trong `internal/api/modem_handler.go`:
- Xoá dòng `SlotNumber   *int    \`json:"slot_number"\`` trong struct `req`.
- Xoá khối kiểm tra `if req.SlotNumber != nil && (*req.SlotNumber < 1 ...)`.
- Xoá khối `if req.SlotNumber != nil { var conflict int64 ... }`.
- Xoá `if req.SlotNumber != nil { updates["slot_number"] = *req.SlotNumber }`.

Trong `internal/api/modem_profile_test.go`: bỏ `"slot_number":16,` khỏi body request và bỏ assertion về `modem.SlotNumber` (nếu có ở phần dưới file — đọc hết file trước khi sửa).

- [ ] **Step 6: Đăng ký route ở `main.go`**

Sau `mh := api.NewModemHandler(...)` thêm `bh := api.NewBayHandler(db, wm)`. Trong `authGroup` (cạnh `authGroup.GET("/modems", mh.ListModems)`):

```go
			authGroup.GET("/bays", bh.List)
			authGroup.GET("/slot-events", bh.ListEvents)
```

Trong `adminGroup` (cạnh `adminGroup.PATCH("/modems/:iccid/profile", mh.UpdateProfile)`):

```go
				adminGroup.PATCH("/bays/:imei", bh.Assign)
```

- [ ] **Step 7: Build + toàn bộ test**

Run: `go build -tags nouac -o smsie.exe . && go test -tags nouac ./...`
Expected: mọi package `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/api/bay_handler.go internal/api/bay_handler_test.go internal/api/modem_handler.go internal/api/modem_profile_test.go main.go
git commit -m "feat(slot): API /bays, /slot-events; profile không còn nhận slot_number"
```

---

### Task 6: Frontend — helper thuần + test

**Files:**
- Modify: `web/static/js/operations-console.js` (thêm helper trước `module.exports`; sửa `module.exports`)
- Modify: `web/static/js/operations-console.test.js`

- [ ] **Step 1: Test trước** — thêm vào cuối `operations-console.test.js` và mở rộng dòng `require`:

```js
const { balanceNeedsRefresh, buildOpsCsv, describeOpsMessageRoute, groupOpsMessages, summarizeOpsData, buildTrayCells, groupSlotEventsByDay, describeSlotEvent } = require('./operations-console.js');

test('buildTrayCells renders exactly 32 cells, unassigned bays separately', () => {
    const now = Date.now();
    const bays = [
        { imei: 'A', slot_number: 16, current_iccid: 'I1', status: 'online', signal_strength: 71, last_event_at: new Date(now - 3600e3).toISOString() },
        { imei: 'B', slot_number: 3, current_iccid: '', status: 'empty' },
        { imei: 'C', slot_number: 9, current_iccid: 'I3', status: 'online', signal_strength: 12 },
        { imei: 'Z', slot_number: null, current_iccid: 'I9', status: 'online' }
    ];
    const { cells, unassigned } = buildTrayCells(bays, now);
    assert.equal(cells.length, 32);
    assert.equal(cells[15].tone, 'online');
    assert.equal(cells[15].swapped, true);
    assert.equal(cells[2].tone, 'empty');
    assert.equal(cells[8].tone, 'weak');
    assert.equal(cells[0].tone, 'missing');
    assert.deepEqual(unassigned.map(b => b.imei), ['Z']);
});

test('groupSlotEventsByDay keeps newest day first', () => {
    const grouped = groupSlotEventsByDay([
        { id: 1, detected_at: '2026-09-15T14:22:08+07:00', event: 'moved' },
        { id: 2, detected_at: '2026-07-21T16:05:33+07:00', event: 'inserted' },
        { id: 3, detected_at: '2026-09-15T14:21:40+07:00', event: 'removed' }
    ]);
    assert.equal(grouped.length, 2);
    assert.equal(grouped[0].day, '2026-09-15');
    assert.deepEqual(grouped[0].events.map(e => e.id), [1, 3]);
});

test('describeSlotEvent renders path and balance label', () => {
    assert.deepEqual(describeSlotEvent({ event: 'moved', from_slot: 15, to_slot: 16, balance_vnd: 48500 }),
        { path: 'Khe 15 → Khe 16', tone: 'moved', balance: '48.500 đ', balanceNote: 'số dư lúc vào khe' });
    assert.deepEqual(describeSlotEvent({ event: 'removed', from_slot: 15 }),
        { path: 'Khe 15 → rút ra', tone: 'removed', balance: '—', balanceNote: 'USSD quá hạn' });
    assert.equal(describeSlotEvent({ event: 'inserted', to_slot: null }).path, 'Chưa gán khe');
});
```

- [ ] **Step 2: Chạy để thấy fail**

Run: `node --test web/static/js/operations-console.test.js`
Expected: 3 test mới FAIL (`buildTrayCells is not a function`).

- [ ] **Step 3: Thêm helper vào `operations-console.js`** (ngay trước `if (typeof module !== 'undefined')`):

```js
const TRAY_SLOTS = 32;
const SWAP_HIGHLIGHT_MS = 24 * 60 * 60 * 1000;

function trayTone(bay) {
    if (!bay) return 'missing';
    if (!bay.current_iccid) return 'empty';
    if (bay.status !== 'online') return 'offline';
    if ((bay.signal_strength || 0) < 20) return 'weak';
    return 'online';
}

function buildTrayCells(bays, now) {
    const bySlot = {};
    const unassigned = [];
    (bays || []).forEach(bay => {
        if (bay.slot_number) bySlot[bay.slot_number] = bay; else unassigned.push(bay);
    });
    const cells = [];
    for (let slot = 1; slot <= TRAY_SLOTS; slot += 1) {
        const bay = bySlot[slot];
        const swapped = !!(bay && bay.last_event_at && (now - new Date(bay.last_event_at).getTime()) < SWAP_HIGHLIGHT_MS);
        cells.push({ slot, bay: bay || null, tone: trayTone(bay), swapped });
    }
    return { cells, unassigned };
}

function slotEventDay(event) {
    const d = new Date(event.detected_at);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function groupSlotEventsByDay(events) {
    const sorted = (events || []).slice().sort((a, b) => new Date(b.detected_at) - new Date(a.detected_at));
    const groups = [];
    sorted.forEach(event => {
        const day = slotEventDay(event);
        let group = groups[groups.length - 1];
        if (!group || group.day !== day) {
            group = { day, events: [] };
            groups.push(group);
        }
        group.events.push(event);
    });
    return groups;
}

function slotLabel(slot) {
    return slot ? `Khe ${slot}` : 'Chưa gán khe';
}

function describeSlotEvent(event) {
    let path;
    let balanceNote;
    if (event.event === 'moved') {
        path = `${slotLabel(event.from_slot)} → ${slotLabel(event.to_slot)}`;
        balanceNote = 'số dư lúc vào khe';
    } else if (event.event === 'removed') {
        path = `${slotLabel(event.from_slot)} → rút ra`;
        balanceNote = 'số dư lúc rời khe';
    } else {
        path = slotLabel(event.to_slot);
        balanceNote = 'số dư lúc vào khe';
    }
    const hasBalance = event.balance_vnd !== null && event.balance_vnd !== undefined;
    return {
        path,
        tone: event.event,
        balance: hasBalance ? opsMoney(event.balance_vnd) : '—',
        balanceNote: hasBalance ? balanceNote : 'USSD quá hạn'
    };
}
```

Kiểm tra `opsMoney` (dòng ~106) trả `"48.500 đ"` cho 48500; nếu định dạng khác thì sửa expected trong test theo hàm thật (helper có sẵn thắng).

Đổi `module.exports` thành:

```js
    module.exports = { balanceNeedsRefresh, buildOpsCsv, describeOpsMessageRoute, groupOpsMessages, summarizeOpsData, buildTrayCells, groupSlotEventsByDay, describeSlotEvent };
```

- [ ] **Step 4: Test xanh**

Run: `node --test web/static/js/operations-console.test.js`
Expected: tất cả PASS.

- [ ] **Step 5: Commit**

```bash
git add web/static/js/operations-console.js web/static/js/operations-console.test.js
git commit -m "feat(slot-ui): helper buildTrayCells/groupSlotEventsByDay/describeSlotEvent"
```

---

### Task 7: Frontend — bản đồ khay 8×4 thay `renderSlotGrid` + `renderMiniSlots`

**Files:**
- Modify: `web/templates/index.html:406-418` (view slots), `:353-405` (khối `#ops-mini-slots` ở overview)
- Modify: `web/static/css/operations-console.css` (thêm cuối file)
- Modify: `web/static/js/operations-console.js:1-11, 37-55, 123-129, 210-230, 377-470, 668-720`

- [ ] **Step 1: Template view `slots`** — thay toàn bộ `<section id="view-slots" …>…</section>` bằng:

```html
          <section id="view-slots" class="view-section ops-view d-none">
            <header class="ops-page-head">
              <div>
                <div class="eyebrow">Khe = modem · SIM đi qua khe</div>
                <h1>Khe SIM</h1>
                <p>Khay 32 modem EC20. Số khe nhận diện bằng IMEI của modem, lịch sử ghi mỗi lần SIM đổi khe.</p>
              </div>
              <div class="ops-head-actions">
                <button type="button" id="btn-export-slot-events" class="btn btn-outline-dark btn-sm"><i class="bi bi-download"></i> Xuất CSV lịch sử</button>
              </div>
            </header>
            <div class="ops-tabs" role="tablist">
              <button type="button" class="ops-tab is-active" data-slot-tab="tray" role="tab">Bản đồ khay</button>
              <button type="button" class="ops-tab" data-slot-tab="history" role="tab">Lịch sử khe</button>
              <button type="button" class="ops-tab" data-slot-tab="calibration" role="tab">Hiệu chuẩn</button>
            </div>
            <div id="slot-tab-tray" class="slot-tab">
              <div class="ops-panel" style="position:relative">
                <div class="ops-panel-head">
                  <h2>Khay trước · 8 × 4</h2>
                  <div class="tray-legend">
                    <span><i class="tone-online"></i>Trực tuyến</span>
                    <span><i class="tone-weak"></i>Sóng yếu</span>
                    <span><i class="tone-offline"></i>Ngoại tuyến</span>
                    <span><i class="tone-empty"></i>Không có SIM</span>
                    <span><i class="tone-swapped"></i>Đảo trong 24h</span>
                  </div>
                </div>
                <div id="ops-tray" class="tray" aria-live="polite"></div>
                <div id="ops-live-unassigned" class="unassigned-strip"></div>
                <div id="ops-tray-pop" class="tray-pop" hidden></div>
              </div>
            </div>
            <div id="slot-tab-history" class="slot-tab d-none">
              <div class="slot-history-split">
                <div id="ops-slot-sim-card" class="ops-panel"></div>
                <div class="ops-panel">
                  <div class="ops-panel-head">
                    <h2>Lịch sử khe</h2>
                    <div class="slot-history-filters">
                      <input type="date" id="slot-events-from" class="form-control form-control-sm" aria-label="Từ ngày" />
                      <span class="text-muted">→</span>
                      <input type="date" id="slot-events-to" class="form-control form-control-sm" aria-label="Đến ngày" />
                    </div>
                  </div>
                  <div id="ops-slot-timeline"></div>
                </div>
              </div>
            </div>
            <div id="slot-tab-calibration" class="slot-tab d-none">
              <div class="cal-note"><i class="bi bi-info-circle"></i> Khe được nhận diện bằng IMEI của modem. Chỉ cần gán một lần; khi nâng cấp, số khe cũ đã được chuyển sang IMEI đang cắm SIM đó. Đổi ở đây chỉ khi sắp xếp lại modem trong khay.</div>
              <div class="ops-panel table-responsive">
                <div class="ops-panel-head"><h2>Khe ↔ modem</h2><span id="ops-cal-summary" class="ops-list-note"></span></div>
                <table class="table ops-table align-middle mb-0">
                  <thead><tr><th>Khe</th><th>IMEI modem</th><th>SIM hiện tại</th><th>Số thuê bao</th><th>Trạng thái</th><th>Lần thấy cuối</th><th></th></tr></thead>
                  <tbody id="ops-cal-body"></tbody>
                </table>
              </div>
            </div>
          </section>
```

Ở overview: giữ nguyên `<div id="ops-mini-slots" class="mini-slot-grid">` (JS sẽ tô theo bays).

- [ ] **Step 2: CSS** — chép các rule từ mockup `docs/mockups/sim-slot-history.html` vào cuối `operations-console.css`, đổi tên đúng như template: `.ops-tabs/.ops-tab`, `.tray`, `.bay` (+ `.empty/.offline/.weak/.online/.swapped`, `.chip`, `.num`, `.phone`, `.op`, `.bal`, `.dot`, `.swap`), `.tray-legend i.tone-*`, `.tray-pop` (từ `.pop`), `.slot-history-split` (từ `.split`), `.slot-history-filters`, `.timeline` + `.ico.*` + `.day`, `.cal-note`, `.pill.*`. Thêm `--sim-gold: #d9a83c; --sim-gold-line: #b8862a;` vào `:root`. Media query `max-width:1100px` → `.tray { grid-template-columns: repeat(4, minmax(0,1fr)); }` và `.slot-history-split { grid-template-columns: 1fr; }`. Xoá rule `.slot-grid-32`, `.slot-card`, `.slot-number`, `.slot-state`, `.slot-meta`, `.hardware-path` (không còn dùng sau bước 3).

- [ ] **Step 3: JS — state + tải `/bays`**

Trong `opsState` thay `previewMappings: {}` bằng `bays: [], slotEvents: [], slotTab: 'tray',`. Xoá `OPS_PROFILE_STORAGE_KEY`, `loadOpsProfiles`, `saveOpsProfiles`, `opsMappedModems`, `opsUnassignedModems` và mọi chỗ gọi chúng (`renderSimRails` dùng `modem.slot_number` — giữ; `describeOpsMessageRoute(…, mappings, …)` nhận map — ở caller truyền `opsSlotByICCID()` định nghĩa dưới).

```js
function opsSlotByICCID() {
    const map = {};
    opsState.bays.forEach(bay => { if (bay.current_iccid && bay.slot_number) map[bay.current_iccid] = bay.slot_number; });
    return map;
}
```

Trong `loadOperationsData` thêm request thứ ba `$.get('/api/v1/bays')` và gán `opsState.bays = bayResponse[0] || [];` (nhớ `$.when` với 3 deferred trả 3 mảng đối số). Xoá vòng `opsState.modems.forEach(modem => { if (modem.slot_number) opsState.previewMappings… })` — giữ phần `phoneNumbers`.

- [ ] **Step 4: JS — `renderMiniSlots` đọc bays**

```js
function renderMiniSlots() {
    const { cells } = buildTrayCells(opsState.bays, Date.now());
    const grid = $('#ops-mini-slots').empty();
    cells.forEach(cell => {
        const item = opsElement('div', `mini-slot tone-${cell.tone}${cell.swapped ? ' is-swapped' : ''}`, String(cell.slot).padStart(2, '0'));
        item.attr('title', cell.bay ? `Khe ${cell.slot}: ${cell.bay.phone_number || cell.bay.current_iccid || 'không có SIM'}` : `Khe ${cell.slot}: chưa gán modem`);
        grid.append(item);
    });
}
```

Thêm CSS `.mini-slot.tone-online{…}` (giữ màu `.is-online` cũ), `.tone-weak` nền `#fff0d6`, `.tone-empty` nền `#fbfcfb` viền đứt, `.tone-offline` nền `#eef1f0`, `.is-swapped` viền `#e8b45a`.

- [ ] **Step 5: JS — `renderTray` thay `renderSlotGrid`; `renderLiveUnassigned` gán IMEI**

Xoá `renderSlotGrid`. Viết:

```js
function bayCard(cell) {
    const bay = cell.bay;
    const card = opsElement('button', `bay ${cell.tone}${cell.swapped ? ' swapped' : ''}`);
    card.attr({ type: 'button', 'aria-label': `Khe ${cell.slot}` });
    const body = opsElement('div');
    body.append(opsElement('div', 'num', `Khe ${String(cell.slot).padStart(2, '0')}`));
    if (!bay) {
        body.append(opsElement('div', 'phone', 'Chưa gán modem'));
    } else if (!bay.current_iccid) {
        body.append(opsElement('div', 'phone', 'Không có SIM'));
        body.append(opsElement('div', 'op', bay.port_name || `IMEI …${bay.imei.slice(-4)}`));
    } else {
        body.append(opsElement('div', 'phone', bay.phone_number || bay.current_iccid));
        body.append(opsElement('div', 'op', [bay.operator, bay.port_name].filter(Boolean).join(' · ') || '—'));
        body.append(opsElement('div', `bal${bay.balance_vnd < 20000 ? ' low' : ''}`, opsMoney(bay.balance_vnd)));
    }
    card.append(opsElement('span', 'chip'), body, opsElement('span', 'dot'));
    if (cell.swapped) card.append(opsElement('span', 'swap', '↔ 24h'));
    if (bay) card.click(() => showTrayPop(cell, card));
    return card;
}

function showTrayPop(cell, card) {
    const bay = cell.bay;
    const pop = $('#ops-tray-pop').empty();
    const panel = card.closest('.ops-panel');
    const r = card[0].getBoundingClientRect();
    const p = panel[0].getBoundingClientRect();
    pop.css({ left: Math.min(r.left - p.left, p.width - 280) + 'px', top: (r.bottom - p.top + 6) + 'px' });
    pop.append(opsElement('b', '', `Khe ${cell.slot}${bay.phone_number ? ' · ' + bay.phone_number : ''}`));
    const dl = $('<dl>');
    const rows = bay.current_iccid
        ? [['ICCID', bay.current_iccid], ['IMEI', bay.imei], ['Nhà mạng', bay.operator || '—'], ['Số dư', opsMoney(bay.balance_vnd)], ['Ở khe từ', bay.last_event_at ? new Date(bay.last_event_at).toLocaleString('vi-VN') : '—']]
        : [['IMEI', bay.imei], ['Cổng', bay.port_name || '—']];
    rows.forEach(([k, v]) => dl.append($('<dt>').text(k), $('<dd>').text(v)));
    pop.append(dl);
    if (bay.current_iccid) {
        const link = opsElement('a', 'text-action', 'Xem lịch sử khe →').attr('href', '#');
        link.click(event => { event.preventDefault(); openSlotHistory(bay.current_iccid); });
        pop.append(opsElement('div', 'mt-2').append(link));
    }
    pop.prop('hidden', false);
}

function renderTray() {
    const { cells } = buildTrayCells(opsState.bays, Date.now());
    const tray = $('#ops-tray').empty();
    cells.forEach(cell => tray.append(bayCard(cell)));
}
```

`renderLiveUnassigned` viết lại: nguồn là `buildTrayCells(opsState.bays).unassigned`; mỗi dòng hiện `port_name · IMEI …xxxx · phone_number`, một `<select>` liệt kê **chỉ các khe chưa có modem** (từ `cells.filter(c => !c.bay)`), nút "Gán khe" gọi:

```js
function assignBaySlot(imei, slot) {
    return $.ajax({ url: `/api/v1/bays/${encodeURIComponent(imei)}`, method: 'PATCH', contentType: 'application/json', data: JSON.stringify({ slot_number: slot }) });
}
```

`.done(loadOperationsData)`, `.fail` hiện `xhr.responseJSON.error` bằng `title` như code cũ. Bỏ input số điện thoại ở đây (số ĐT vẫn sửa ở Bảo trì qua `/profile`).

Đóng popover: `$(document).on('click', e => { if (!$(e.target).closest('.bay, #ops-tray-pop').length) $('#ops-tray-pop').prop('hidden', true); });` trong `ready`.

- [ ] **Step 6: `renderOperationsConsole` gọi `renderTray()` thay `renderSlotGrid()`; thêm `renderBayCalibration(); renderSlotHistory();` (Task 8 định nghĩa — tạm khai hai hàm rỗng để không vỡ).**

- [ ] **Step 7: Xoá dòng "Preview · chưa lưu mapping" và nút "Bỏ mapping preview" (đã xoá theo template/JS mới). Grep để chắc:**

Run: `grep -n "previewMappings\|loadOpsProfiles\|saveOpsProfiles\|renderSlotGrid\|opsMappedModems\|opsUnassignedModems" web/static/js/*.js web/templates/index.html`
Expected: không còn kết quả.

- [ ] **Step 8: Chạy JS test + build + mở app xem tay**

Run: `node --test web/static/js/operations-console.test.js && go build -tags nouac -o smsie.exe .`
Chạy `./smsie.exe` với `config.yaml` dev (SQLite), mở `http://localhost:8080/#/slots`, đăng nhập admin. Kỳ vọng: 32 ô khe, ô không modem hiện "Chưa gán modem", không lỗi console.

- [ ] **Step 9: Commit**

```bash
git add web/templates/index.html web/static/css/operations-console.css web/static/js/operations-console.js
git commit -m "feat(slot-ui): bản đồ khay 8×4 đọc từ /bays, gán khe theo IMEI"
```

---

### Task 8: Frontend — tab Lịch sử khe + Hiệu chuẩn + CSV

**Files:**
- Modify: `web/static/js/operations-console.js`

- [ ] **Step 1: Chuyển tab**

```js
function showSlotTab(tab) {
    opsState.slotTab = tab;
    $('[data-slot-tab]').each(function () { $(this).toggleClass('is-active', $(this).data('slot-tab') === tab); });
    ['tray', 'history', 'calibration'].forEach(name => $(`#slot-tab-${name}`).toggleClass('d-none', name !== tab));
}
```

Trong `ready`: `$('[data-slot-tab]').click(function () { showSlotTab($(this).data('slot-tab')); });`

- [ ] **Step 2: Lịch sử khe**

```js
function openSlotHistory(iccid) {
    window.navigateApp('slots', iccid);
    showSlotTab('history');
    loadSlotEvents(iccid);
}

function loadSlotEvents(iccid) {
    const params = { iccid, page_size: 500 };
    const from = $('#slot-events-from').val();
    const to = $('#slot-events-to').val();
    if (from) params.from = from;
    if (to) params.to = to;
    $.get('/api/v1/slot-events', params).done(function (response) {
        opsState.slotEvents = response.data || [];
        renderSlotHistory();
    });
}

function renderSlotHistory() {
    const route = window.currentAppRoute ? window.currentAppRoute() : { iccid: '' };
    const card = $('#ops-slot-sim-card').empty();
    const timeline = $('#ops-slot-timeline').empty();
    if (!route.iccid) {
        card.append(opsElement('div', 'ops-empty', 'Chọn một khe trên bản đồ khay rồi bấm "Xem lịch sử khe".'));
        return;
    }
    const bay = opsState.bays.find(b => b.current_iccid === route.iccid);
    const modem = opsState.modems.find(m => m.iccid === route.iccid) || {};
    card.append(opsElement('div', 'eyebrow', 'SIM · ICCID'));
    card.append(opsElement('h3', 'mono', route.iccid));
    const dl = $('<dl>').addClass('kv');
    [['Số thuê bao', modem.phone_number || '—'], ['Số dư', `${opsBalanceLabel(modem)}`], ['Lần đảo', `${opsState.slotEvents.filter(e => e.event === 'moved').length} lần`]]
        .forEach(([k, v]) => dl.append($('<dt>').text(k), $('<dd>').text(v)));
    card.append(dl);
    card.append(opsElement('div', 'now', bay ? `Đang ở khe ${bay.slot_number || '— (chưa gán)'} · ${bay.port_name || ''} · ${bay.status === 'online' ? 'trực tuyến' : 'ngoại tuyến'}` : 'Hiện không nằm trong khay'));

    if (!opsState.slotEvents.length) {
        timeline.append(opsElement('div', 'ops-empty', 'Chưa có sự kiện đổi khe cho SIM này.'));
        return;
    }
    const icons = { moved: 'bi-arrow-left-right', removed: 'bi-eject', inserted: 'bi-box-arrow-in-down' };
    groupSlotEventsByDay(opsState.slotEvents).forEach(group => {
        timeline.append(opsElement('div', 'day', new Date(group.day).toLocaleDateString('vi-VN')));
        const ul = $('<ul>').addClass('timeline');
        group.events.forEach(event => {
            const d = describeSlotEvent(event);
            const li = $('<li>');
            const when = opsElement('div', 'when');
            when.append(opsElement('b', '', new Date(event.detected_at).toLocaleTimeString('vi-VN')), document.createTextNode(event.port_name || ''));
            const ico = opsElement('span', `ico ${d.tone}`).append($('<i>').addClass(`bi ${icons[event.event] || 'bi-dot'}`));
            const what = opsElement('div', 'what');
            what.append(opsElement('span', 'path', d.path));
            if (event.operator) what.append(opsElement('small', '', event.operator));
            const bal = opsElement('div', `bal${d.balance === '—' ? ' na' : ''}`, d.balance).append(opsElement('small', '', d.balanceNote));
            li.append(when, ico, what, bal);
            ul.append(li);
        });
        timeline.append(ul);
    });
}
```

Trong `ready`: `$('#slot-events-from, #slot-events-to').change(function () { const r = window.currentAppRoute(); if (r.iccid) loadSlotEvents(r.iccid); });`
Trong handler `smsie:route`: nếu `route.view === 'slots' && route.iccid` thì `loadSlotEvents(route.iccid)`.

- [ ] **Step 3: Hiệu chuẩn**

```js
function renderBayCalibration() {
    const { cells, unassigned } = buildTrayCells(opsState.bays, Date.now());
    const body = $('#ops-cal-body').empty();
    const assigned = cells.filter(c => c.bay).length;
    $('#ops-cal-summary').text(`${assigned}/32 khe đã gán · ${unassigned.length} modem chưa gán`);
    const freeSlots = cells.filter(c => !c.bay).map(c => c.slot);
    const pill = bay => !bay ? '<span class="pill off">Chưa gán</span>' : !bay.current_iccid ? '<span class="pill warn">Không SIM</span>' : bay.status === 'online' ? '<span class="pill ok">Trực tuyến</span>' : '<span class="pill off">Ngoại tuyến</span>';
    const row = (slot, bay) => {
        const tr = $('<tr>');
        tr.append($('<td>').addClass('mono').text(slot ? String(slot).padStart(2, '0') : '—'));
        tr.append($('<td>').addClass('mono').text(bay ? bay.imei : '—'));
        tr.append($('<td>').addClass('mono').text(bay && bay.current_iccid ? bay.current_iccid : '—'));
        tr.append($('<td>').addClass('mono').text(bay && bay.phone_number ? bay.phone_number : '—'));
        tr.append($('<td>').html(pill(bay)));
        tr.append($('<td>').addClass('mono').text(bay && bay.last_seen_at ? new Date(bay.last_seen_at).toLocaleString('vi-VN') : '—'));
        const actions = $('<td>');
        if (bay) {
            const select = $('<select>').addClass('form-select form-select-sm');
            select.append($('<option>').val('').text(slot ? 'Bỏ gán' : 'Chọn khe…'));
            freeSlots.forEach(s => select.append($('<option>').val(s).text(`Khe ${String(s).padStart(2, '0')}`)));
            select.change(function () {
                const value = $(this).val();
                assignBaySlot(bay.imei, value ? Number(value) : null)
                    .done(loadOperationsData)
                    .fail(xhr => window.alert(xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không gán được khe'));
            });
            actions.append(select);
        }
        tr.append(actions);
        return tr;
    };
    cells.forEach(c => body.append(row(c.slot, c.bay)));
    unassigned.forEach(b => body.append(row(null, b)));
}
```

- [ ] **Step 4: Xuất CSV lịch sử** — trong `ready`:

```js
    $('#btn-export-slot-events').click(function () {
        const rows = [['detected_at', 'event', 'iccid', 'imei', 'phone_number', 'from_slot', 'to_slot', 'balance_vnd', 'port_name']];
        opsState.slotEvents.forEach(e => rows.push([e.detected_at, e.event, e.iccid, e.imei, e.phone_number || '', e.from_slot ?? '', e.to_slot ?? '', e.balance_vnd ?? '', e.port_name || '']));
        const csv = rows.map(r => r.map(v => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\r\n');
        const url = URL.createObjectURL(new Blob(['\ufeff', csv], { type: 'text/csv;charset=utf-8' }));
        const link = document.createElement('a');
        link.href = url;
        link.download = `smsie-slot-events-${new Date().toISOString().slice(0, 10)}.csv`;
        link.click();
        URL.revokeObjectURL(url);
    });
```

- [ ] **Step 5: Nhật ký (Audit)** — trong `renderAuditPreview` (dòng ~651) thêm các `opsState.slotEvents` gần nhất (nếu đã tải) thành dòng `thời gian · hệ thống · "Đổi khe" · ICCID · path`. Đọc hàm trước; chỉ thêm rows, không đổi cấu trúc bảng.

- [ ] **Step 6: Kiểm thử tay theo kịch bản**

Chạy `./smsie.exe`; trên DB dev tạo dữ liệu bằng SQLite CLI hoặc để modem thật probe. Kịch bản tối thiểu (không cần modem):

```sql
INSERT INTO modems (iccid, imei, phone_number, balance_vnd) VALUES ('ICCID-1','IMEI-A','0987654321',48500),('ICCID-2','IMEI-B','0912334770',26000);
INSERT INTO modem_bays (imei, slot_number, current_iccid) VALUES ('IMEI-A',15,'ICCID-1'),('IMEI-B',16,'ICCID-2');
INSERT INTO sim_slot_events (detected_at, iccid, imei, event, from_slot, to_slot, balance_vnd, port_name) VALUES (datetime('now','-1 hour'),'ICCID-1','IMEI-A','moved',16,15,48500,'COM17');
```

Kỳ vọng: khe 15 và 16 hiện số ĐT; khe 15 có viền "↔ 24h"; bấm khe 15 → "Xem lịch sử khe" → timeline có 1 dòng `Khe 16 → Khe 15 · 48.500 đ`; tab Hiệu chuẩn hiện 2/32; đổi khe của IMEI-B sang 20 → tải lại thấy khe 20.

- [ ] **Step 7: JS test + Go test + commit**

Run: `node --test web/static/js/operations-console.test.js && go test -tags nouac ./...`

```bash
git add web/static/js/operations-console.js web/templates/index.html web/static/css/operations-console.css
git commit -m "feat(slot-ui): tab lịch sử khe, hiệu chuẩn khe ↔ IMEI, xuất CSV"
```

---

### Task 9: Tài liệu + OpenAPI

**Files:**
- Modify: `openapi/swagger.yaml`
- Modify: `README.md` (mục Features)
- Modify: `docs/specs/persistent-operations.md` (ghi chú `slot_number` đã chuyển sang bays)

- [ ] **Step 1: swagger** — thêm paths `/bays` (GET), `/bays/{imei}` (PATCH body `{slot_number: integer|null}`; 409), `/slot-events` (GET query `iccid, slot, from, to, page, page_size`; response `{data, total, page, page_size}`); schema `ModemBay`, `SimSlotEvent` khớp struct Task 1; bỏ `slot_number` khỏi body của `/modems/{iccid}/profile`.

- [ ] **Step 2: README** — dưới "Modem Management" thêm:

```markdown
- **SIM slot history**: each physical bay is identified by the modem IMEI; when a SIM (ICCID) shows up in a different bay, smsie records `inserted / moved / removed` events with the `*101#` balance read right after detection. Calibrate bay numbers once under *Khe SIM → Hiệu chuẩn*.
```

- [ ] **Step 3: `docs/specs/persistent-operations.md`** — trong "API contract" đổi dòng `PATCH /api/v1/modems/:iccid/profile` thành chỉ còn `phone_number, hardware_path, balance_vnd, balance_updated_at`, thêm ghi chú "slot_number moved to `PATCH /api/v1/bays/:imei` (see `sim-slot-history.md`)".

- [ ] **Step 4: Commit + push nhánh**

```bash
git add openapi/swagger.yaml README.md docs/specs/persistent-operations.md
git commit -m "docs: API bays/slot-events, README lịch sử đảo SIM"
git push -u origin feat/sim-slot-history
```

Mở PR vào `main` với tiêu đề `feat: lịch sử đảo SIM (khe = IMEI)`; mô tả liên kết spec + mockup và ảnh chụp 3 màn (`docs/mockups/*.png`).

---

## Self-review

- **Spec coverage:** mô hình dữ liệu (T1), kế thừa (T3 `MigrateFromModems` + T4 main), luồng phát hiện + `removed` khi không SIM + không ghi khi rớt COM (T2/T3/T4 — chỉ `MarkEmpty` khi IMEI đọc được mà ICCID rỗng), số dư sau event (T4), API 3 endpoint + bỏ `slot_number` (T5), UI khay 8×4 + popover + dải chưa gán (T7), lịch sử + lọc ngày + CSV (T8), hiệu chuẩn (T8), Audit (T8 bước 5), webhook tuỳ chọn — **bỏ khỏi đợt này** (spec ghi "tuỳ chọn, mặc định tắt"; thêm sau nếu cần).
- **Placeholder:** không có TBD; mọi bước có code.
- **Type consistency:** `Observation{IMEI, ICCID, Operator, PortName, At}` dùng ở T3/T4; `bayView` fields dùng ở T6/T7 (`phone_number, operator, balance_vnd, port_name, signal_strength, status, last_event_at, slot_number, current_iccid, imei`); `describeSlotEvent` trả `{path, tone, balance, balanceNote}` dùng ở T8.
