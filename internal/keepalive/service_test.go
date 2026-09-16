package keepalive

import (
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"gorm.io/gorm"
)

type fakeSender struct {
	calls []string // "iccid→phone: msg"
	err   error
}

func (f *fakeSender) SendSMS(iccid, phone, msg string) error {
	f.calls = append(f.calls, iccid+"→"+phone+": "+msg)
	return f.err
}

// newTest: A (bật, first_seen 30 ngày, khe 1) và B (đích, có số, khe 2); cả hai trong khay, wm nil → offline.
func newTest(t *testing.T, enabled bool) (*Service, *fakeSender, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.SMS{}, &model.Webhook{}, &model.SimAlert{}, &model.KeepaliveRun{}, &model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Add(-30 * 24 * time.Hour)
	db.Create(&model.Modem{ICCID: "A", PhoneNumber: "0900000001", FirstSeenAt: &seen, KeepaliveEnabled: true})
	db.Create(&model.Modem{ICCID: "B", PhoneNumber: "0900000002", FirstSeenAt: &seen})
	s1, s2 := 1, 2
	db.Create(&model.ModemBay{IMEI: "I-A", SlotNumber: &s1, CurrentICCID: "A"})
	db.Create(&model.ModemBay{IMEI: "I-B", SlotNumber: &s2, CurrentICCID: "B"})
	fs := &fakeSender{}
	s := NewService(db, nil, fs, logic.NewWebhookService(repository.NewWebhookRepository(db)),
		config.KeepaliveConfig{Enabled: enabled, IntervalDays: 25, MaxPerMonth: 3, Message: "keepalive {{.Date}}", RunHour: 7})
	s.gap = 0
	s.now = func() time.Time { return time.Date(2026, 9, 16, 7, 0, 0, 0, time.Local) }
	return s, fs, db
}

func alertCount(db *gorm.DB) int64 {
	var n int64
	db.Model(&model.SimAlert{}).Where("kind = ?", model.SimAlertKeepalive).Count(&n)
	return n
}

func TestDisabledNeverSends(t *testing.T) {
	s, fs, _ := newTest(t, false)
	if n, err := s.RunAll(); err != nil || n != 0 {
		t.Fatalf("RunAll = %d, %v", n, err)
	}
	if _, err := s.RunOne("A", true); !errors.Is(err, ErrDisabled) {
		t.Fatalf("RunOne err = %v, want ErrDisabled", err)
	}
	if len(fs.calls) != 0 {
		t.Fatalf("sender called: %v", fs.calls)
	}
}

func TestDueWithTargetSends(t *testing.T) {
	s, fs, db := newTest(t, true)
	run, err := s.RunOne("A", false)
	if err != nil || run == nil || run.Status != model.KeepaliveSent || run.TargetICCID != "B" || run.TargetPhone != "0900000002" {
		t.Fatalf("run = %+v, err = %v", run, err)
	}
	if len(fs.calls) != 1 || fs.calls[0] != "A→0900000002: keepalive 2026-09-16" {
		t.Fatalf("calls = %v", fs.calls)
	}
	var al model.AuditLog
	if err := db.First(&al).Error; err != nil || al.Username != "system" || al.Action != "keepalive.send" || al.ICCID != "A" || al.Target != "0900000002" || al.Status != 200 {
		t.Fatalf("audit = %+v, err = %v", al, err)
	}
	if alertCount(db) != 0 {
		t.Fatal("sent không được tạo alert")
	}
	items, err := s.Collect()
	if err != nil {
		t.Fatal(err)
	}
	a := items[0]
	if a.ICCID != "A" || *a.SlotNumber != 1 || a.SentThisMonth != 1 || a.LastRun == nil || a.LastRun.Status != model.KeepaliveSent ||
		a.LastActivityAt == nil || !a.LastActivityAt.Equal(run.RanAt) || a.NextDueAt == nil || !a.NextDueAt.Equal(run.RanAt.AddDate(0, 0, 25)) {
		t.Fatalf("item A = %+v", a)
	}
}

func TestSenderErrorRecordsFailedAndAlerts(t *testing.T) {
	s, fs, db := newTest(t, true)
	fs.err = errors.New("modem offline")
	run, err := s.RunOne("A", true)
	if err != nil || run.Status != model.KeepaliveFailed || run.Reason != "modem offline" {
		t.Fatalf("run = %+v, err = %v", run, err)
	}
	if alertCount(db) != 1 {
		t.Fatalf("alerts = %d", alertCount(db))
	}
	var a model.SimAlert
	db.First(&a)
	if a.ICCID != "A" || a.Detail != "modem offline" {
		t.Fatalf("alert = %+v", a)
	}
	if _, err := s.RunOne("A", true); err != nil || alertCount(db) != 1 {
		t.Fatalf("alert phải nhắc tối đa 1 lần/7 ngày: err=%v n=%d", err, alertCount(db))
	}
}

