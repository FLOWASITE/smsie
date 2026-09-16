package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/balance"
	"github.com/pccr10001/smsie/internal/repository"
	"gorm.io/gorm"
)

type BalanceHandler struct {
	db        *gorm.DB
	sched     *balance.Scheduler
	repo      *repository.BalanceRepository
	evalDelay time.Duration // RunNow: chờ modem trả *101# trước khi đánh giá; test đặt 0
}

func NewBalanceHandler(db *gorm.DB, sched *balance.Scheduler) *BalanceHandler {
	return &BalanceHandler{db: db, sched: sched, repo: repository.NewBalanceRepository(db), evalDelay: 20 * time.Second}
}

// Status: admin thấy hết, user thường chỉ SIM mình được xem (cùng cách lọc với BayHandler.List).
func (h *BalanceHandler) Status(c *gin.Context) {
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
	items, err := h.sched.Status()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to evaluate balance"})
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
	c.JSON(http.StatusOK, items)
}

func (h *BalanceHandler) Alerts(c *gin.Context) {
	if !h.admin(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if size > 500 {
		size = 500
	}
	alerts, total, err := h.repo.ListAlerts(page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list balance alerts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alerts, "total": total, "page": page, "page_size": size})
}

// RunNow: trả 202 ngay với số SIM đủ điều kiện; gửi *101# rồi đánh giá chạy nền.
func (h *BalanceHandler) RunNow(c *gin.Context) {
	if !h.admin(c) {
		return
	}
	ws := h.sched.Eligible()
	go func() {
		h.sched.Request(ws, nil)
		time.Sleep(h.evalDelay)
		h.sched.Evaluate()
	}()
	c.JSON(http.StatusAccepted, gin.H{"requested": len(ws)})
}

func (h *BalanceHandler) admin(c *gin.Context) bool {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return false
	}
	return true
}
