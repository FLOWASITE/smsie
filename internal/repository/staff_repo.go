package repository

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

var (
	ErrStaffNotFound   = errors.New("staff not found")
	ErrStaffLeft       = errors.New("staff has left")
	ErrSIMNotFound     = errors.New("sim not found")
	ErrSIMNotAssigned  = errors.New("sim is not assigned")
	ErrStaffHoldsSIMs  = errors.New("staff still holds sims")
	ErrStaffHasHistory = errors.New("staff has assignment history")
	ErrSameStaff       = errors.New("source and target staff are the same")
)

// assignMu tuần tự hoá mọi thay đổi giao nhận trong tiến trình. Bất biến "mỗi SIM tối đa một dòng đang mở"
// được kiểm bằng đọc-rồi-ghi; hai request song song cùng đọc "chưa ai giữ" sẽ cùng mở dòng nếu không khoá.
var assignMu sync.Mutex

type StaffRepository struct {
	db *gorm.DB
}

func NewStaffRepository(db *gorm.DB) *StaffRepository {
	return &StaffRepository{db: db}
}

// Holding — một SIM đang do nhân sự giữ, kèm thông tin hiển thị.
type Holding struct {
	AssignmentID uint      `json:"assignment_id"`
	ICCID        string    `gorm:"column:iccid" json:"iccid"`
	PhoneNumber  string    `json:"phone_number,omitempty"`
	SlotNumber   *int      `json:"slot_number,omitempty"`
	StaffID      uint      `json:"staff_id"`
	StaffName    string    `json:"staff_name"`
	Department   string    `json:"department,omitempty"`
	AssignedAt   time.Time `json:"assigned_at"`
	Note         string    `json:"note,omitempty"`
}

// StaffWithSIMs — nhân sự kèm các SIM đang giữ.
type StaffWithSIMs struct {
	model.Staff
	Active bool      `json:"active"`
	SIMs   []Holding `json:"sims"`
}

// AssignmentRow — một dòng lịch sử giao nhận.
type AssignmentRow struct {
	model.SimAssignment
	StaffName   string `json:"staff_name"`
	PhoneNumber string `json:"phone_number,omitempty"`
}

type AssignmentFilter struct {
	ICCID    string
	StaffID  uint
	OpenOnly bool
	Page     int
	Size     int
}

func (r *StaffRepository) Create(s *model.Staff) error {
	return r.db.Create(s).Error
}

func (r *StaffRepository) Get(id uint) (*model.Staff, error) {
	var s model.Staff
	if err := r.db.First(&s, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStaffNotFound
		}
		return nil, err
	}
	return &s, nil
}

