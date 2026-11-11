package repository

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func newStaffRepo(t *testing.T) (*StaffRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.Staff{}, &model.SimAssignment{}); err != nil {
		t.Fatal(err)
	}
	slot := 3
	db.Create(&model.Modem{ICCID: "SIM-A", PhoneNumber: "0900000001"})
	db.Create(&model.Modem{ICCID: "SIM-B", PhoneNumber: "0900000002"})
	db.Create(&model.Modem{ICCID: "SIM-C"})
	db.Create(&model.ModemBay{IMEI: "IMEI-1", SlotNumber: &slot, CurrentICCID: "SIM-A"})
	return NewStaffRepository(db), db
}

func addStaff(t *testing.T, r *StaffRepository, name string) uint {
	t.Helper()
	s := model.Staff{Name: name, Department: "Kinh doanh"}
	if err := r.Create(&s); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func openCount(db *gorm.DB, iccid string) int64 {
	var n int64
	db.Model(&model.SimAssignment{}).Where("iccid = ? AND released_at IS NULL", iccid).Count(&n)
	return n
}

var t0 = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)

func TestAssignThenChangeHolderKeepsHistory(t *testing.T) {
	r, db := newStaffRepo(t)
	lan, minh := addStaff(t, r, "Lan"), addStaff(t, r, "Minh")

	first, err := r.Assign("SIM-A", lan, "admin", "SIM chạy quảng cáo", t0)
	if err != nil || !first.Changed || first.Previous != nil || first.Assignment.StaffID != lan {
		t.Fatalf("giao lần đầu = %+v, err = %v", first, err)
	}
	// Giao lại đúng người đó: không ghi thêm dòng nào.
	again, err := r.Assign("SIM-A", lan, "admin", "", t0.Add(time.Hour))
	if err != nil || again.Changed || again.Assignment.ID != first.Assignment.ID {
		t.Fatalf("giao lại cùng người = %+v, err = %v", again, err)
	}
	// Đổi người giữ: dòng cũ đóng lại, dòng mới mở ra, cùng một thời điểm.
	at := t0.Add(48 * time.Hour)
	changed, err := r.Assign("SIM-A", minh, "admin", "", at)
	if err != nil || !changed.Changed || changed.Previous == nil || changed.Previous.StaffID != lan {
		t.Fatalf("đổi người = %+v, err = %v", changed, err)
	}
	if openCount(db, "SIM-A") != 1 {
		t.Fatalf("SIM phải có đúng một người giữ, đang có %d", openCount(db, "SIM-A"))
	}
	rows, total, err := r.History(AssignmentFilter{ICCID: "SIM-A"})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("lịch sử = %d dòng, err = %v", total, err)
	}
	now, old := rows[0], rows[1]
	if now.StaffName != "Minh" || now.ReleasedAt != nil || now.PhoneNumber != "0900000001" {
		t.Fatalf("dòng hiện tại = %+v", now)
	}
	if old.StaffName != "Lan" || old.ReleasedAt == nil || !old.ReleasedAt.Equal(at) || old.ReleaseNote != "Chuyển cho Minh" || old.ReleasedBy != "admin" || old.Note != "SIM chạy quảng cáo" {
		t.Fatalf("dòng cũ = %+v", old)
	}
}

