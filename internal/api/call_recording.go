package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

const maxCallRecordingBytes int64 = 100 << 20

var callRecordingExtensions = map[string]string{
	"audio/webm":  ".webm",
	"audio/ogg":   ".ogg",
	"audio/mp4":   ".m4a",
	"audio/mpeg":  ".mp3",
	"audio/wav":   ".wav",
	"audio/x-wav": ".wav",
}

type CallRecordingHandler struct {
	db   *gorm.DB
	root string
}

func NewCallRecordingHandler(db *gorm.DB, root string) *CallRecordingHandler {
	return &CallRecordingHandler{db: db, root: root}
}

func (h *CallRecordingHandler) Upload(c *gin.Context) {
	iccid := strings.TrimSpace(c.Param("iccid"))
	if !enforceICCIDPermission(c, h.db, iccid, PermMakeCall) {
		return
	}
	actor, _ := getActor(c)

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCallRecordingBytes+(1<<20))
	file, header, err := c.Request.FormFile("recording")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recording file is required"})
		return
	}
	defer file.Close()

	contentType := strings.ToLower(strings.TrimSpace(strings.Split(header.Header.Get("Content-Type"), ";")[0]))
	extension, ok := callRecordingExtensions[contentType]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported audio format"})
		return
	}
	if header.Size <= 0 || header.Size > maxCallRecordingBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recording must be between 1 byte and 100 MB"})
		return
	}

	duration, err := strconv.Atoi(strings.TrimSpace(c.PostForm("duration_seconds")))
	if err != nil || duration < 0 || duration > 24*60*60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration_seconds must be between 0 and 86400"})
		return
	}
	phone := strings.TrimSpace(c.PostForm("phone"))
	if len(phone) > 32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone is too long"})
		return
	}

	fileName, err := randomRecordingFileName(extension)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate recording"})
		return
	}
	if err := os.MkdirAll(h.root, 0700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare recording storage"})
		return
	}
	path := filepath.Join(h.root, fileName)
	destination, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create recording"})
		return
	}
	written, copyErr := io.Copy(destination, io.LimitReader(file, maxCallRecordingBytes+1))
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil || written > maxCallRecordingBytes {
		_ = os.Remove(path)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to save recording"})
		return
	}

	recording := model.CallRecording{
		ICCID:           iccid,
		Phone:           phone,
		FileName:        fileName,
		ContentType:     contentType,
		SizeBytes:       written,
		DurationSeconds: duration,
		CreatedBy:       actor.User.ID,
	}
	if err := h.db.Create(&recording).Error; err != nil {
		_ = os.Remove(path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to index recording"})
		return
	}
	c.JSON(http.StatusCreated, recording)
}

func (h *CallRecordingHandler) List(c *gin.Context) {
	iccid := strings.TrimSpace(c.Param("iccid"))
	if !enforceICCIDPermission(c, h.db, iccid, PermMakeCall) {
		return
	}
	page := boundedPositiveInt(c.Query("page"), 1, 1, 1000000)
	pageSize := boundedPositiveInt(c.Query("pageSize"), 20, 1, 100)

	query := h.db.Model(&model.CallRecording{}).Where("iccid = ?", iccid)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count recordings"})
		return
	}
	var recordings []model.CallRecording
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&recordings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list recordings"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": recordings,
		"pagination": gin.H{
			"page":       page,
			"pageSize":   pageSize,
			"totalItems": total,
			"totalPages": (total + int64(pageSize) - 1) / int64(pageSize),
		},
	})
}

func (h *CallRecordingHandler) Download(c *gin.Context) {
	iccid := strings.TrimSpace(c.Param("iccid"))
	if !enforceICCIDPermission(c, h.db, iccid, PermMakeCall) {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recording id"})
		return
	}
	var recording model.CallRecording
	if err := h.db.Where("id = ? AND iccid = ?", id, iccid).First(&recording).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "recording not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load recording"})
		return
	}
	if recording.FileName == "" || filepath.Base(recording.FileName) != recording.FileName {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid recording path"})
		return
	}
	path := filepath.Join(h.root, recording.FileName)
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording file not found"})
		return
	}
	c.Header("Content-Type", recording.ContentType)
	c.FileAttachment(path, fmt.Sprintf("call-%d%s", recording.ID, filepath.Ext(recording.FileName)))
}

func randomRecordingFileName(extension string) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes) + extension, nil
}

func boundedPositiveInt(raw string, fallback, min, max int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < min {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
