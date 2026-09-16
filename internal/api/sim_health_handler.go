package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/simhealth"
	"gorm.io/gorm"
)

type SimHealthHandler struct {
	db   *gorm.DB
	svc  *simhealth.Service
	repo *repository.SimAlertRepository
}

func NewSimHealthHandler(db *gorm.DB, svc *simhealth.Service) *SimHealthHandler {
	return &SimHealthHandler{db: db, svc: svc, repo: repository.NewSimAlertRepository(db)}
}

// Status: admin thấy hết, user thường chỉ SIM mình được xem (cùng cách lọc với BalanceHandler.Status).
func (h *SimHealthHandler) Status(c *gin.Context) {
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to evaluate SIM health"})
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

func (h *SimHealthHandler) Alerts(c *gin.Context) {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if size > 500 {
		size = 500
	}
	alerts, total, err := h.repo.List(page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list SIM alerts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alerts, "total": total, "page": page, "page_size": size})
}
