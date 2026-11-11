package staffimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)

// xlsx dựng một file .xlsx tối thiểu. Mỗi ô: "s:<chữ>" dùng sharedStrings, "i:<chữ>" inline, "n:<số>" số trần,
// "" bỏ hẳn ô đó khỏi XML (như Excel thật làm với ô trống).
func xlsx(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var shared []string
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	esc := func(s string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
	}
	for r, row := range rows {
		sheet.WriteString(`<row r="` + itoa(r+1) + `">`)
		for c, cell := range row {
			ref := string(rune('A'+c)) + itoa(r+1)
			switch {
			case cell == "":
			case strings.HasPrefix(cell, "s:"):
				shared = append(shared, cell[2:])
				sheet.WriteString(`<c r="` + ref + `" t="s"><v>` + itoa(len(shared)-1) + `</v></c>`)
			case strings.HasPrefix(cell, "i:"):
				sheet.WriteString(`<c r="` + ref + `" t="inlineStr"><is><t>` + esc(cell[2:]) + `</t></is></c>`)
			case strings.HasPrefix(cell, "n:"):
				sheet.WriteString(`<c r="` + ref + `"><v>` + cell[2:] + `</v></c>`)
			}
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	var sst strings.Builder
	sst.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for i, s := range shared {
		if i == 1 { // một chuỗi dạng nhiều đoạn + chú âm, như Excel hay ghi
			half := len([]rune(s)) / 2
			r := []rune(s)
			sst.WriteString(`<si><r><t>` + esc(string(r[:half])) + `</t></r><r><t>` + esc(string(r[half:])) + `</t></r><rPh><t>BO QUA</t></rPh></si>`)
			continue
		}
		sst.WriteString(`<si><t>` + esc(s) + `</t></si>`)
	}
	sst.WriteString(`</sst>`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml":        `<Types/>`,
		"xl/workbook.xml":            `<workbook/>`,
		"xl/sharedStrings.xml":       sst.String(),
		"xl/worksheets/sheet1.xml":   sheet.String(),
		"xl/worksheets/sheet2.xml":   `<worksheet><sheetData><row><c t="inlineStr"><is><t>TRANG KHAC</t></is></c></row></sheetData></worksheet>`,
		"xl/worksheets/_rels/x.rels": `<Relationships/>`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// kehubHeader — đúng thứ tự cột file "Xuất Excel" của KeHub → Nhân sự (phần đầu).
var kehubHeader = []string{"s:Mã NV", "s:Họ tên", "s:Vị trí công việc", "s:Phòng ban", "s:Bộ phận", "s:Chức vụ quản lý", "s:Email", "s:Zalo ID", "s:Ngày vào làm", "s:Trạng thái", "s:Ngày nghỉ việc", "s:Số CCCD", "s:STK ngân hàng", "s:SĐT LH khẩn cấp"}

func TestReadKeHubXLSXKeepsOnlyStaffColumns(t *testing.T) {
	data := xlsx(t, [][]string{
		kehubHeader,
		{"s:NV012", "s:Nguyễn Thị Lan", "s:Kế toán viên", "s:Kế toán", "s:Tổ 1", "", "s:Lan@Cty.vn", "", "s:01/03/2024", "s:Chính thức", "", "s:079123456789", "s:0011223344", "s:0900999888"},
		{"i:NV027", "i:Trần Văn Minh", "", "", "i:CSKH", "", "", "", "", "i:Nghỉ việc", "i:30/09/2026", "n:79000000001", "", ""},
		{"", "", "", "", "", "", "", "", "", "", "", "", "", ""},
		{"s:NV031", "", "", "s:Kế toán", "", "", "", "", "", "s:Thử việc", "", "", "", ""},
	})
	rows, err := ReadTable(data)
	if err != nil {
		t.Fatal(err)
	}
	if rows[1][1] != "Nguyễn Thị Lan" || rows[0][1] != "Họ tên" {
		t.Fatalf("đọc sai ô (kể cả chuỗi nhiều đoạn, bỏ chú âm): %q / %q", rows[0][1], rows[1][1])
	}
	p, err := Parse(rows, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.People) != 2 {
		t.Fatalf("muốn 2 người, có %d: %+v", len(p.People), p.People)
	}
	lan, minh := p.People[0], p.People[1]
	if lan != (Person{Row: 2, Code: "NV012", Name: "Nguyễn Thị Lan", Department: "Kế toán", Email: "lan@cty.vn"}) {
		t.Fatalf("Lan = %+v", lan)
	}
	// Không có phòng ban thì lấy bộ phận; trạng thái "Nghỉ việc" → đã nghỉ.
	if minh != (Person{Row: 3, Code: "NV027", Name: "Trần Văn Minh", Department: "CSKH", Left: true}) {
		t.Fatalf("Minh = %+v", minh)
	}
	if strings.Join(p.Columns, ",") != "Mã NV,Họ tên,Phòng ban,Bộ phận,Email,Trạng thái,Ngày nghỉ việc" || p.HasPhone {
		t.Fatalf("cột nhận ra = %v, có điện thoại = %v (SĐT khẩn cấp KHÔNG phải số của nhân sự)", p.Columns, p.HasPhone)
	}
	if len(p.Skipped) != 1 || p.Skipped[0] != "Dòng 5: không có họ tên" {
		t.Fatalf("dòng bị bỏ = %v", p.Skipped)
	}
	// Dữ liệu nhạy cảm trong file không lọt vào kết quả.
	for _, person := range p.People {
		flat := person.Code + person.Name + person.Department + person.Email + person.Phone
		for _, secret := range []string{"079123456789", "0011223344", "0900999888", "79000000001"} {
			if strings.Contains(flat, secret) {
				t.Fatalf("lọt dữ liệu nhạy cảm %q vào %+v", secret, person)
			}
		}
	}
}

func TestReadCSVVariants(t *testing.T) {
	semicolon := "\xef\xbb\xbfHọ và tên;Mã nhân viên;Phòng;Số điện thoại;Ngày nghỉ việc\r\n\"Lê Thu Hà\";NV031;Kế toán;0988555666;\r\nPhạm An;NV040;Kho;;01/10/2026\r\nVũ Bình;NV041;Kho;;25/12/2026\r\n"
	rows, err := ReadTable([]byte(semicolon))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(rows, now)
	if err != nil || len(p.People) != 3 {
		t.Fatalf("people = %+v, err = %v", p, err)
	}
	if p.People[0].Phone != "0988555666" || !p.HasPhone || p.People[0].Code != "NV031" {
		t.Fatalf("Hà = %+v", p.People[0])
	}
	// Không có cột trạng thái: ngày nghỉ đã qua → đã nghỉ; ngày nghỉ còn ở tương lai → vẫn đang làm.
	if !p.People[1].Left || p.People[2].Left {
		t.Fatalf("An (nghỉ 01/10) = %v, Bình (nghỉ 25/12) = %v", p.People[1].Left, p.People[2].Left)
	}

	// Tiêu đề không nằm ở dòng 1 (file có dòng tên báo cáo phía trên), dùng tab, tiêu đề tiếng Anh.
	tabbed := "Danh sách nhân viên\n\nName\tDepartment\tE-mail\tStatus\nAn\tSales\tAN@X.VN\tinactive\n"
	rows, _ = ReadTable([]byte(tabbed))
	p, err = Parse(rows, now)
	if err != nil || len(p.People) != 1 || p.People[0] != (Person{Row: 4, Name: "An", Department: "Sales", Email: "an@x.vn", Left: true}) {
		t.Fatalf("people = %+v, err = %v", p, err)
	}
}

func TestRejectsUnusableFiles(t *testing.T) {
	if _, err := ReadTable([]byte{0xD0, 0xCF, 0x11, 0xE0, 1, 2, 3}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), ".xlsx") {
		t.Fatalf(".xls cũ: %v", err)
	}
	if _, err := ReadTable([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ảnh: %v", err)
	}
	if _, err := ReadTable([]byte("PK\x03\x04rác không phải zip")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("zip hỏng: %v", err)
	}
	if _, err := ReadTable(make([]byte, MaxFileBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("quá lớn: %v", err)
	}
	rows, _ := ReadTable([]byte("Cột A,Cột B\n1,2\n"))
	if _, err := Parse(rows, now); err == nil || !strings.Contains(err.Error(), "Họ tên") {
		t.Fatalf("thiếu cột họ tên phải báo rõ: %v", err)
	}
	rows, _ = ReadTable([]byte("Họ tên,Email\n"))
	if _, err := Parse(rows, now); !errors.Is(err, ErrEmpty) {
		t.Fatalf("chỉ có tiêu đề: %v", err)
	}
}

func TestNormHeader(t *testing.T) {
	cases := map[string]string{
		"  Mã NV ": "ma nv", "HỌ VÀ TÊN": "ho va ten", "Số điện thoại": "so dien thoai", "E-mail": "e mail",
		"Phòng  ban": "phong ban", "Họ tên": "ho ten", "Đơn vị": "don vi", "ma_nv": "ma nv",
	}
	for in, want := range cases {
		if got := normHeader(in); got != want {
			t.Errorf("normHeader(%q) = %q, want %q", in, got, want)
		}
	}
}
