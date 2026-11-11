package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/auth"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"gorm.io/gorm"
)

type staffEnv struct {
	t      *testing.T
	r      *gin.Engine
	db     *gorm.DB
	admin  string // token
	viewer string // token của user thường chỉ được xem SIM-A
}

func newStaffEnv(t *testing.T) *staffEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.User{}, &model.UserModemPermission{}, &model.APIKey{}, &model.AuditLog{},
		&model.Modem{}, &model.ModemBay{}, &model.Staff{}, &model.SimAssignment{}); err != nil {
		t.Fatal(err)
	}
	auth.SetSecret([]byte(strings.Repeat("n", auth.MinSecretLen)))
	slot := 5
	db.Create(&model.Modem{ICCID: "SIM-A", PhoneNumber: "0900000001"})
	db.Create(&model.Modem{ICCID: "SIM-B", PhoneNumber: "0900000002"})
	db.Create(&model.ModemBay{IMEI: "IMEI-1", SlotNumber: &slot, CurrentICCID: "SIM-A"})

	admin := model.User{Username: "sep", PasswordHash: noPasswordHash, Role: "admin"}
	viewer := model.User{Username: "nv", PasswordHash: noPasswordHash, Role: "user", AllowedModems: "SIM-A"}
	db.Create(&admin)
	db.Create(&viewer)
	adminTok, _ := auth.GenerateToken(&admin)
	viewerTok, _ := auth.GenerateToken(&viewer)

	h := NewStaffHandler(db)
	r := gin.New()
	authed := r.Group("/api/v1", AuthMiddleware(db), audit.Middleware(db))
	authed.GET("/sim-holders", h.Holders)
	adm := authed.Group("/", AdminOnly())
	adm.GET("/staff", h.List)
	adm.POST("/staff", h.Create)
	adm.PUT("/staff/:id", h.Update)
	adm.DELETE("/staff/:id", h.Delete)
	adm.POST("/staff/:id/leave", h.Leave)
	adm.POST("/staff/:id/return", h.Return)
	adm.POST("/staff/:id/transfer", h.Transfer)
	adm.POST("/modems/:iccid/holder", h.Assign)
	adm.DELETE("/modems/:iccid/holder", h.Release)
	adm.GET("/sim-assignments", h.History)
	return &staffEnv{t: t, r: r, db: db, admin: adminTok, viewer: viewerTok}
}

func (e *staffEnv) addStaff(name string) uint {
	e.t.Helper()
	w := call(e.r, http.MethodPost, "/api/v1/staff", e.admin, gin.H{"name": name, "department": "Kinh doanh"})
	if w.Code != http.StatusCreated {
		e.t.Fatalf("tạo nhân sự %s = %d %s", name, w.Code, w.Body.String())
	}
	var s model.Staff
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	return s.ID
}

func errCode(body []byte) string {
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &out)
	return out.Code
}

func idPath(prefix string, id uint, suffix string) string {
	return prefix + strconv.FormatUint(uint64(id), 10) + suffix
}

func TestStaffRoutesAreAdminOnly(t *testing.T) {
	e := newStaffEnv(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/staff"},
		{http.MethodPost, "/api/v1/staff"},
		{http.MethodPost, "/api/v1/modems/SIM-A/holder"},
		{http.MethodDelete, "/api/v1/modems/SIM-A/holder"},
		{http.MethodGet, "/api/v1/sim-assignments"},
	} {
		if w := call(e.r, c.method, c.path, e.viewer, gin.H{"name": "x", "staff_id": 1}); w.Code != http.StatusForbidden {
			t.Errorf("%s %s với user thường = %d, muốn 403", c.method, c.path, w.Code)
		}
		if w := call(e.r, c.method, c.path, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s không đăng nhập = %d, muốn 401", c.method, c.path, w.Code)
		}
	}
}

