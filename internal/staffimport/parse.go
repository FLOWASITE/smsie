package staffimport

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Person — một nhân sự đọc từ file. Row là số dòng trong file (đếm từ 1) để báo lại cho người dùng.
type Person struct {
	Row        int    `json:"row"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Left       bool   `json:"left"`
}

// Parsed — kết quả đọc file: người, cột nào được nhận ra, và các dòng bị bỏ kèm lý do.
type Parsed struct {
	People   []Person `json:"-"`
	Columns  []string `json:"columns"`  // nhãn các trường đã nhận ra, ví dụ ["Mã NV", "Họ tên", "Phòng ban"]
	HasPhone bool     `json:"-"`        // file có cột điện thoại — không có thì không đụng số đã lưu
	Skipped  []string `json:"warnings"` // "Dòng 7: không có họ tên"
}

var deaccent = func() *strings.Replacer {
	groups := map[string]string{
		"a": "àáạảãâầấậẩẫăằắặẳẵ", "e": "èéẹẻẽêềếệểễ", "i": "ìíịỉĩ",
		"o": "òóọỏõôồốộổỗơờớợởỡ", "u": "ùúụủũưừứựửữ", "y": "ỳýỵỷỹ", "d": "đ",
	}
	pairs := []string{}
	for base, set := range groups {
		for _, r := range set {
			pairs = append(pairs, string(r), base)
		}
	}
	return strings.NewReplacer(pairs...)
}()

// normHeader: chữ thường, bỏ dấu tiếng Việt (cả dạng tổ hợp), mọi ký tự không phải chữ/số thành một dấu cách.
func normHeader(s string) string {
	s = deaccent.Replace(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r): // dấu tổ hợp (file gõ kiểu NFD)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

type field int

const (
	fCode field = iota
	fName
	fDept
	fSubDept
	fEmail
	fPhone
	fStatus
	fLeftDate
)

// headerNames — tên cột được nhận (đã chuẩn hoá). Dòng đầu mỗi nhóm là tên cột trong file xuất của KeHub.
var headerNames = map[string]field{
	"ma nv": fCode, "ma nhan vien": fCode, "ma nhan su": fCode, "ma so": fCode, "ma": fCode, "code": fCode, "employee code": fCode, "employee id": fCode,
	"ho ten": fName, "ho va ten": fName, "ten": fName, "ten nhan vien": fName, "ten nhan su": fName, "name": fName, "full name": fName, "fullname": fName,
	"phong ban": fDept, "phong": fDept, "don vi": fDept, "department": fDept,
	"bo phan": fSubDept, "to": fSubDept, "nhom": fSubDept, "team": fSubDept,
	"email": fEmail, "e mail": fEmail, "thu dien tu": fEmail, "mail": fEmail,
	"so dien thoai": fPhone, "sdt": fPhone, "dien thoai": fPhone, "di dong": fPhone, "so di dong": fPhone, "phone": fPhone, "mobile": fPhone, "phone number": fPhone,
	"trang thai": fStatus, "trang thai lam viec": fStatus, "status": fStatus,
	"ngay nghi viec": fLeftDate, "ngay nghi": fLeftDate, "ngay thoi viec": fLeftDate,
}

var fieldLabels = map[field]string{
	fCode: "Mã NV", fName: "Họ tên", fDept: "Phòng ban", fSubDept: "Bộ phận", fEmail: "Email",
	fPhone: "Điện thoại", fStatus: "Trạng thái", fLeftDate: "Ngày nghỉ việc",
}

var leftStatuses = map[string]bool{
	"nghi viec": true, "da nghi": true, "da nghi viec": true, "thoi viec": true, "da thoi viec": true,
	"inactive": true, "left": true, "resigned": true, "terminated": true,
}

var dateLayouts = []string{"02/01/2006", "2/1/2006", "2006-01-02", "02-01-2006", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05"}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// Parse tìm dòng tiêu đề (trong 15 dòng đầu, dòng đầu tiên có cột họ tên) rồi đọc từng người.
// `now` quyết định "ngày nghỉ việc" đã tới hay chưa.
func Parse(rows [][]string, now time.Time) (*Parsed, error) {
	headerAt := -1
	cols := map[field]int{}
	for i := 0; i < len(rows) && i < 15; i++ {
		found := map[field]int{}
		for c, cell := range rows[i] {
			if f, ok := headerNames[normHeader(cell)]; ok {
				if _, dup := found[f]; !dup {
					found[f] = c
				}
			}
		}
		if _, ok := found[fName]; ok {
			headerAt, cols = i, found
			break
		}
	}
	if headerAt < 0 {
		return nil, fmt.Errorf("không tìm thấy cột họ tên — dòng tiêu đề cần có cột \"Họ tên\" (file xuất từ KeHub có sẵn)")
	}

	out := &Parsed{People: []Person{}, Columns: []string{}, Skipped: []string{}}
	for _, f := range []field{fCode, fName, fDept, fSubDept, fEmail, fPhone, fStatus, fLeftDate} {
		if _, ok := cols[f]; ok {
			out.Columns = append(out.Columns, fieldLabels[f])
		}
	}
	_, out.HasPhone = cols[fPhone]
	_, hasStatus := cols[fStatus]

	cell := func(row []string, f field) string {
		c, ok := cols[f]
		if !ok || c >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[c])
	}
	for i := headerAt + 1; i < len(rows); i++ {
		row := rows[i]
		blank := true
		for _, v := range row {
			if strings.TrimSpace(v) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		if len(out.People) >= maxRows {
			return nil, fmt.Errorf("%w: quá %d nhân sự", ErrTooLarge, maxRows)
		}
		p := Person{
			Row:        i + 1,
			Code:       trimTo(cell(row, fCode), 40),
			Name:       trimTo(cell(row, fName), 120),
			Department: trimTo(cell(row, fDept), 120),
			Email:      strings.ToLower(trimTo(cell(row, fEmail), 254)),
			Phone:      trimTo(cell(row, fPhone), 32),
		}
		if p.Department == "" {
			p.Department = trimTo(cell(row, fSubDept), 120)
		}
		if p.Name == "" {
			out.Skipped = append(out.Skipped, fmt.Sprintf("Dòng %d: không có họ tên", p.Row))
			continue
		}
		if p.Email != "" && !strings.Contains(p.Email, "@") {
			p.Email = "" // ô email chứa thứ khác: bỏ, không chặn cả dòng
		}
		// Có cột trạng thái thì tin trạng thái; không có thì nhìn ngày nghỉ việc đã tới chưa.
		if hasStatus {
			p.Left = leftStatuses[normHeader(cell(row, fStatus))]
		} else if d, ok := parseDate(cell(row, fLeftDate)); ok {
			p.Left = !d.After(now)
		}
		out.People = append(out.People, p)
	}
	if len(out.People) == 0 {
		return nil, ErrEmpty
	}
	return out, nil
}
