package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/pccr10001/smsie/internal/model"
)

func actions(res *ImportResult) string {
	out := []string{}
	for _, it := range res.Items {
		out = append(out, it.Action)
	}
	return strings.Join(out, ",")
}

func TestImportPreviewThenApply(t *testing.T) {
	r, db := newStaffRepo(t)
	// Có sẵn: Lan (mã NV012, đang giữ SIM-A), Minh (chỉ có email), Hà (đã nghỉ), Tâm (không có trong file).
	lan := model.Staff{Name: "Lan", Code: "NV012", Department: "Kinh doanh", Phone: "0988111222", Note: "ghi chú tay"}
	minh := model.Staff{Name: "Minh", Email: "minh@cty.vn"}
	left := t0.Add(-time.Hour)
	ha := model.Staff{Name: "Lê Thu Hà", Code: "NV031", LeftAt: &left}
	tam := model.Staff{Name: "Tâm", Code: "NV099"}
	for _, s := range []*model.Staff{&lan, &minh, &ha, &tam} {
		db.Create(s)
	}
	if _, err := r.Assign("SIM-A", lan.ID, "admin", "", t0); err != nil {
		t.Fatal(err)
	}

	people := []ImportPerson{
		{Row: 2, Code: "nv012", Name: "Nguyễn Thị Lan", Department: "Kế toán", Left: true},       // khớp mã (khác hoa thường), nguồn báo nghỉ, còn giữ SIM
		{Row: 3, Code: "NV027", Name: "Trần Văn Minh", Email: "MINH@cty.vn", Department: "CSKH"}, // khớp email, bổ sung mã
		{Row: 4, Code: "NV031", Name: "Lê Thu Hà"},                                               // đang ghi đã nghỉ, nguồn báo đang làm
		{Row: 5, Code: "NV040", Name: "Phạm An", Department: "Kho", Email: "an@cty.vn"},          // mới
		{Row: 6, Code: "NV040", Name: "Phạm An (lặp)"},                                           // trùng dòng 5
		{Row: 7, Code: "NV050", Name: "Người cũ", Left: true},                                    // đã nghỉ, chưa có → bỏ qua
		{Row: 8, Name: "Không mã không email"},                                                   // không nhận diện được
		{Row: 9, Code: "NV012", Name: "Lan lặp"},                                                 // trùng dòng 2
	}

	// Xem trước: đúng kế hoạch, KHÔNG ghi gì.
	preview, err := r.Import(people, false, false, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := "leave,update,return,create,skip,skip,skip,skip"
	if actions(preview) != want || preview.Applied {
		t.Fatalf("xem trước = %s (applied=%v), muốn %s", actions(preview), preview.Applied, want)
	}
	if preview.Created != 1 || preview.Updated != 1 || preview.Left != 1 || preview.Returned != 1 || preview.Skipped != 4 || preview.LeftHolding != 1 {
		t.Fatalf("tổng xem trước = %+v", preview)
	}
	if preview.Items[0].Holding != 1 || strings.Join(preview.Items[0].Changes, "; ") != "Họ tên: Lan → Nguyễn Thị Lan; Phòng ban: Kinh doanh → Kế toán" {
		t.Fatalf("dòng Lan = %+v", preview.Items[0])
	}
	if strings.Join(preview.Items[1].Changes, "; ") != "Họ tên: Minh → Trần Văn Minh; Mã NV: NV027; Phòng ban: CSKH" {
		t.Fatalf("dòng Minh = %+v", preview.Items[1])
	}
	if preview.Items[4].Reason != "Trùng với dòng 5" || preview.Items[7].Reason != "Trùng với dòng 2" || !strings.Contains(preview.Items[6].Reason, "nhận diện") {
		t.Fatalf("lý do bỏ qua = %q / %q / %q", preview.Items[4].Reason, preview.Items[7].Reason, preview.Items[6].Reason)
	}
	var n int64
	db.Model(&model.Staff{}).Count(&n)
	var lanNow model.Staff
	db.First(&lanNow, lan.ID)
	if n != 4 || lanNow.Name != "Lan" || lanNow.LeftAt != nil {
		t.Fatalf("xem trước mà đã ghi: %d người, Lan = %+v", n, lanNow)
	}

	// Áp dụng.
	applied, err := r.Import(people, false, true, t0)
	if err != nil || actions(applied) != want || !applied.Applied {
		t.Fatalf("áp dụng = %s, err = %v", actions(applied), err)
	}
	db.First(&lanNow, lan.ID)
	if lanNow.Name != "Nguyễn Thị Lan" || lanNow.Department != "Kế toán" || lanNow.LeftAt == nil ||
		lanNow.Phone != "0988111222" || lanNow.Note != "ghi chú tay" || lanNow.Code != "NV012" {
		t.Fatalf("Lan sau khi nhập (điện thoại/ghi chú phải còn nguyên) = %+v", lanNow)
	}
	// Đánh dấu nghỉ nhưng KHÔNG tự thu hồi SIM: vẫn đứng tên để admin bàn giao.
	if openCount(db, "SIM-A") != 1 {
		t.Fatal("nhập từ file không được tự thu hồi SIM của người nghỉ việc")
	}
	var minhNow, haNow, tamNow, an model.Staff
	db.First(&minhNow, minh.ID)
	db.First(&haNow, ha.ID)
	db.First(&tamNow, tam.ID)
	if minhNow.Code != "NV027" || minhNow.Name != "Trần Văn Minh" || minhNow.Department != "CSKH" || minhNow.Email != "minh@cty.vn" {
		t.Fatalf("Minh = %+v", minhNow)
	}
	if haNow.LeftAt != nil {
		t.Fatal("Hà phải được đánh dấu làm lại")
	}
	if tamNow.Name != "Tâm" || tamNow.LeftAt != nil {
		t.Fatalf("người không có trong file phải để yên: %+v", tamNow)
	}
	if err := db.Where("code = ?", "NV040").First(&an).Error; err != nil || an.Name != "Phạm An" || an.Department != "Kho" || an.Email != "an@cty.vn" {
		t.Fatalf("An = %+v, err = %v", an, err)
	}
	db.Model(&model.Staff{}).Count(&n)
	if n != 5 {
		t.Fatalf("tổng nhân sự = %d, muốn 5 (thêm đúng 1)", n)
	}

	// Nhập lại đúng file đó: không còn gì để làm (idempotent).
	again, _ := r.Import(people, false, true, t0)
	if again.Created != 0 || again.Updated != 0 || again.Left != 0 || again.Returned != 0 || again.Unchanged != 4 {
		t.Fatalf("nhập lần hai = %+v (%s)", again, actions(again))
	}
}

func TestImportPhoneOnlyWhenFileHasPhoneColumn(t *testing.T) {
	r, db := newStaffRepo(t)
	s := model.Staff{Name: "Lan", Code: "NV012", Phone: "0988111222"}
	db.Create(&s)
	people := []ImportPerson{{Row: 2, Code: "NV012", Name: "Lan", Phone: "0977000111"}}

	if res, _ := r.Import(people, false, true, t0); res.Unchanged != 1 {
		t.Fatalf("file không có cột điện thoại mà vẫn đổi: %+v", res)
	}
	res, _ := r.Import(people, true, true, t0)
	var now model.Staff
	db.First(&now, s.ID)
	if res.Updated != 1 || now.Phone != "0977000111" {
		t.Fatalf("có cột điện thoại: %+v, phone = %q", res, now.Phone)
	}
	// Ô trống không xoá số đang có.
	res, _ = r.Import([]ImportPerson{{Row: 2, Code: "NV012", Name: "Lan"}}, true, true, t0)
	db.First(&now, s.ID)
	if res.Unchanged != 1 || now.Phone != "0977000111" {
		t.Fatalf("ô trống xoá mất số: %+v, phone = %q", res, now.Phone)
	}
}