func TestAssignRejectsUnknownSIMStaffAndLeavers(t *testing.T) {
	r, _ := newStaffRepo(t)
	lan := addStaff(t, r, "Lan")
	if _, err := r.Assign("KHONG-CO", lan, "admin", "", t0); !errors.Is(err, ErrSIMNotFound) {
		t.Fatalf("SIM lạ: %v", err)
	}
	if _, err := r.Assign("SIM-A", 999, "admin", "", t0); !errors.Is(err, ErrStaffNotFound) {
		t.Fatalf("nhân sự lạ: %v", err)
	}
	if _, err := r.SetLeft(lan, false, "admin", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Assign("SIM-A", lan, "admin", "", t0); !errors.Is(err, ErrStaffLeft) {
		t.Fatalf("người đã nghỉ không được nhận SIM: %v", err)
	}
	if err := r.SetReturned(lan); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Assign("SIM-A", lan, "admin", "", t0); err != nil {
		t.Fatalf("làm lại thì nhận SIM được: %v", err)
	}
}

func TestReleaseAndHoldings(t *testing.T) {
	r, _ := newStaffRepo(t)
	lan := addStaff(t, r, "Lan")
	if _, err := r.Release("SIM-A", "admin", "", t0); !errors.Is(err, ErrSIMNotAssigned) {
		t.Fatalf("thu hồi SIM chưa giao: %v", err)
	}
	_, _ = r.Assign("SIM-A", lan, "admin", "", t0)
	_, _ = r.Assign("SIM-C", lan, "admin", "", t0.Add(time.Minute))

	held, err := r.Holdings()
	if err != nil || len(held) != 2 {
		t.Fatalf("holdings = %+v, err = %v", held, err)
	}
	a := held[0]
	if a.ICCID != "SIM-A" || a.StaffName != "Lan" || a.Department != "Kinh doanh" || a.PhoneNumber != "0900000001" || a.SlotNumber == nil || *a.SlotNumber != 3 {
		t.Fatalf("holding SIM-A = %+v", a)
	}
	if held[1].ICCID != "SIM-C" || held[1].SlotNumber != nil || held[1].PhoneNumber != "" {
		t.Fatalf("holding SIM-C (không khe, không số) = %+v", held[1])
	}

	if _, err := r.Release("SIM-A", "admin", "Hết dự án", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, _ := r.List()
	if len(list) != 1 || len(list[0].SIMs) != 1 || list[0].SIMs[0].ICCID != "SIM-C" || !list[0].Active {
		t.Fatalf("list = %+v", list)
	}
}

func TestTransferMovesEverySIM(t *testing.T) {
	r, db := newStaffRepo(t)
	lan, minh := addStaff(t, r, "Lan"), addStaff(t, r, "Minh")
	_, _ = r.Assign("SIM-A", lan, "admin", "", t0)
	_, _ = r.Assign("SIM-B", lan, "admin", "", t0)
	_, _ = r.Assign("SIM-C", minh, "admin", "", t0)

	if _, err := r.Transfer(lan, lan, "admin", "", t0); !errors.Is(err, ErrSameStaff) {
		t.Fatalf("chuyển cho chính mình: %v", err)
	}
	n, err := r.Transfer(lan, minh, "admin", "", t0.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("chuyển giao = %d, err = %v", n, err)
	}
	list, _ := r.List()
	for _, s := range list {
		switch s.Name {
		case "Lan":
			if len(s.SIMs) != 0 {
				t.Fatalf("Lan còn giữ %d SIM", len(s.SIMs))
			}
		case "Minh":
			if len(s.SIMs) != 3 {
				t.Fatalf("Minh giữ %d SIM, muốn 3", len(s.SIMs))
			}
		}
	}
	for _, iccid := range []string{"SIM-A", "SIM-B", "SIM-C"} {
		if openCount(db, iccid) != 1 {
			t.Fatalf("%s có %d người giữ", iccid, openCount(db, iccid))
		}
	}
	rows, _, _ := r.History(AssignmentFilter{ICCID: "SIM-B"})
	if rows[0].Note != "Nhận bàn giao từ Lan" || rows[1].ReleaseNote != "Chuyển cho Minh" {
		t.Fatalf("ghi chú bàn giao = %q / %q", rows[0].Note, rows[1].ReleaseNote)
	}
}

func TestLeaveRequiresHandingBackSIMs(t *testing.T) {
	r, db := newStaffRepo(t)
	lan := addStaff(t, r, "Lan")
	_, _ = r.Assign("SIM-A", lan, "admin", "", t0)

	if _, err := r.SetLeft(lan, false, "admin", t0.Add(time.Hour)); !errors.Is(err, ErrStaffHoldsSIMs) {
		t.Fatalf("nghỉ khi còn giữ SIM phải bị chặn: %v", err)
	}
	if s, _ := r.Get(lan); s.LeftAt != nil {
		t.Fatal("bị chặn thì chưa được đánh dấu nghỉ")
	}
	n, err := r.SetLeft(lan, true, "admin", t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("nghỉ + thu hồi = %d, err = %v", n, err)
	}
	if s, _ := r.Get(lan); s.LeftAt == nil || openCount(db, "SIM-A") != 0 {
		t.Fatal("phải đánh dấu nghỉ và SIM không còn người giữ")
	}
	rows, _, _ := r.History(AssignmentFilter{StaffID: lan})
	if len(rows) != 1 || rows[0].ReleaseNote != "Nghỉ việc" {
		t.Fatalf("lịch sử = %+v", rows)
	}
}

func TestDeleteOnlyWithoutHistory(t *testing.T) {
	r, _ := newStaffRepo(t)
	lan, minh := addStaff(t, r, "Lan"), addStaff(t, r, "Minh")
	_, _ = r.Assign("SIM-A", lan, "admin", "", t0)
	_, _ = r.Release("SIM-A", "admin", "", t0.Add(time.Hour))

	if err := r.Delete(lan); !errors.Is(err, ErrStaffHasHistory) {
		t.Fatalf("xoá người đã có lịch sử: %v", err)
	}
	if err := r.Delete(minh); err != nil {
		t.Fatalf("xoá người chưa từng giữ SIM: %v", err)
	}
	if err := r.Delete(minh); !errors.Is(err, ErrStaffNotFound) {
		t.Fatalf("xoá lần hai: %v", err)
	}
}

func TestHolderNamesAtPointInTime(t *testing.T) {
	r, _ := newStaffRepo(t)
	lan, minh := addStaff(t, r, "Lan"), addStaff(t, r, "Minh")
	_, _ = r.Assign("SIM-A", lan, "admin", "", t0)                       // Lan giữ từ 11/11
	_, _ = r.Assign("SIM-A", minh, "admin", "", t0.Add(10*24*time.Hour)) // Minh giữ từ 21/11

	before, _ := r.HolderNamesAt(t0.Add(-time.Hour))
	mid, _ := r.HolderNamesAt(t0.Add(5 * 24 * time.Hour))
	after, _ := r.HolderNamesAt(t0.Add(30 * 24 * time.Hour))
	if before["SIM-A"] != "" || mid["SIM-A"] != "Lan" || after["SIM-A"] != "Minh" {
		t.Fatalf("trước = %q, giữa = %q, sau = %q", before["SIM-A"], mid["SIM-A"], after["SIM-A"])
	}
}

func TestConcurrentAssignLeavesOneHolder(t *testing.T) {
	r, db := newStaffRepo(t)
	ids := []uint{addStaff(t, r, "A"), addStaff(t, r, "B"), addStaff(t, r, "C"), addStaff(t, r, "D")}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = r.Assign("SIM-B", ids[i%len(ids)], "admin", "", t0.Add(time.Duration(i)*time.Second))
		}(i)
	}
	wg.Wait()
	if openCount(db, "SIM-B") != 1 {
		t.Fatalf("giao đồng thời để lại %d người giữ cùng một SIM", openCount(db, "SIM-B"))
	}
}
