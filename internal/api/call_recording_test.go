package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func recordingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.UserModemPermission{}, &model.CallRecording{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Modem{ICCID: "sim-1"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func recordingUploadRequest(t *testing.T, contentType string, payload []byte) (*http.Request, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("phone", "0901234567")
	_ = writer.WriteField("duration_seconds", "42")
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="recording"; filename="call.webm"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(http.MethodPost, "/api/v1/modems/sim-1/call/recordings", &body), writer.FormDataContentType()
}

func TestCallRecordingUploadPersistsFileAndMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := recordingTestDB(t)
	root := t.TempDir()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request, contentType := recordingUploadRequest(t, "audio/webm;codecs=opus", []byte("recorded audio"))
	request.Header.Set("Content-Type", contentType)
	context.Request = request
	context.Params = gin.Params{{Key: "iccid", Value: "sim-1"}}
	context.Set("user", &model.User{ID: 7, Role: "admin", AllowedModems: "*"})

	NewCallRecordingHandler(db, root).Upload(context)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var saved model.CallRecording
	if err := db.First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.ICCID != "sim-1" || saved.Phone != "0901234567" || saved.DurationSeconds != 42 || saved.CreatedBy != 7 {
		t.Fatalf("recording = %#v", saved)
	}
	content, err := os.ReadFile(filepath.Join(root, saved.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "recorded audio" {
		t.Fatalf("content = %q", content)
	}
}

func TestCallRecordingUploadRejectsUnsupportedMedia(t *testing.T) {
	db := recordingTestDB(t)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request, contentType := recordingUploadRequest(t, "text/plain", []byte("not audio"))
	request.Header.Set("Content-Type", contentType)
	context.Request = request
	context.Params = gin.Params{{Key: "iccid", Value: "sim-1"}}
	context.Set("user", &model.User{ID: 7, Role: "admin", AllowedModems: "*"})

	NewCallRecordingHandler(db, t.TempDir()).Upload(context)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unsupported audio format") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestCallRecordingListIsScopedToModem(t *testing.T) {
	db := recordingTestDB(t)
	if err := db.Create(&model.CallRecording{ICCID: "sim-1", Phone: "0901234567", FileName: "one.webm", ContentType: "audio/webm"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CallRecording{ICCID: "sim-2", Phone: "0900000000", FileName: "two.webm", ContentType: "audio/webm"}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/modems/sim-1/call/recordings", nil)
	context.Params = gin.Params{{Key: "iccid", Value: "sim-1"}}
	context.Set("user", &model.User{ID: 7, Role: "admin", AllowedModems: "*"})

	NewCallRecordingHandler(db, t.TempDir()).List(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data []model.CallRecording `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].ICCID != "sim-1" {
		t.Fatalf("data = %#v", response.Data)
	}
}

func TestCallRecordingDownloadUsesIndexedFileOnly(t *testing.T) {
	db := recordingTestDB(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.webm"), []byte("audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	recording := model.CallRecording{ICCID: "sim-1", Phone: "0901234567", FileName: "one.webm", ContentType: "audio/webm"}
	if err := db.Create(&recording).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/modems/sim-1/call/recordings/1/file", nil)
	context.Params = gin.Params{{Key: "iccid", Value: "sim-1"}, {Key: "id", Value: strconv.FormatUint(uint64(recording.ID), 10)}}
	context.Set("user", &model.User{ID: 7, Role: "admin", AllowedModems: "*"})

	NewCallRecordingHandler(db, root).Download(context)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "audio bytes" {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
}
