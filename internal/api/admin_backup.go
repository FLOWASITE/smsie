package api

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdminBackupHandler struct {
	db     *gorm.DB
	driver string
}

func NewAdminBackupHandler(db *gorm.DB, driver string) *AdminBackupHandler {
	return &AdminBackupHandler{db: db, driver: strings.ToLower(strings.TrimSpace(driver))}
}

func (h *AdminBackupHandler) Download(c *gin.Context) {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}
	if h.driver != "" && h.driver != "sqlite" {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "Backup download currently supports SQLite only"})
		return
	}

	temp, err := os.CreateTemp("", "smsie-backup-*.db")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not prepare backup"})
		return
	}
	path := temp.Name()
	temp.Close()
	os.Remove(path)
	defer os.Remove(path)

	escapedPath := strings.ReplaceAll(path, "'", "''")
	if err := h.db.Exec("VACUUM INTO '" + escapedPath + "'").Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create backup"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(path, "smsie-backup-"+time.Now().Format("20060102-150405")+".db")
}
