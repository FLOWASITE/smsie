package repository

import (
	"strconv"
	"strings"
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

// ImportPerson — một nhân sự từ nguồn ngoài (file Excel/CSV xuất từ KeHub…).
type ImportPerson struct {
	Row        int
	Code       string
	Name       string
	Department string
	Email      string
	Phone      string
	Left       bool
}

const (
	ImportCreate    = "create"    // chưa có trong smsie → thêm
	ImportUpdate    = "update"    // đã có, hồ sơ khác → cập nhật
	ImportUnchanged = "unchanged" // đã có, giống hệt
	ImportLeave     = "leave"     // nguồn báo đã nghỉ → đánh dấu nghỉ việc
	ImportReturn    = "return"    // smsie đang ghi đã nghỉ, nguồn báo đang làm → làm lại
	ImportSkip      = "skip"      // bỏ qua (kèm lý do)
)

// ImportItem — việc sẽ làm (hoặc đã làm) với một dòng.
type ImportItem struct {
	Row     int      `json:"row"`
	Name    string   `json:"name"`
	Code    string   `json:"code,omitempty"`
	Action  string   `json:"action"`
	Changes []string `json:"changes,omitempty"` // "Phòng ban: Kế toán → Tài chính"
	Reason  string   `json:"reason,omitempty"`  // lý do bỏ qua
	StaffID uint     `json:"staff_id,omitempty"`
	// Holding — số SIM người này đang giữ; > 0 ở dòng "leave" nghĩa là cần bàn giao sau khi nhập.
	Holding int `json:"holding,omitempty"`
}

type ImportResult struct {
	Applied   bool         `json:"applied"`
	Items     []ImportItem `json:"items"`
	Created   int          `json:"created"`
	Updated   int          `json:"updated"`
	Unchanged int          `json:"unchanged"`
	Left      int          `json:"left"`
	Returned  int          `json:"returned"`
	Skipped   int          `json:"skipped"`
	// LeftHolding — số người vừa bị đánh dấu nghỉ mà còn đứng tên SIM.
	LeftHolding int `json:"left_holding"`
}

// Import so danh sách từ nguồn ngoài với nhân sự trong smsie rồi (khi apply) ghi các thay đổi trong MỘT giao dịch.
//
// Luật:
//   - Khớp người theo mã nhân viên (không phân biệt hoa thường); không có mã thì theo email. Không bao giờ khớp
//     theo tên — hai người trùng tên là chuyện thường.
//   - Chỉ THÊM và CẬP NHẬT. Người có trong smsie mà không có trong file thì để yên: file có thể đã bị lọc.
//   - Ô trống trong file không xoá dữ liệu đang có; số điện thoại chỉ được đụng khi file có cột điện thoại.
//   - Nguồn báo đã nghỉ → đánh dấu nghỉ việc nhưng KHÔNG tự thu hồi SIM: người đó hiện ở mục "Cần xử lý" để
//     admin bàn giao cho đúng người, thay vì SIM lặng lẽ mất chủ.
//   - Ghi chú và lịch sử giao nhận không bao giờ bị đụng.
func (r *StaffRepository) Import(people []ImportPerson, hasPhone, apply bool, now time.Time) (*ImportResult, error) {
	assignMu.Lock()
	defer assignMu.Unlock()
	res := &ImportResult{Applied: apply, Items: []ImportItem{}}

	run := func(tx *gorm.DB) error {
		var staff []model.Staff
		if err := tx.Order("id ASC").Find(&staff).Error; err != nil {
			return err
		}
		byCode, byEmail := map[string]*model.Staff{}, map[string]*model.Staff{}
		for i := range staff {
			s := &staff[i]
			if k := strings.ToLower(strings.TrimSpace(s.Code)); k != "" {
				if _, dup := byCode[k]; !dup {
					byCode[k] = s
				}
			}
			if k := strings.ToLower(strings.TrimSpace(s.Email)); k != "" {
				if _, dup := byEmail[k]; !dup {
					byEmail[k] = s
				}
			}
		}
		holding := map[uint]int{}
		var open []model.SimAssignment
		if err := tx.Select("staff_id").Where("released_at IS NULL").Find(&open).Error; err != nil {
			return err
		}
		for _, a := range open {
			holding[a.StaffID]++
		}

		seen := map[uint]int{}      // staff id → dòng đã xử lý
		seenNew := map[string]int{} // khoá người mới → dòng đã xử lý
		for _, p := range people {
			item := ImportItem{Row: p.Row, Name: p.Name, Code: p.Code}
			codeKey, emailKey := strings.ToLower(p.Code), strings.ToLower(p.Email)
			var match *model.Staff
			if codeKey != "" {
				match = byCode[codeKey]
			}
			if match == nil && emailKey != "" {
				match = byEmail[emailKey]
			}

			if match == nil {
				key := "c:" + codeKey
				if codeKey == "" {
					key = "e:" + emailKey
				}
				switch {
				case p.Left:
					item.Action, item.Reason = ImportSkip, "Đã nghỉ việc và chưa có trong smsie"
				case codeKey == "" && emailKey == "":
					item.Action, item.Reason = ImportSkip, "Không có mã nhân viên lẫn email để nhận diện"
				case seenNew[key] != 0:
					item.Action, item.Reason = ImportSkip, "Trùng với dòng "+strconv.Itoa(seenNew[key])
				default:
					seenNew[key] = p.Row
					item.Action = ImportCreate
					if apply {
						s := model.Staff{Name: p.Name, Code: p.Code, Department: p.Department, Email: strings.ToLower(p.Email), Phone: p.Phone}
						if err := tx.Create(&s).Error; err != nil {
							return err
						}
						item.StaffID = s.ID
					}
				}
				res.count(item)
				res.Items = append(res.Items, item)
				continue
			}

			item.StaffID = match.ID
			if prev := seen[match.ID]; prev != 0 {
				item.Action, item.Reason = ImportSkip, "Trùng với dòng "+strconv.Itoa(prev)
				res.count(item)
				res.Items = append(res.Items, item)
				continue
			}
			seen[match.ID] = p.Row

			updates := map[string]interface{}{}
			diff := func(label, column, old, next string) {
				if next != "" && next != old {
					updates[column] = next
					if old == "" {
						item.Changes = append(item.Changes, label+": "+next)
					} else {
						item.Changes = append(item.Changes, label+": "+old+" → "+next)
					}
				}
			}
			diff("Họ tên", "name", match.Name, p.Name)
			if !strings.EqualFold(match.Code, p.Code) { // khác hoa-thường không phải là đổi mã
				diff("Mã NV", "code", match.Code, p.Code)
			}
			diff("Phòng ban", "department", match.Department, p.Department)
			if !strings.EqualFold(match.Email, p.Email) {
				diff("Email", "email", match.Email, strings.ToLower(p.Email))
			}
			if hasPhone {
				diff("Điện thoại", "phone", match.Phone, p.Phone)
			}

			switch {
			case p.Left && match.LeftAt == nil:
				item.Action, item.Holding = ImportLeave, holding[match.ID]
				updates["left_at"] = now
			case !p.Left && match.LeftAt != nil:
				item.Action = ImportReturn
				updates["left_at"] = nil
			case len(updates) > 0:
				item.Action = ImportUpdate
			default:
				item.Action = ImportUnchanged
			}
			if apply && len(updates) > 0 {
				if err := tx.Model(&model.Staff{}).Where("id = ?", match.ID).Updates(updates).Error; err != nil {
					return err
				}
			}
			res.count(item)
			res.Items = append(res.Items, item)
		}
		return nil
	}

	var err error
	if apply {
		err = r.db.Transaction(run)
	} else {
		err = run(r.db)
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (res *ImportResult) count(item ImportItem) {
	switch item.Action {
	case ImportCreate:
		res.Created++
	case ImportUpdate:
		res.Updated++
	case ImportUnchanged:
		res.Unchanged++
	case ImportLeave:
		res.Left++
		if item.Holding > 0 {
			res.LeftHolding++
		}
	case ImportReturn:
		res.Returned++
	case ImportSkip:
		res.Skipped++
	}
}