// UpdateProfile ghi các trường hồ sơ; không đụng LeftAt (nghỉ việc đi qua SetLeft).
func (r *StaffRepository) UpdateProfile(id uint, in model.Staff) (*model.Staff, error) {
	if _, err := r.Get(id); err != nil {
		return nil, err
	}
	err := r.db.Model(&model.Staff{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name": in.Name, "code": in.Code, "department": in.Department,
		"phone": in.Phone, "email": in.Email, "note": in.Note,
	}).Error
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// Holdings trả mọi SIM đang có người giữ. slot lấy từ khe đang chứa SIM (modem_bays), không có thì rỗng.
func (r *StaffRepository) Holdings() ([]Holding, error) {
	var out []Holding
	err := r.db.Table("sim_assignments AS a").
		Select("a.id AS assignment_id, a.iccid, m.phone_number, b.slot_number, a.staff_id, s.name AS staff_name, s.department, a.assigned_at, a.note").
		Joins("JOIN staff AS s ON s.id = a.staff_id").
		Joins("LEFT JOIN modems AS m ON m.iccid = a.iccid").
		Joins("LEFT JOIN modem_bays AS b ON b.current_iccid = a.iccid").
		Where("a.released_at IS NULL").
		Order("a.assigned_at ASC, a.id ASC").
		Scan(&out).Error
	if out == nil {
		out = []Holding{}
	}
	return out, err
}

// List trả mọi nhân sự (đang làm trước, rồi theo tên) kèm SIM đang giữ.
func (r *StaffRepository) List() ([]StaffWithSIMs, error) {
	var staff []model.Staff
	if err := r.db.Order("CASE WHEN left_at IS NULL THEN 0 ELSE 1 END, name ASC, id ASC").Find(&staff).Error; err != nil {
		return nil, err
	}
	holdings, err := r.Holdings()
	if err != nil {
		return nil, err
	}
	byStaff := map[uint][]Holding{}
	for _, h := range holdings {
		byStaff[h.StaffID] = append(byStaff[h.StaffID], h)
	}
	out := make([]StaffWithSIMs, 0, len(staff))
	for _, s := range staff {
		sims := byStaff[s.ID]
		if sims == nil {
			sims = []Holding{}
		}
		out = append(out, StaffWithSIMs{Staff: s, Active: s.LeftAt == nil, SIMs: sims})
	}
	return out, nil
}

func openAssignment(tx *gorm.DB, iccid string) (*model.SimAssignment, error) {
	// Find + Limit thay vì First: "chưa ai giữ" là chuyện bình thường, không phải lỗi cần ghi log.
	var rows []model.SimAssignment
	if err := tx.Where("iccid = ? AND released_at IS NULL", iccid).Order("id DESC").Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func closeAssignment(tx *gorm.DB, id uint, by, note string, now time.Time) error {
	return tx.Model(&model.SimAssignment{}).Where("id = ? AND released_at IS NULL", id).
		Updates(map[string]interface{}{"released_at": now, "released_by": by, "release_note": note}).Error
}

func activeStaff(tx *gorm.DB, id uint) (*model.Staff, error) {
	var s model.Staff
	if err := tx.First(&s, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStaffNotFound
		}
		return nil, err
	}
	if s.LeftAt != nil {
		return nil, ErrStaffLeft
	}
	return &s, nil
}

// AssignResult — kết quả giao SIM. Previous khác nil nghĩa là đây là lần ĐỔI người giữ; Changed false nghĩa là
// SIM vốn đã do đúng người này giữ, không ghi gì thêm.
type AssignResult struct {
	Assignment model.SimAssignment  `json:"assignment"`
	Previous   *model.SimAssignment `json:"previous,omitempty"`
	Staff      model.Staff          `json:"staff"`
	Changed    bool                 `json:"changed"`
}

// Assign giao SIM cho nhân sự. SIM đang do người khác giữ thì đóng lần giao cũ (ghi "chuyển cho …") rồi mở lần
// mới trong cùng một giao dịch — không có khoảnh khắc nào SIM có hai người giữ hoặc mất dấu người giữ cũ.
func (r *StaffRepository) Assign(iccid string, staffID uint, by, note string, now time.Time) (*AssignResult, error) {
	assignMu.Lock()
	defer assignMu.Unlock()
	iccid = strings.TrimSpace(iccid)
	res := &AssignResult{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		staff, err := activeStaff(tx, staffID)
		if err != nil {
			return err
		}
		res.Staff = *staff
		var n int64
		if err := tx.Model(&model.Modem{}).Where("iccid = ?", iccid).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return ErrSIMNotFound
		}
		current, err := openAssignment(tx, iccid)
		if err != nil {
			return err
		}
		if current != nil && current.StaffID == staffID {
			res.Assignment = *current
			return nil
		}
		if current != nil {
			if err := closeAssignment(tx, current.ID, by, "Chuyển cho "+staff.Name, now); err != nil {
				return err
			}
			res.Previous = current
		}
		res.Assignment = model.SimAssignment{ICCID: iccid, StaffID: staffID, AssignedAt: now, AssignedBy: by, Note: note}
		res.Changed = true
		return tx.Create(&res.Assignment).Error
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Release thu hồi SIM (không còn ai giữ).
func (r *StaffRepository) Release(iccid, by, note string, now time.Time) (*model.SimAssignment, error) {
	assignMu.Lock()
	defer assignMu.Unlock()
	var released *model.SimAssignment
	err := r.db.Transaction(func(tx *gorm.DB) error {
		current, err := openAssignment(tx, strings.TrimSpace(iccid))
		if err != nil {
			return err
		}
		if current == nil {
			return ErrSIMNotAssigned
		}
		released = current
		return closeAssignment(tx, current.ID, by, note, now)
	})
	return released, err
}

// Transfer chuyển MỌI SIM đang do `from` giữ sang `to` (bàn giao khi đổi nhân sự / nghỉ việc). Trả số SIM đã chuyển.
func (r *StaffRepository) Transfer(from, to uint, by, note string, now time.Time) (int, error) {
	if from == to {
		return 0, ErrSameStaff
	}
	assignMu.Lock()
	defer assignMu.Unlock()
	moved := 0
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var src model.Staff
		if err := tx.First(&src, from).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}
		target, err := activeStaff(tx, to)
		if err != nil {
			return err
		}
		var open []model.SimAssignment
		if err := tx.Where("staff_id = ? AND released_at IS NULL", from).Order("id ASC").Find(&open).Error; err != nil {
			return err
		}
		for _, a := range open {
			if err := closeAssignment(tx, a.ID, by, "Chuyển cho "+target.Name, now); err != nil {
				return err
			}
			next := model.SimAssignment{ICCID: a.ICCID, StaffID: to, AssignedAt: now, AssignedBy: by, Note: note}
			if next.Note == "" {
				next.Note = "Nhận bàn giao từ " + src.Name
			}
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
			moved++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}

// SetLeft đánh dấu nghỉ việc. Còn giữ SIM mà releaseSIMs = false thì từ chối (ErrStaffHoldsSIMs) để không ai
// nghỉ mà SIM vẫn đứng tên họ; releaseSIMs = true thì thu hồi hết trong cùng giao dịch. Trả số SIM đã thu hồi.
func (r *StaffRepository) SetLeft(id uint, releaseSIMs bool, by string, now time.Time) (int, error) {
	assignMu.Lock()
	defer assignMu.Unlock()
	released := 0
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.Staff
		if err := tx.First(&s, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}
		var open []model.SimAssignment
		if err := tx.Where("staff_id = ? AND released_at IS NULL", id).Find(&open).Error; err != nil {
			return err
		}
		if len(open) > 0 && !releaseSIMs {
			return ErrStaffHoldsSIMs
		}
		for _, a := range open {
			if err := closeAssignment(tx, a.ID, by, "Nghỉ việc", now); err != nil {
				return err
			}
			released++
		}
		if s.LeftAt != nil {
			return nil
		}
		return tx.Model(&model.Staff{}).Where("id = ?", id).Update("left_at", now).Error
	})
	if err != nil {
		return 0, err
	}
	return released, nil
}

