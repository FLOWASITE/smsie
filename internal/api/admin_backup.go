package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/backup"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

const maxRestoreBytes int64 = 512 << 20

type AdminBackupHandler struct {
	db     *gorm.DB
	driver string
	dsn    string
	cfg    config.BackupConfig
}

// NewAdminBackupHandler: dsn = đường dẫn file SQLite (để đặt file staging cạnh DB thật).
func NewAdminBackupHandler(db *gorm.DB, driver, dsn string, cfg config.BackupConfig) *AdminBackupHandler {
	return &AdminBackupHandler{db: db, driver: strings.ToLower(strings.TrimSpace(driver)), dsn: dsn, cfg: cfg}
}

func (h *AdminBackupHandler) sqliteOnly(c *gin.Context) bool {
	actor, ok := getActor(c)
	if !ok || actor.User == nil || actor.User.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return false
	}
	if h.driver != "" && h.driver != "sqlite" {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "Backup currently supports SQLite only"})
		return false
	}
	return true
}

func (h *AdminBackupHandler) Download(c *gin.Context) {
	if !h.sqliteOnly(c) {
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

// List — GET /admin/backups: danh sách bản (mới nhất trước) + lịch.
func (h *AdminBackupHandler) List(c *gin.Context) {
	if !h.sqliteOnly(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": backup.List(h.cfg.Dir), "schedule": h.cfg})
}

// RunNow — POST /admin/backups/run: sao lưu ngay, đồng bộ.
func (h *AdminBackupHandler) RunNow(c *gin.Context) {
	if !h.sqliteOnly(c) {
		return
	}
	res, err := (&backup.Scheduler{DB: h.db, Dir: h.cfg.Dir, Keep: h.cfg.Keep}).Once()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// Get — GET /admin/backups/:name: tải một bản; tên phải khớp NameRe (chống path traversal).
func (h *AdminBackupHandler) Get(c *gin.Context) {
	if !h.sqliteOnly(c) {
		return
	}
	name := c.Param("name")
	if !backup.NameRe.MatchString(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid backup name"})
		return
	}
	path := filepath.Join(h.cfg.Dir, name)
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "backup not found"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(path, name)
}

// Restore — POST /admin/restore: multipart `file` (≤512 MB) hoặc JSON {name}. Kiểm header +
// integrity → ghi <dsn>.restore-pending → 202. Dưới Windows Service (SMSIE_SERVICE=1) tự thoát
// mã 3 sau 2 s để WinSW khởi động lại; ngoài service thì khởi động lại tay.
// Audit `restore.staged` do middleware ghi (route nằm trong bảng action).
func (h *AdminBackupHandler) Restore(c *gin.Context) {
	if !h.sqliteOnly(c) {
		return
	}
	stage := backup.StagePath(h.dsn)
	tmp := stage + ".upload"
	os.Remove(tmp)
	defer os.Remove(tmp)

	if strings.HasPrefix(c.ContentType(), "multipart/") {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRestoreBytes+(1<<20))
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file is required (≤ 512 MB)"})
			return
		}
		defer file.Close()
		if header.Size <= 0 || header.Size > maxRestoreBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file must be between 1 byte and 512 MB"})
			return
		}
		out, err := os.Create(tmp)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not stage file"})
			return
		}
		_, err = io.Copy(out, file)
		out.Close()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not stage file"})
			return
		}
	} else {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || !backup.NameRe.MatchString(body.Name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must be an existing backup file name"})
			return
		}
		src, err := os.Open(filepath.Join(h.cfg.Dir, body.Name))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "backup not found"})
			return
		}
		out, err := os.Create(tmp)
		if err == nil {
			_, err = io.Copy(out, src)
			out.Close()
		}
		src.Close()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not stage file"})
			return
		}
	}

	if err := backup.Integrity(tmp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not a valid SQLite database: " + err.Error()})
		return
	}
	if err := os.Rename(tmp, stage); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not stage file"})
		return
	}
	service := os.Getenv("SMSIE_SERVICE") == "1"
	if service {
		logger.Log.Warnf("restore: đã xếp %s, thoát mã 3 sau 2 s để service khởi động lại", stage)
		time.AfterFunc(2*time.Second, func() { os.Exit(3) })
	} else {
		logger.Log.Warnf("restore: đã xếp %s — khởi động lại tay để áp", stage)
	}
	c.JSON(http.StatusAccepted, gin.H{"restart_required": true, "service": service})
}
