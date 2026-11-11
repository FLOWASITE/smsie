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
	"gorm.io/gorm"
)

// StaffHandler — quản lý nhân sự giữ SIM: ai đang dùng SIM nào, đổi người giữ, bàn giao, lịch sử giao nhận.
//
//	GET    /api/v1/sim-holders             người giữ hiện tại của các SIM người gọi được xem
//	GET    /api/v1/staff                   (admin) nhân sự + SIM đang giữ
//	POST   /api/v1/staff                   (admin) thêm nhân sự
//	PUT    /api/v1/staff/:id               (admin) sửa hồ sơ
//	DELETE /api/v1/staff/:id               (admin) xoá — chỉ khi chưa từng giữ SIM
//	POST   /api/v1/staff/:id/leave         (admin) đánh dấu nghỉ việc {release_sims}
//	POST   /api/v1/staff/:id/return        (admin) làm lại
//	POST   /api/v1/staff/:id/transfer      (admin) bàn giao mọi SIM sang người khác {to_staff_id, note}
//	POST   /api/v1/modems/:iccid/holder    (admin) giao SIM / đổi người giữ {staff_id, note}
//	DELETE /api/v1/modems/:iccid/holder    (admin) thu hồi SIM {note}
//	GET    /api/v1/sim-assignments         (admin) lịch sử giao nhận ?iccid=&staff_id=&open=1&page=&page_size=
type StaffHandler struct {
	db   *gorm.DB
	repo *repository.StaffRepository
	now  func() time.Time
}

func NewStaffHandler(db *gorm.DB) *StaffHandler {
	return &StaffHandler{db: db, repo: repository.NewStaffRepository(db), now: time.Now}
}

type staffInput struct {
	Name       string `json:"name"`
	Code       string `json:"code"`
	Department string `json:"department"`
	Phone      string `json:"phone"`
	Email      string `json:"email"`
	Note       string `json:"note"`
}

// clean cắt khoảng trắng và kiểm độ dài theo cột DB; trả thông báo lỗi nếu không hợp lệ.
func (in *staffInput) clean() string {
	in.Name = strings.TrimSpace(in.Name)
	in.Code = strings.TrimSpace(in.Code)
	in.Department = strings.TrimSpace(in.Department)
	in.Phone = strings.TrimSpace(in.Phone)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Note = strings.TrimSpace(in.Note)
	switch {
	case in.Name == "":
		return "name is required"
	case len([]rune(in.Name)) > 120:
		return "name is too long (max 120)"
	case len([]rune(in.Code)) > 40:
		return "code is too long (max 40)"
	case len([]rune(in.Department)) > 120:
		return "department is too long (max 120)"
	case len(in.Phone) > 32:
		return "phone is too long (max 32)"
	case in.Email != "" && !looksLikeEmail(in.Email):
		return "email is not valid"
	case len([]rune(in.Note)) > 500:
		return "note is too long (max 500)"
	}
	return ""
}

func (in staffInput) model() model.Staff {
	return model.Staff{Name: in.Name, Code: in.Code, Department: in.Department, Phone: in.Phone, Email: in.Email, Note: in.Note}
}

func staffActorName(c *gin.Context) string {
	if actor, ok := getActor(c); ok && actor.User != nil {
		return actor.User.Username
	}
	return ""
}

func staffID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid staff id"})
		return 0, false
	}
	return uint(id), true
}

func trimNote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, len([]rune(s)) <= 500
}

// staffError đổi lỗi nghiệp vụ thành HTTP status + mã lỗi ổn định cho giao diện.
func staffError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrStaffNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "staff not found", "code": "staff_not_found"})
	case errors.Is(err, repository.ErrSIMNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "sim not found", "code": "sim_not_found"})
	case errors.Is(err, repository.ErrStaffLeft):
		c.JSON(http.StatusConflict, gin.H{"error": "staff has left; mark them as returned first", "code": "staff_left"})
	case errors.Is(err, repository.ErrSIMNotAssigned):
		c.JSON(http.StatusConflict, gin.H{"error": "sim is not assigned to anyone", "code": "sim_not_assigned"})
	case errors.Is(err, repository.ErrStaffHoldsSIMs):
		c.JSON(http.StatusConflict, gin.H{"error": "staff still holds sims; transfer or release them first", "code": "staff_holds_sims"})
	case errors.Is(err, repository.ErrStaffHasHistory):
		c.JSON(http.StatusConflict, gin.H{"error": "staff has assignment history; mark them as left instead of deleting", "code": "staff_has_history"})
	case errors.Is(err, repository.ErrSameStaff):
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and target staff must differ", "code": "same_staff"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "staff operation failed"})
	}
}

// Holders — người giữ hiện tại, lọc theo SIM người gọi được xem (cùng luật với /bays).
func (h *StaffHandler) Holders(c *gin.Context) {
	actor, ok := getActor(c)
	if !ok || actor.User == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !permissionFlagFromKey(actor.APIKey, PermViewSMS) {
		c.JSON(http.StatusForbidden, gin.H{"error": "API key permission denied"})
		return
	}
	allowed, err := allowedICCIDsForPermission(h.db, actor.User, PermViewSMS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "permission check failed"})
		return
	}
	holdings, err := h.repo.Holdings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load holders"})
		return
	}
	if actor.User.Role != "admin" && !hasWildcardICCID(allowed) {
		set := map[string]struct{}{}
		for _, iccid := range allowed {
			set[iccid] = struct{}{}
		}
		visible := holdings[:0]
		for _, item := range holdings {
			if _, ok := set[item.ICCID]; ok {
				visible = append(visible, item)
			}
		}
		holdings = visible
	}
	c.JSON(http.StatusOK, holdings)
}