// SetReturned bỏ đánh dấu nghỉ việc (làm lại).
func (r *StaffRepository) SetReturned(id uint) error {
	res := r.db.Model(&model.Staff{}).Where("id = ?", id).Update("left_at", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrStaffNotFound
	}
	return nil
}

// Delete xoá hẳn nhân sự — chỉ khi chưa từng được giao SIM nào. Đã có lịch sử thì phải đánh dấu nghỉ việc,
// nếu không lịch sử giao nhận sẽ trỏ tới người không còn tồn tại.
func (r *StaffRepository) Delete(id uint) error {
	assignMu.Lock()
	defer assignMu.Unlock()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.SimAssignment{}).Where("staff_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrStaffHasHistory
		}
		res := tx.Delete(&model.Staff{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrStaffNotFound
		}
		return nil
	})
}

// History trả lịch sử giao nhận, mới nhất trước.
func (r *StaffRepository) History(f AssignmentFilter) ([]AssignmentRow, int64, error) {
	if f.Size <= 0 {
		f.Size = 50
	}
	if f.Size > 500 {
		f.Size = 500
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	q := r.db.Table("sim_assignments AS a").
		Joins("LEFT JOIN staff AS s ON s.id = a.staff_id").
		Joins("LEFT JOIN modems AS m ON m.iccid = a.iccid")
	if f.ICCID != "" {
		q = q.Where("a.iccid = ?", f.ICCID)
	}
	if f.StaffID != 0 {
		q = q.Where("a.staff_id = ?", f.StaffID)
	}
	if f.OpenOnly {
		q = q.Where("a.released_at IS NULL")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []AssignmentRow{}
	err := q.Select("a.*, s.name AS staff_name, m.phone_number").
		Order("a.assigned_at DESC, a.id DESC").
		Offset((f.Page - 1) * f.Size).Limit(f.Size).
		Scan(&rows).Error
	return rows, total, err
}

// HolderNamesAt trả tên người giữ từng SIM tại thời điểm `at` (dùng cho báo cáo tháng: ai giữ lúc cuối tháng).
func (r *StaffRepository) HolderNamesAt(at time.Time) (map[string]string, error) {
	var rows []struct {
		ICCID string `gorm:"column:iccid"`
		Name  string
	}
	err := r.db.Table("sim_assignments AS a").
		Select("a.iccid, s.name").
		Joins("JOIN staff AS s ON s.id = a.staff_id").
		Where("a.assigned_at < ? AND (a.released_at IS NULL OR a.released_at >= ?)", at, at).
		Order("a.assigned_at ASC, a.id ASC").
		Scan(&rows).Error
	out := map[string]string{}
	for _, row := range rows {
		out[row.ICCID] = row.Name // dòng muộn hơn thắng
	}
	return out, err
}