func TestCreateStaffValidation(t *testing.T) {
	e := newStaffEnv(t)
	for _, body := range []gin.H{
		{"name": "   "},
		{"name": strings.Repeat("a", 121)},
		{"name": "Lan", "email": "khong-phai-email"},
		{"name": "Lan", "note": strings.Repeat("x", 501)},
	} {
		if w := call(e.r, http.MethodPost, "/api/v1/staff", e.admin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%v → %d, muốn 400", body, w.Code)
		}
	}
	w := call(e.r, http.MethodPost, "/api/v1/staff", e.admin, gin.H{"name": "  Nguyễn Thị Lan ", "code": "NV012", "email": " Lan@Cty.vn "})
	var s model.Staff
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != http.StatusCreated || s.Name != "Nguyễn Thị Lan" || s.Email != "lan@cty.vn" || s.Code != "NV012" {
		t.Fatalf("tạo = %d %+v", w.Code, s)
	}
	up := call(e.r, http.MethodPut, idPath("/api/v1/staff/", s.ID, ""), e.admin, gin.H{"name": "Lan (KD)", "department": "Kinh doanh"})
	var updated model.Staff // biến mới: trường rỗng bị omitempty nên giải mã đè lên biến cũ sẽ giữ giá trị cũ
	_ = json.Unmarshal(up.Body.Bytes(), &updated)
	if up.Code != http.StatusOK || updated.Name != "Lan (KD)" || updated.Department != "Kinh doanh" || updated.Code != "" || updated.Email != "" {
		t.Fatalf("sửa = %d %+v", up.Code, updated)
	}
}

