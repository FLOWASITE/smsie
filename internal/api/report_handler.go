package api

import (
	"bytes"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/report"
	"gorm.io/gorm"
)

type ReportHandler struct{ db *gorm.DB }

func NewReportHandler(db *gorm.DB) *ReportHandler { return &ReportHandler{db: db} }

// Monthly — GET /api/v1/reports/monthly?month=YYYY-MM&format=json|csv.
// Admin thấy hết; user thường lọc theo quyền xem SIM (cùng cách với BalanceHandler.Status).
func (h *ReportHandler) Monthly(c *gin.Context) {
	actor, ok := getActor(c)
	if !ok || actor.User == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !permissionFlagFromKey(actor.APIKey, PermViewSMS) {
		c.JSON(http.StatusForbidden, gin.H{"error": "API key permission denied"})
		return
	}
	month := time.Now()
	if s := c.Query("month"); s != "" {
		t, err := time.ParseInLocation("2006-01", s, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "month must be YYYY-MM"})
			return
		}
		month = t
	}
	allowed, err := allowedICCIDsForPermission(h.db, actor.User, PermViewSMS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "permission check failed"})
		return
	}
	if actor.User.Role == "admin" || hasWildcardICCID(allowed) {
		allowed = nil
	}
	rep, err := report.Monthly(h.db, month, allowed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build report"})
		return
	}
	if c.Query("format") != "csv" {
		c.JSON(http.StatusOK, rep)
		return
	}
	var buf bytes.Buffer
	if err := report.WriteCSV(&buf, rep); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write csv"})
		return
	}
	c.Header("Content-Disposition", `attachment; filename=smsie-bao-cao-`+rep.Month+`.csv`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}