func TestMonthlyCapSkips(t *testing.T) {
	s, fs, db := newTest(t, true)
	for i := 0; i < 3; i++ {
		db.Create(&model.KeepaliveRun{ICCID: "A", TargetICCID: "B", Status: model.KeepaliveSent, RanAt: s.now().Add(-time.Duration(i+1) * time.Hour)})
	}
	run, err := s.RunOne("A", true)
	if err != nil || run.Status != model.KeepaliveSkipped || run.Reason != "đã đạt trần 3 lần/tháng" {
		t.Fatalf("run = %+v, err = %v", run, err)
	}
	if len(fs.calls) != 0 || alertCount(db) != 1 {
		t.Fatalf("calls = %v alerts = %d", fs.calls, alertCount(db))
	}
}

func TestNotDueSkipsUnlessForced(t *testing.T) {
	s, fs, db := newTest(t, true)
	db.Create(&model.SMS{ICCID: "A", Type: "received", Timestamp: s.now(), CreatedAt: s.now().Add(-24 * time.Hour)})
	run, err := s.RunOne("A", false)
	if err != nil || run != nil || len(fs.calls) != 0 {
		t.Fatalf("not due: run=%+v err=%v calls=%v", run, err, fs.calls)
	}
	var n int64
	db.Model(&model.KeepaliveRun{}).Count(&n)
	if n != 0 {
		t.Fatalf("không được ghi run, có %d", n)
	}
	if run, err := s.RunOne("A", true); err != nil || run == nil || run.Status != model.KeepaliveSent {
		t.Fatalf("force: run=%+v err=%v", run, err)
	}
}

func TestNoCandidateSkips(t *testing.T) {
	s, fs, db := newTest(t, true)
	db.Model(&model.Modem{}).Where("iccid = ?", "B").Update("phone_number", "")
	run, err := s.RunOne("A", true)
	if err != nil || run.Status != model.KeepaliveSkipped || len(fs.calls) != 0 {
		t.Fatalf("run = %+v, err = %v, calls = %v", run, err, fs.calls)
	}
	slot := 1
	if got := alertText(Item{ICCID: "A", PhoneNumber: "0900000001", SlotNumber: &slot}, run.Reason); got != "🔁 SIM 0900000001 (khe 1): nuôi SIM thất bại — không có SIM cùng nhà mạng có số điện thoại trong khay" {
		t.Fatalf("text = %q", got)
	}
}

// Hai lượt chồng nhau (scheduler RunAll + RunNow) cùng Collect() trước khi vào khoá → cả hai thấy số cũ.
// runOne phải đọc lại trần + mốc gửi cuối từ nhật ký TRONG khoá.
func TestRunOneRereadsLogUnderLock(t *testing.T) {
	s, fs, db := newTest(t, true)
	stale, err := s.Collect() // SentThisMonth = 0, chưa tới hạn nào ghi
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunOne("A", true); err != nil || len(fs.calls) != 1 {
		t.Fatalf("lượt 1: err=%v calls=%v", err, fs.calls)
	}
	// lượt chồng, không force: vừa gửi xong → không còn tới hạn → không run, không gửi
	if run, err := s.runOne(stale[0], stale, false); err != nil || run != nil || len(fs.calls) != 1 {
		t.Fatalf("stale not-due: run=%+v err=%v calls=%v", run, err, fs.calls)
	}
	for i := 0; i < 2; i++ {
		db.Create(&model.KeepaliveRun{ICCID: "A", TargetICCID: "B", Status: model.KeepaliveSent, RanAt: s.now()})
	}
	// lượt chồng, force: nhật ký đã 3 sent → phải skipped dù item cũ nói 0
	if run, err := s.runOne(stale[0], stale, true); err != nil || run == nil || run.Status != model.KeepaliveSkipped || len(fs.calls) != 1 {
		t.Fatalf("stale cap: run=%+v err=%v calls=%v", run, err, fs.calls)
	}
}

func TestBadTemplateDoesNotSend(t *testing.T) {
	s, fs, _ := newTest(t, true)
	s.cfg.Message = "keepalive {{.Date"
	run, err := s.RunOne("A", true)
	if err != nil || run == nil || run.Status != model.KeepaliveFailed || len(fs.calls) != 0 {
		t.Fatalf("run=%+v err=%v calls=%v", run, err, fs.calls)
	}
}