func TestAssignChangeHolderReleaseAndHistory(t *testing.T) {
	e := newStaffEnv(t)
	lan, minh := e.addStaff("Lan"), e.addStaff("Minh")

	// Giao lần đầu
	w := call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan, "note": "chạy quảng cáo"})
	var res repository.AssignResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != http.StatusOK || !res.Changed || res.Previous != nil || res.Assignment.AssignedBy != "sep" {
		t.Fatalf("giao = %d %+v", w.Code, res)
	}
	// Đổi người giữ
	w = call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": minh})
	res = repository.AssignResult{}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != http.StatusOK || !res.Changed || res.Previous == nil || res.Previous.StaffID != lan || res.Staff.Name != "Minh" {
		t.Fatalf("đổi người giữ = %d %+v", w.Code, res)
	}

	// Danh sách nhân sự phản ánh đúng
	var list []repository.StaffWithSIMs
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/staff", e.admin, nil).Body.Bytes(), &list)
	held := map[string]int{}
	for _, s := range list {
		held[s.Name] = len(s.SIMs)
	}
	if held["Lan"] != 0 || held["Minh"] != 1 {
		t.Fatalf("SIM đang giữ = %v", held)
	}

	// Lịch sử: 2 dòng, mới nhất trước
	var hist struct {
		Data  []repository.AssignmentRow `json:"data"`
		Total int64                      `json:"total"`
	}
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/sim-assignments?iccid=SIM-A", e.admin, nil).Body.Bytes(), &hist)
	if hist.Total != 2 || hist.Data[0].StaffName != "Minh" || hist.Data[0].ReleasedAt != nil || hist.Data[1].StaffName != "Lan" || hist.Data[1].ReleaseNote != "Chuyển cho Minh" {
		t.Fatalf("lịch sử = %+v", hist)
	}
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/sim-assignments?open=1", e.admin, nil).Body.Bytes(), &hist)
	if hist.Total != 1 {
		t.Fatalf("đang mở = %d", hist.Total)
	}

	// Thu hồi; thu hồi lần hai báo chưa giao
	if w := call(e.r, http.MethodDelete, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"note": "hết dự án"}); w.Code != http.StatusOK {
		t.Fatalf("thu hồi = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodDelete, "/api/v1/modems/SIM-A/holder", e.admin, nil); w.Code != http.StatusConflict || errCode(w.Body.Bytes()) != "sim_not_assigned" {
		t.Fatalf("thu hồi lần hai = %d %s", w.Code, w.Body.String())
	}

	// Lỗi nghiệp vụ có mã ổn định
	if w := call(e.r, http.MethodPost, "/api/v1/modems/KHONG-CO/holder", e.admin, gin.H{"staff_id": lan}); w.Code != http.StatusNotFound || errCode(w.Body.Bytes()) != "sim_not_found" {
		t.Fatalf("SIM lạ = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": 999}); w.Code != http.StatusNotFound || errCode(w.Body.Bytes()) != "staff_not_found" {
		t.Fatalf("nhân sự lạ = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{}); w.Code != http.StatusBadRequest {
		t.Fatalf("thiếu staff_id = %d", w.Code)
	}

	// Nhật ký ghi hành động kèm tên nhân sự (middleware ghi nền nên chờ một nhịp).
	var logs []model.AuditLog
	for i := 0; i < 50; i++ {
		e.db.Where("action IN ?", []string{"sim.assign", "sim.release"}).Order("id").Find(&logs)
		if len(logs) >= 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var okAssign, okRelease bool
	for _, l := range logs {
		if l.Action == "sim.assign" && l.Status == 200 && l.ICCID == "SIM-A" && l.Target == "Minh" && l.Username == "sep" {
			okAssign = true
		}
		if l.Action == "sim.release" && l.Status == 200 && l.ICCID == "SIM-A" && l.Target == "Minh" {
			okRelease = true
		}
	}
	if !okAssign || !okRelease {
		t.Fatalf("nhật ký thiếu sim.assign/sim.release kèm tên nhân sự: %+v", logs)
	}
}

func TestLeaveTransferAndDelete(t *testing.T) {
	e := newStaffEnv(t)
	lan, minh, tam := e.addStaff("Lan"), e.addStaff("Minh"), e.addStaff("Tâm")
	call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan})
	call(e.r, http.MethodPost, "/api/v1/modems/SIM-B/holder", e.admin, gin.H{"staff_id": lan})

	// Nghỉ việc khi còn giữ SIM: bị chặn, phải bàn giao trước
	if w := call(e.r, http.MethodPost, idPath("/api/v1/staff/", lan, "/leave"), e.admin, gin.H{}); w.Code != http.StatusConflict || errCode(w.Body.Bytes()) != "staff_holds_sims" {
		t.Fatalf("nghỉ khi còn SIM = %d %s", w.Code, w.Body.String())
	}
	// Bàn giao toàn bộ cho Minh
	w := call(e.r, http.MethodPost, idPath("/api/v1/staff/", lan, "/transfer"), e.admin, gin.H{"to_staff_id": minh})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"moved":2`) {
		t.Fatalf("bàn giao = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodPost, idPath("/api/v1/staff/", lan, "/transfer"), e.admin, gin.H{"to_staff_id": lan}); w.Code != http.StatusBadRequest {
		t.Fatalf("bàn giao cho chính mình = %d", w.Code)
	}
	// Giờ nghỉ được; người đã nghỉ không nhận SIM mới, cũng không nhận bàn giao
	if w := call(e.r, http.MethodPost, idPath("/api/v1/staff/", lan, "/leave"), e.admin, gin.H{}); w.Code != http.StatusOK {
		t.Fatalf("nghỉ = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan}); w.Code != http.StatusConflict || errCode(w.Body.Bytes()) != "staff_left" {
		t.Fatalf("giao cho người đã nghỉ = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodPost, idPath("/api/v1/staff/", minh, "/transfer"), e.admin, gin.H{"to_staff_id": lan}); w.Code != http.StatusConflict {
		t.Fatalf("bàn giao cho người đã nghỉ = %d", w.Code)
	}
	// Nghỉ kèm thu hồi luôn
	w = call(e.r, http.MethodPost, idPath("/api/v1/staff/", minh, "/leave"), e.admin, gin.H{"release_sims": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"released":2`) {
		t.Fatalf("nghỉ + thu hồi = %d %s", w.Code, w.Body.String())
	}
	var open int64
	e.db.Model(&model.SimAssignment{}).Where("released_at IS NULL").Count(&open)
	if open != 0 {
		t.Fatalf("còn %d SIM có người giữ", open)
	}
	// Xoá: người có lịch sử thì không, người chưa từng giữ SIM thì được
	if w := call(e.r, http.MethodDelete, idPath("/api/v1/staff/", lan, ""), e.admin, nil); w.Code != http.StatusConflict || errCode(w.Body.Bytes()) != "staff_has_history" {
		t.Fatalf("xoá người có lịch sử = %d %s", w.Code, w.Body.String())
	}
	if w := call(e.r, http.MethodDelete, idPath("/api/v1/staff/", tam, ""), e.admin, nil); w.Code != http.StatusOK {
		t.Fatalf("xoá người chưa giữ SIM = %d", w.Code)
	}
	// Làm lại
	if w := call(e.r, http.MethodPost, idPath("/api/v1/staff/", lan, "/return"), e.admin, nil); w.Code != http.StatusOK {
		t.Fatalf("làm lại = %d", w.Code)
	}
	if w := call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan}); w.Code != http.StatusOK {
		t.Fatalf("giao sau khi làm lại = %d", w.Code)
	}
}

