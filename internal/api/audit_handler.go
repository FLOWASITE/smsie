package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

type AuditHandler struct{ db *gorm.DB }

func NewAuditHandler(db *gorm.DB) *AuditHandler { return &AuditHandler{db: db} }

// List — GET /api/v1/audit?page=&page_size=&iccid=&username=&action=&from=&to= (admin).
func (h *AuditHandler) List(c *gin.Context) {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}
	q := h.db.Model(&model.AuditLog{})
	for _, k := range []string{"iccid", "username", "action"} {
		if v := c.Query(k); v != "" {
			q = q.Where(k+" = ?", v)
		}
	}
	for name, op := range map[string]string{"from": ">=", "to": "<"} {
		if s := c.Query(name); s != "" {
			t, err := time.ParseInLocation("2006-01-02", s, time.Local)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be YYYY-MM-DD"})
				return
			}
			if name == "to" {
				t = t.Add(24 * time.Hour)
			}
			q = q.Where("at "+op+" ?", t)
		}
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	var total int64
	var rows []model.AuditLog
	if err := q.Count(&total).Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list audit logs"})
		return
	}
	if rows == nil {
		rows = []model.AuditLog{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": size})
}
