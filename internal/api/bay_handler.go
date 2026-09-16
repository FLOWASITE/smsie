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
	actor, ok := getActor(c)
	if !ok || actor.User == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !permissionFlagFromKey(actor.APIKey, PermViewSMS) {
		c.JSON(http.StatusForbidden, gin.H{"error": "API key permission denied"})
		return
	}
	// Non-admin chỉ thấy khe trống + khe chứa SIM mình được xem (cùng cách lọc với ListModems).
	allowed, err := allowedICCIDsForPermission(h.db, actor.User, PermViewSMS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "permission check failed"})
		return
	}
	canSee := func(iccid string) bool {
		if actor.User.Role == "admin" || hasWildcardICCID(allowed) {
			return true
		}
		for _, a := range allowed {
			if a == iccid {
				return true
			}
		}
		return false
	}
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
	// ponytail: MAX(id) thay MAX(detected_at) — SQLite trả aggregate datetime dạng text, không scan được vào time.Time;
	// id tăng dần theo thời gian ghi nên tương đương.
	var lastEvents []model.SimSlotEvent
	h.db.Where("id IN (?)", h.db.Model(&model.SimSlotEvent{}).Select("MAX(id)").Where("event <> ?", model.SlotEventRemoved).Group("iccid")).Find(&lastEvents)
	lastEventAt := map[string]time.Time{}
	for _, e := range lastEvents {
		lastEventAt[e.ICCID] = e.DetectedAt
	}

	out := make([]bayView, 0, len(bays))
	for _, b := range bays {
		if b.CurrentICCID != "" && !canSee(b.CurrentICCID) {
			continue
		}
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