func TestHoldersAreFilteredByModemPermission(t *testing.T) {
	e := newStaffEnv(t)
	lan := e.addStaff("Lan")
	call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan})
	call(e.r, http.MethodPost, "/api/v1/modems/SIM-B/holder", e.admin, gin.H{"staff_id": lan})

	var all, mine []repository.Holding
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/sim-holders", e.admin, nil).Body.Bytes(), &all)
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/sim-holders", e.viewer, nil).Body.Bytes(), &mine)
	if len(all) != 2 {
		t.Fatalf("admin thấy %d người giữ, muốn 2", len(all))
	}
	if len(mine) != 1 || mine[0].ICCID != "SIM-A" || mine[0].StaffName != "Lan" || mine[0].SlotNumber == nil || *mine[0].SlotNumber != 5 {
		t.Fatalf("user chỉ được xem SIM-A lại thấy: %+v", mine)
	}
}

func TestDeletingSIMKeepsAssignmentHistory(t *testing.T) {
	e := newStaffEnv(t)
	if err := e.db.AutoMigrate(&model.SMS{}, &model.Webhook{}); err != nil {
		t.Fatal(err)
	}
	mh := NewModemHandler(e.db, nil, nil)
	e.r.DELETE("/api/v1/modems/:iccid", AuthMiddleware(e.db), AdminOnly(), mh.DeleteModem)

	lan := e.addStaff("Lan")
	call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan, "note": "OTP ngân hàng"})
	e.db.Create(&model.SMS{ICCID: "SIM-A", Phone: "123", Content: "x", Type: "received"})

	if w := call(e.r, http.MethodDelete, "/api/v1/modems/SIM-A", e.admin, nil); w.Code != http.StatusOK {
		t.Fatalf("xoá SIM = %d %s", w.Code, w.Body.String())
	}
	var modems, sms int64
	e.db.Model(&model.Modem{}).Where("iccid = ?", "SIM-A").Count(&modems)
	e.db.Model(&model.SMS{}).Where("iccid = ?", "SIM-A").Count(&sms)
	if modems != 0 || sms != 0 {
		t.Fatalf("SIM và tin nhắn phải bị xoá: modems=%d sms=%d", modems, sms)
	}

	var hist struct {
		Data  []repository.AssignmentRow `json:"data"`
		Total int64                      `json:"total"`
	}
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/sim-assignments?iccid=SIM-A", e.admin, nil).Body.Bytes(), &hist)
	if hist.Total != 1 {
		t.Fatalf("xoá SIM phải GIỮ lịch sử giao nhận, còn %d dòng", hist.Total)
	}
	row := hist.Data[0]
	if row.StaffName != "Lan" || !row.SimDeleted || row.PhoneNumber != "0900000001" || row.ReleasedAt == nil ||
		row.ReleaseNote != repository.SimDeletedNote || row.ReleasedBy != "sep" || row.Note != "OTP ngân hàng" {
		t.Fatalf("dòng lịch sử sau khi xoá SIM = %+v", row)
	}
	var list []repository.StaffWithSIMs
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/staff", e.admin, nil).Body.Bytes(), &list)
	if len(list) != 1 || len(list[0].SIMs) != 0 {
		t.Fatalf("Lan không còn đứng tên SIM đã xoá: %+v", list)
	}
}