func (h *StaffHandler) List(c *gin.Context) {
	items, err := h.repo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load staff"})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *StaffHandler) Create(c *gin.Context) {
	var in staffInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if msg := in.clean(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	staff := in.model()
	if err := h.repo.Create(&staff); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create staff"})
		return
	}
	c.Set("audit_target", staff.Name)
	c.JSON(http.StatusCreated, staff)
}

func (h *StaffHandler) Update(c *gin.Context) {
	id, ok := staffID(c)
	if !ok {
		return
	}
	var in staffInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if msg := in.clean(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	staff, err := h.repo.UpdateProfile(id, in.model())
	if err != nil {
		staffError(c, err)
		return
	}
	c.Set("audit_target", staff.Name)
	c.JSON(http.StatusOK, staff)
}

func (h *StaffHandler) Delete(c *gin.Context) {
	id, ok := staffID(c)
	if !ok {
		return
	}
	if staff, err := h.repo.Get(id); err == nil {
		c.Set("audit_target", staff.Name)
	}
	if err := h.repo.Delete(id); err != nil {
		staffError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *StaffHandler) Leave(c *gin.Context) {
	id, ok := staffID(c)
	if !ok {
		return
	}
	var req struct {
		ReleaseSIMs bool `json:"release_sims"`
	}
	_ = c.ShouldBindJSON(&req) // thân rỗng = không thu hồi hộ
	if staff, err := h.repo.Get(id); err == nil {
		c.Set("audit_target", staff.Name)
	}
	released, err := h.repo.SetLeft(id, req.ReleaseSIMs, staffActorName(c), h.now())
	if err != nil {
		staffError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "left", "released": released})
}

func (h *StaffHandler) Return(c *gin.Context) {
	id, ok := staffID(c)
	if !ok {
		return
	}
	if err := h.repo.SetReturned(id); err != nil {
		staffError(c, err)
		return
	}
	if staff, err := h.repo.Get(id); err == nil {
		c.Set("audit_target", staff.Name)
	}
	c.JSON(http.StatusOK, gin.H{"status": "active"})
}

func (h *StaffHandler) Transfer(c *gin.Context) {
	id, ok := staffID(c)
	if !ok {
		return
	}
	var req struct {
		ToStaffID uint   `json:"to_staff_id"`
		Note      string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ToStaffID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to_staff_id is required"})
		return
	}
	note, fits := trimNote(req.Note)
	if !fits {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note is too long (max 500)"})
		return
	}
	if target, err := h.repo.Get(req.ToStaffID); err == nil {
		c.Set("audit_target", target.Name)
	}
	moved, err := h.repo.Transfer(id, req.ToStaffID, staffActorName(c), note, h.now())
	if err != nil {
		staffError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "transferred", "moved": moved})
}

// Assign — giao SIM cho nhân sự; SIM đang có người khác giữ thì đây là đổi người giữ.
func (h *StaffHandler) Assign(c *gin.Context) {
	var req struct {
		StaffID uint   `json:"staff_id"`
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.StaffID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "staff_id is required"})
		return
	}
	note, fits := trimNote(req.Note)
	if !fits {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note is too long (max 500)"})
		return
	}
	result, err := h.repo.Assign(c.Param("iccid"), req.StaffID, staffActorName(c), note, h.now())
	if err != nil {
		staffError(c, err)
		return
	}
	c.Set("audit_target", result.Staff.Name)
	c.JSON(http.StatusOK, result)
}

// Release — thu hồi SIM, không còn ai giữ.
func (h *StaffHandler) Release(c *gin.Context) {
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	note, fits := trimNote(req.Note)
	if !fits {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note is too long (max 500)"})
		return
	}
	released, err := h.repo.Release(c.Param("iccid"), staffActorName(c), note, h.now())
	if err != nil {
		staffError(c, err)
		return
	}
	if staff, err := h.repo.Get(released.StaffID); err == nil {
		c.Set("audit_target", staff.Name)
	}
	c.JSON(http.StatusOK, gin.H{"status": "released", "assignment_id": released.ID})
}

func (h *StaffHandler) History(c *gin.Context) {
	f := repository.AssignmentFilter{
		ICCID:    strings.TrimSpace(c.Query("iccid")),
		OpenOnly: c.Query("open") == "1",
		Page:     boundedPositiveInt(c.Query("page"), 1, 1, 1000000),
		Size:     boundedPositiveInt(c.Query("page_size"), 50, 1, 500),
	}
	if raw := c.Query("staff_id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "staff_id must be a number"})
			return
		}
		f.StaffID = uint(id)
	}
	rows, total, err := h.repo.History(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load assignments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": f.Page, "page_size": f.Size})
}
