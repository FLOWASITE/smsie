package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/keepalive"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

type KeepaliveHandler struct {
	db   *gorm.DB
	svc  *keepalive.Service
	repo *repository.KeepaliveRepository
}

func NewKeepaliveHandler(db *gorm.DB, svc *keepalive.Service) *KeepaliveHandler {
	return &KeepaliveHandler{db: db, svc: svc, repo: repository.NewKeepaliveRepository(db)}
}

// Status: {"config": {...}, "items": [...]}; admin thấy hết, user thường chỉ SIM mình được xem (cùng cách lọc với BalanceHandler.Status).
func (h *KeepaliveHandler) Status(c *gin.Context) {
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
	items, err := h.svc.Collect()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to collect keepalive status"})
		return
	}
	if actor.User.Role != "admin" && !hasWildcardICCID(allowed) {
		set := map[string]bool{}
		for _, a := range allowed {
			set[a] = true
		}
		kept := items[:0]
		for _, it := range items {
			if set[it.ICCID] {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	cfg := h.svc.Config()
	c.JSON(http.StatusOK, gin.H{
		"config": gin.H{"enabled": cfg.Enabled, "run_hour": cfg.RunHour, "interval_days": cfg.IntervalDays, "max_per_month": cfg.MaxPerMonth},
		"items":  items,
	})
}

func (h *KeepaliveHandler) Runs(c *gin.Context) {
	if !h.admin(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if size > 500 {
		size = 500
	}
	runs, total, err := h.repo.List(c.Query("iccid"), page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list keepalive runs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": runs, "total": total, "page": page, "page_size": size})
}

// RunNow: body {iccid?}. Có iccid → chạy đồng bộ (bỏ qua chu kỳ, vẫn trần tháng) trả run; không → RunAll nền, 202.
func (h *KeepaliveHandler) RunNow(c *gin.Context) {
	if !h.admin(c) {
		return
	}
	if !h.svc.Config().Enabled {
		c.JSON(http.StatusConflict, gin.H{"error": keepalive.ErrDisabled.Error()})
		return
	}
	var req struct {
		ICCID string `json:"iccid"`
	}
	_ = c.ShouldBindJSON(&req) // body rỗng = cả khay
	if req.ICCID == "" {
		go func() {
			if _, err := h.svc.RunAll(); err != nil {
				logger.Log.Errorf("keepalive: RunAll lỗi: %v", err)
			}
		}()
		c.JSON(http.StatusAccepted, gin.H{"started": true})
		return
	}
	run, err := h.svc.RunOne(req.ICCID, true)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Modem not found"})
	case err != nil:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, run)
	}
}

func (h *KeepaliveHandler) admin(c *gin.Context) bool {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return false
	}
	return true
}