func (e *staffEnv) upload(token, filename string, content []byte, apply bool) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if content != nil {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			e.t.Fatal(err)
		}
		_, _ = part.Write(content)
	}
	if apply {
		_ = mw.WriteField("apply", "1")
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/staff/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func TestImportStaffFromKeHubExport(t *testing.T) {
	e := newStaffEnv(t)
	h := NewStaffHandler(e.db)
	e.r.POST("/api/v1/staff/import", AuthMiddleware(e.db), audit.Middleware(e.db), AdminOnly(), h.Import)
	lan := e.addStaff("Lan")
	e.db.Model(&model.Staff{}).Where("id = ?", lan).Update("code", "NV012")
	call(e.r, http.MethodPost, "/api/v1/modems/SIM-A/holder", e.admin, gin.H{"staff_id": lan})

	// Đúng dạng file KeHub xuất (rút gọn), có cột nhạy cảm.
	csv := []byte("\xef\xbb\xbfMã NV,Họ tên,Phòng ban,Email,Trạng thái,Số CCCD,STK ngân hàng\r\n" +
		"NV012,Nguyễn Thị Lan,Kế toán,lan@cty.vn,Nghỉ việc,079123456789,0011223344\r\n" +
		"NV027,Trần Văn Minh,CSKH,minh@cty.vn,Chính thức,079000000001,9988776655\r\n" +
		",,,,,,\r\n" +
		"NV031,,Kho,,Thử việc,,\r\n")

	if w := e.upload(e.viewer, "nv.csv", csv, false); w.Code != http.StatusForbidden {
		t.Fatalf("user thường nhập file = %d", w.Code)
	}
	if w := e.upload(e.admin, "", nil, false); w.Code != http.StatusBadRequest {
		t.Fatalf("thiếu file = %d", w.Code)
	}
	if w := e.upload(e.admin, "anh.png", []byte("\x89PNG\r\n\x1a\n\x00\x00"), false); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Không đọc được file") {
		t.Fatalf("file không phải bảng tính = %d %s", w.Code, w.Body.String())
	}

	type reply struct {
		Result   repository.ImportResult `json:"result"`
		Columns  []string                `json:"columns"`
		Warnings []string                `json:"warnings"`
	}
	// Xem trước: không ghi.
	w := e.upload(e.admin, "nhan-vien.csv", csv, false)
	var preview reply
	_ = json.Unmarshal(w.Body.Bytes(), &preview)
	if w.Code != http.StatusOK || preview.Result.Applied || preview.Result.Created != 1 || preview.Result.Left != 1 || preview.Result.LeftHolding != 1 {
		t.Fatalf("xem trước = %d %s", w.Code, w.Body.String())
	}
	if strings.Join(preview.Columns, ",") != "Mã NV,Họ tên,Phòng ban,Email,Trạng thái" || len(preview.Warnings) != 1 || preview.Warnings[0] != "Dòng 5: không có họ tên" {
		t.Fatalf("cột = %v, cảnh báo = %v", preview.Columns, preview.Warnings)
	}
	for _, secret := range []string{"079123456789", "0011223344", "9988776655"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("phản hồi lộ dữ liệu nhạy cảm của file: %s", secret)
		}
	}
	var n int64
	e.db.Model(&model.Staff{}).Count(&n)
	if n != 1 {
		t.Fatalf("xem trước mà đã ghi: %d nhân sự", n)
	}

	// Áp dụng.
	w = e.upload(e.admin, "nhan-vien.csv", csv, true)
	var applied reply
	_ = json.Unmarshal(w.Body.Bytes(), &applied)
	if w.Code != http.StatusOK || !applied.Result.Applied || applied.Result.Created != 1 || applied.Result.Left != 1 {
		t.Fatalf("áp dụng = %d %s", w.Code, w.Body.String())
	}
	var list []repository.StaffWithSIMs
	_ = json.Unmarshal(call(e.r, http.MethodGet, "/api/v1/staff", e.admin, nil).Body.Bytes(), &list)
	byName := map[string]repository.StaffWithSIMs{}
	for _, s := range list {
		byName[s.Name] = s
	}
	lanNow, minh := byName["Nguyễn Thị Lan"], byName["Trần Văn Minh"]
	if len(list) != 2 || lanNow.Active || len(lanNow.SIMs) != 1 || lanNow.Department != "Kế toán" || lanNow.Email != "lan@cty.vn" {
		t.Fatalf("Lan sau khi nhập (nghỉ nhưng vẫn đứng tên SIM để bàn giao) = %+v", lanNow)
	}
	if !minh.Active || minh.Code != "NV027" || minh.Department != "CSKH" {
		t.Fatalf("Minh = %+v", minh)
	}

	// Nhật ký ghi lượt nhập, KHÔNG chép nội dung file.
	var logs []model.AuditLog
	for i := 0; i < 50; i++ {
		e.db.Where("action = ?", "staff.import").Order("id").Find(&logs)
		if len(logs) >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	found := false
	for _, l := range logs {
		if strings.Contains(l.Detail, "079123456789") || strings.Contains(l.Detail, "Nguyễn") {
			t.Fatalf("nhật ký chép nội dung file: %q", l.Detail)
		}
		if l.Status == 200 && l.Target == "thêm 1, sửa 0, nghỉ 1, làm lại 0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nhật ký thiếu dòng tóm tắt lượt nhập: %+v", logs)
	}
}
