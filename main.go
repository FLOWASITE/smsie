package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/api"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/auth"
	"github.com/pccr10001/smsie/internal/backup"
	"github.com/pccr10001/smsie/internal/balance"
	"github.com/pccr10001/smsie/internal/keepalive"
	"github.com/pccr10001/smsie/internal/calling"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/mccmnc"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/simhealth"
	"github.com/pccr10001/smsie/internal/worker"
	"github.com/pccr10001/smsie/pkg/logger"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	// 1. Load Config
	config.LoadConfig()

	// 2. Init Logger
	logger.InitLogger(config.AppConfig.Log.Level)
	logger.Log.Info("Starting SMS Dashboard...")

	// Load MCCMNC
	if err := mccmnc.LoadOperators("mcc_mnc.json"); err != nil {
		logger.Log.Warnf("Failed to load MCC/MNC data: %v", err)
	}

	// 3. Init Database — áp file khôi phục chờ (<dsn>.restore-pending) TRƯỚC khi mở DB.
	sqliteDSN := ""
	if config.AppConfig.Database.Driver != "mysql" {
		if sqliteDSN = config.AppConfig.Database.DSN; sqliteDSN == "" {
			sqliteDSN = "smsie_v2.db"
		}
	}
	restored := false
	if sqliteDSN != "" {
		applied, err := backup.ApplyPending(sqliteDSN, config.AppConfig.Backup.Dir)
		if err != nil {
			logger.Log.Errorf("restore: %v", err)
		}
		if applied {
			logger.Log.Warnf("restore: đã áp %s vào %s (bản cũ ở %s/pre-restore-*.db)", backup.StagePath(sqliteDSN), sqliteDSN, config.AppConfig.Backup.Dir)
			restored = true
		}
	}
	db := initDB()
	if restored {
		audit.Record(db, audit.Entry{Username: "system", Action: "restore.applied", Target: sqliteDSN, Status: 200})
	}

	// 4. Init Router
	if config.AppConfig.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	// Khoá ký JWT: lấy từ cấu hình, không có thì tự sinh và lưu trong DB. Phải xong trước khi nhận request.
	if err := auth.InitSecret(db, config.AppConfig.Auth.JWTSecret); err != nil {
		logger.Log.Fatalf("Failed to initialise JWT secret: %v", err)
	}

	r := gin.New()
	r.Use(gin.LoggerWithFormatter(accessLogLine), gin.Recovery())
	// Chỉ tin X-Forwarded-For từ các proxy đã khai (mặc định: cloudflared cùng máy). Tin mọi nguồn thì ai cũng
	// tự đặt được IP của mình, vô hiệu giới hạn đăng nhập theo IP và làm sai nhật ký.
	if err := r.SetTrustedProxies(config.AppConfig.Server.TrustedProxies); err != nil {
		logger.Log.Fatalf("server.trusted_proxies không hợp lệ: %v", err)
	}

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	// 5. Start Worker Manager
	wm := worker.NewManager(db)
	wm.Start()
	defer wm.Stop()

	stdLogger := log.New(os.Stdout, "[calling] ", log.LstdFlags|log.Lmicroseconds)
	callMgr, err := calling.NewManager(calling.Config{
		STUNServers: config.AppConfig.Calling.STUNServers,
		UDPPortMin:  config.AppConfig.Calling.UDPPortMin,
		UDPPortMax:  config.AppConfig.Calling.UDPPortMax,
		Audio: calling.AudioConfig{
			DeviceKeyword:    config.AppConfig.Calling.Audio.DeviceKeyword,
			OutputDeviceName: config.AppConfig.Calling.Audio.OutputDeviceName,
			SampleRate:       config.AppConfig.Calling.Audio.SampleRate,
			Channels:         config.AppConfig.Calling.Audio.Channels,
			BitsPerSample:    config.AppConfig.Calling.Audio.BitsPerSample,
			CaptureChunkMs:   config.AppConfig.Calling.Audio.CaptureChunkMs,
			PlaybackChunkMs:  config.AppConfig.Calling.Audio.PlaybackChunkMs,
		},
		SIP: calling.SIPConfig{
			RegisterExpires:    config.AppConfig.Calling.SIP.RegisterExpires,
			LocalHost:          config.AppConfig.Calling.SIP.LocalHost,
			LocalPort:          config.AppConfig.Calling.SIP.LocalPort,
			RTPBindIP:          config.AppConfig.Calling.SIP.RTPBindIP,
			RTPPortMin:         config.AppConfig.Calling.SIP.RTPPortMin,
			RTPPortMax:         config.AppConfig.Calling.SIP.RTPPortMax,
			InviteTimeoutSec:   config.AppConfig.Calling.SIP.InviteTimeoutSec,
			DTMFMethod:         config.AppConfig.Calling.SIP.DTMFMethod,
			DTMFDurationMillis: config.AppConfig.Calling.SIP.DTMFDurationMillis,
		},
	}, stdLogger)
	if err != nil {
		logger.Log.Fatalf("Failed to init calling manager: %v", err)
	}
	defer callMgr.CloseAll()

	registerSIPModemCallStateListener(wm, callMgr, stdLogger)

	sipSyncStop := make(chan struct{})
	defer close(sipSyncStop)
	go runSIPInboundSyncLoop(db, wm, callMgr, calling.SIPConfig{
		LocalHost:          config.AppConfig.Calling.SIP.LocalHost,
		LocalPort:          config.AppConfig.Calling.SIP.LocalPort,
		RTPBindIP:          config.AppConfig.Calling.SIP.RTPBindIP,
		RTPPortMin:         config.AppConfig.Calling.SIP.RTPPortMin,
		RTPPortMax:         config.AppConfig.Calling.SIP.RTPPortMax,
		InviteTimeoutSec:   config.AppConfig.Calling.SIP.InviteTimeoutSec,
		DTMFMethod:         config.AppConfig.Calling.SIP.DTMFMethod,
		DTMFDurationMillis: config.AppConfig.Calling.SIP.DTMFDurationMillis,
	}, stdLogger, sipSyncStop)

	webhookSvc := logic.NewWebhookService(repository.NewWebhookRepository(db))
	sched := balance.NewScheduler(db, wm, webhookSvc, config.AppConfig.Balance)
	simHealth := simhealth.NewService(db, wm, webhookSvc, config.AppConfig.SimHealth)
	if config.AppConfig.SimHealth.Enabled {
		sched.AddExtra(simHealth)
	}
	schedStop := make(chan struct{})
	defer close(schedStop)
	go sched.Run(schedStop)
	ks := keepalive.NewService(db, wm, nil, webhookSvc, config.AppConfig.Keepalive)
	go ks.Run(schedStop)
	// Sao lưu hằng ngày (chỉ SQLite; không chạy lúc boot).
	if config.AppConfig.Backup.Enabled {
		if sqliteDSN == "" {
			logger.Log.Warnf("backup: chỉ hỗ trợ SQLite, driver %s bỏ qua", config.AppConfig.Database.Driver)
		} else {
			bs := &backup.Scheduler{DB: db, Dir: config.AppConfig.Backup.Dir, Keep: config.AppConfig.Backup.Keep, Hour: config.AppConfig.Backup.Hour, Notify: webhookSvc.Broadcast}
			go bs.Run(schedStop)
		}
	}
	// Nhật ký hành động: dọn lúc khởi động + mỗi 24 h.
	go func() {
		for {
			if err := audit.Prune(db, config.AppConfig.Audit.KeepDays); err != nil {
				logger.Log.Warnf("audit: dọn nhật ký lỗi: %v", err)
			}
			select {
			case <-schedStop:
				return
			case <-time.After(24 * time.Hour):
			}
		}
	}()

	// 6. Start Server
	// Load Templates
	r.LoadHTMLGlob("web/templates/*")
	r.Static("/static", "./web/static")

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	// Setup Routes
	mh := api.NewModemHandler(db, wm, callMgr)
	bh := api.NewBayHandler(db, wm)
	balh := api.NewBalanceHandler(db, sched)
	kah := api.NewKeepaliveHandler(db, ks)
	shh := api.NewSimHealthHandler(db, simHealth)
	sh := api.NewSMSHandler(db)
	wh := api.NewWebhookHandler(db)
	uh := api.NewUserHandler(db)
	akh := api.NewAPIKeyHandler(db)
	backupHandler := api.NewAdminBackupHandler(db, config.AppConfig.Database.Driver, sqliteDSN, config.AppConfig.Backup)
	auditHandler := api.NewAuditHandler(db)
	reportHandler := api.NewReportHandler(db)
	recordingHandler := api.NewCallRecordingHandler(db, "recordings")
	mcpHTTP := api.NewMCPHTTPServer(db, wm)
	r.Any("/mcp", gin.WrapH(mcpHTTP.Handler()))

	// Đăng nhập bằng tài khoản Jione (OIDC): /oidc/login, /oidc/callback, /oidc/backchannel-logout…
	jioneHandler := api.NewJioneHandler(db, config.AppConfig.Jione)
	api.RegisterJioneRoutes(r, jioneHandler)
	if config.AppConfig.Jione.Enabled {
		if strings.TrimSpace(config.AppConfig.Jione.ClientID) == "" {
			logger.Log.Warn("jione.enabled = true nhưng thiếu jione.client_id — đăng nhập Jione đang tắt")
		} else {
			logger.Log.Infof("Đăng nhập Jione bật: issuer=%s client_id=%s", config.AppConfig.Jione.Issuer, config.AppConfig.Jione.ClientID)
		}
	}

	apiGroup := r.Group("/api/v1")
	{
		apiGroup.POST("/login", uh.Login)

		// Authenticated Routes
		authGroup := apiGroup.Group("/")
		authGroup.Use(api.AuthMiddleware(db))
		authGroup.Use(api.APIKeyAllowedOnly())
		authGroup.Use(audit.Middleware(db)) // sau AuthMiddleware; adminGroup lồng trong authGroup nên cũng được ghi
		{
			authGroup.POST("/change_password", uh.ChangePassword)
			authGroup.GET("/apikeys", akh.ListMyAPIKeys)
			authGroup.POST("/apikeys", akh.CreateMyAPIKey)
			authGroup.POST("/apikeys/:id/rotate", akh.RotateMyAPIKey)
			authGroup.DELETE("/apikeys/:id", akh.DeleteMyAPIKey)

			authGroup.GET("/modems", mh.ListModems)
			authGroup.GET("/bays", bh.List)
			authGroup.GET("/balance/status", balh.Status)
			authGroup.GET("/sim-health", shh.Status)
			authGroup.GET("/keepalive/status", kah.Status)
			authGroup.GET("/slot-events", bh.ListEvents)
			authGroup.GET("/modems/:iccid", mh.GetModem)
			authGroup.PUT("/modems/:iccid", mh.UpdateModem)
			authGroup.POST("/modems/:iccid/scan", mh.ScanNetworks)
			authGroup.POST("/modems/:iccid/operator", mh.SetOperator)
			authGroup.POST("/modems/:iccid/at", mh.ExecuteAT)
			authGroup.POST("/modems/:iccid/input", mh.ExecuteInput)
			authGroup.POST("/modems/:iccid/balance-check", mh.CheckBalance)
			authGroup.POST("/modems/:iccid/phone-lookup", mh.PhoneLookup)
			authGroup.GET("/modems/:iccid/phone-history", mh.PhoneHistory)
			authGroup.GET("/modems/:iccid/call/state", mh.GetCallState)
			authGroup.POST("/modems/:iccid/call/dial", mh.Dial)
			authGroup.POST("/modems/:iccid/call/hangup", mh.Hangup)
			authGroup.POST("/modems/:iccid/call/dtmf", mh.DTMF)
			authGroup.GET("/modems/:iccid/call/recordings", recordingHandler.List)
			authGroup.POST("/modems/:iccid/call/recordings", recordingHandler.Upload)
			authGroup.GET("/modems/:iccid/call/recordings/:id/file", recordingHandler.Download)
			authGroup.POST("/modems/:iccid/reboot", mh.Reboot)
			authGroup.POST("/modems/:iccid/send", mh.SendSMS)
			authGroup.GET("/reports/monthly", reportHandler.Monthly)
			authGroup.GET("/sms", sh.ListSMS)
			authGroup.GET("/modems/:iccid/ws", mh.WS)

			// Admin Only
			adminGroup := authGroup.Group("/")
			adminGroup.Use(api.AdminOnly())
			{
				adminGroup.GET("/webhooks", wh.ListWebhooks)
				adminGroup.POST("/webhooks", wh.CreateWebhook)
				adminGroup.DELETE("/webhooks/:id", wh.DeleteWebhook)
				adminGroup.DELETE("/modems/:iccid", mh.DeleteModem)
				adminGroup.PATCH("/modems/:iccid/profile", mh.UpdateProfile)
				adminGroup.PATCH("/bays/:imei", bh.Assign)
				adminGroup.GET("/balance/alerts", balh.Alerts)
				adminGroup.GET("/sim-health/alerts", shh.Alerts)
				adminGroup.POST("/balance/run", balh.RunNow)
				adminGroup.GET("/keepalive/runs", kah.Runs)
				adminGroup.POST("/keepalive/run", kah.RunNow)
				adminGroup.GET("/admin/backup", backupHandler.Download)
				adminGroup.GET("/admin/backups", backupHandler.List)
				adminGroup.POST("/admin/backups/run", backupHandler.RunNow)
				adminGroup.GET("/admin/backups/:name", backupHandler.Get)
				adminGroup.POST("/admin/restore", backupHandler.Restore)
				adminGroup.GET("/audit", auditHandler.List)

				adminGroup.GET("/users", uh.ListUsers)
				adminGroup.POST("/users", uh.CreateUser)
				adminGroup.GET("/users/:id/permissions", uh.ListUserPermissions)
				adminGroup.PUT("/users/:id/permissions", uh.UpdateUserPermissions)
				adminGroup.DELETE("/users/:id", uh.DeleteUser)
			}
		}
	}

	addr := config.AppConfig.Server.Port
	if addr == "" {
		addr = ":8080"
	}
	// Không đặt WriteTimeout/ReadTimeout tổng: WebSocket cuộc gọi và MCP wait_sms giữ kết nối lâu.
	srv := &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: 15 * time.Second}

	serveErr := make(chan error, 1)
	go func() {
		logger.Log.Infof("Server listening on %s", addr)
		serveErr <- srv.ListenAndServe()
	}()

	// Tắt êm: SIGINT/SIGTERM → ngừng nhận request, chờ tối đa 10 giây, rồi các defer ở trên dừng worker
	// modem, cuộc gọi và scheduler. Trước đây tiến trình bị giết thẳng, defer không bao giờ chạy.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Log.Errorf("Server failed: %v", err)
		}
	case sig := <-quit:
		logger.Log.Infof("Nhận tín hiệu %s, đang tắt…", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := srv.Shutdown(ctx); err != nil {
			logger.Log.Warnf("Tắt HTTP server chưa gọn: %v", err)
			_ = srv.Close()
		}
		cancel()
	}
}

// sensitiveQueryKeys — tham số URL mang bí mật: token WebSocket, code/state của OIDC. Không ghi giá trị vào log.
var sensitiveQueryKeys = map[string]bool{"token": true, "code": true, "state": true, "logout_token": true}

// redactQuery thay giá trị các tham số nhạy cảm trong "path?query" bằng "***".
func redactQuery(pathWithQuery string) string {
	i := strings.IndexByte(pathWithQuery, '?')
	if i < 0 {
		return pathWithQuery
	}
	parts := strings.Split(pathWithQuery[i+1:], "&")
	for n, part := range parts {
		key := part
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			key = part[:eq]
		}
		if decoded, err := url.QueryUnescape(key); err == nil {
			key = decoded
		}
		if sensitiveQueryKeys[strings.ToLower(key)] {
			parts[n] = key + "=***"
		}
	}
	return pathWithQuery[:i+1] + strings.Join(parts, "&")
}

// accessLogLine: dòng log truy cập cùng dạng với gin mặc định, nhưng đã che tham số nhạy cảm.
func accessLogLine(p gin.LogFormatterParams) string {
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %q\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"), p.StatusCode, p.Latency, p.ClientIP, p.Method, redactQuery(p.Path), p.ErrorMessage)
}

func initDB() *gorm.DB {
	var db *gorm.DB
	var err error

	driver := config.AppConfig.Database.Driver
	dsn := config.AppConfig.Database.DSN

	switch driver {
	case "mysql":
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	default:
		// Default to SQLite (pure Go)
		if dsn == "" {
			dsn = "smsie_v2.db"
		}
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	}

	if err != nil {
		logger.Log.Fatalf("Failed to connect database (%s): %v", driver, err)
	}

	// Auto Migrate
	if err := autoMigrateSchema(db); err != nil {
		logger.Log.Fatalf("Failed to migrate database schema: %v", err)
	}
	// Backfill first_seen_at cho SIM có trước tính năng: lấy SMS đầu tiên, không có thì bây giờ.
	if err := db.Exec("UPDATE modems SET first_seen_at = COALESCE((SELECT MIN(timestamp) FROM sms WHERE sms.iccid = modems.iccid), ?) WHERE first_seen_at IS NULL", time.Now()).Error; err != nil {
		logger.Log.Warnf("Backfill first_seen_at failed: %v", err)
	}
	if err := repository.NewBayRepository(db).MigrateFromModems(); err != nil {
		logger.Log.Fatalf("Failed to migrate slot numbers to modem bays: %v", err)
	}

	// Init Admin
	var count int64
	db.Model(&model.User{}).Count(&count)
	if count == 0 {
		// Generate random password
		const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		ret := make([]byte, 12)
		for i := 0; i < 12; i++ {
			num, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
			if err != nil {
				logger.Log.Fatalf("Failed to generate random password: %v", err)
			}
			ret[i] = chars[num.Int64()]
		}
		randPw := string(ret)

		// Hash it using bcrypt
		bytes, err := bcrypt.GenerateFromPassword([]byte(randPw), 14)
		if err != nil {
			logger.Log.Fatalf("Failed to hash password: %v", err)
		}
		hash := string(bytes)

		admin := model.User{
			Username:     "admin",
			PasswordHash: hash,
			Role:         "admin",
		}
		db.Create(&admin)
		logger.Log.Warnf("INITIAL ADMIN CREATED. Username: admin, Password: %s", randPw)
	}

	return db
}

func autoMigrateSchema(db *gorm.DB) error {
	if err := migrateLegacyModemSIPColumns(db); err != nil {
		return err
	}
	if err := migrateLegacyUserModemPermissionColumns(db); err != nil {
		return err
	}
	return db.AutoMigrate(&model.User{}, &model.Modem{}, &model.SMS{}, &model.CallRecording{}, &model.Webhook{}, &model.UserModemPermission{}, &model.APIKey{}, &model.ModemBay{}, &model.SimSlotEvent{}, &model.BalanceSnapshot{}, &model.BalanceAlert{}, &model.SimAlert{}, &model.KeepaliveRun{}, &model.PhoneNumberHistory{}, &model.AuditLog{}, &model.Setting{})
}

func migrateLegacyModemSIPColumns(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&model.Modem{}) {
		return nil
	}

	legacyColumns := map[string]string{
		"s_ip_enabled":         "sip_enabled",
		"s_ip_username":        "sip_username",
		"s_ip_password":        "sip_password",
		"s_ip_proxy":           "sip_proxy",
		"s_ip_port":            "sip_port",
		"s_ip_domain":          "sip_domain",
		"s_ip_transport":       "sip_transport",
		"s_ip_register":        "sip_register",
		"s_ip_tls_skip_verify": "sip_tls_skip_verify",
		"s_ip_listen_port":     "sip_listen_port",
	}

	for legacy, current := range legacyColumns {
		legacyExists := migrator.HasColumn(&model.Modem{}, legacy)
		currentExists := migrator.HasColumn(&model.Modem{}, current)
		switch {
		case legacyExists && !currentExists:
			if err := migrator.RenameColumn(&model.Modem{}, legacy, current); err != nil {
				return fmt.Errorf("rename modems.%s to %s: %w", legacy, current, err)
			}
		case legacyExists && currentExists:
			logger.Log.Warnf("legacy modem column %s still exists alongside %s; leaving both columns in place", legacy, current)
		}
	}
	return nil
}

func migrateLegacyUserModemPermissionColumns(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&model.UserModemPermission{}) {
		return nil
	}

	legacyExists := migrator.HasColumn(&model.UserModemPermission{}, "icc_id")
	currentExists := migrator.HasColumn(&model.UserModemPermission{}, "iccid")
	switch {
	case legacyExists && !currentExists:
		if err := migrator.RenameColumn(&model.UserModemPermission{}, "icc_id", "iccid"); err != nil {
			return fmt.Errorf("rename user_modem_permissions.icc_id to iccid: %w", err)
		}
	case legacyExists && currentExists:
		logger.Log.Warnf("legacy user_modem_permissions column icc_id still exists alongside iccid; leaving both columns in place")
	}

	return nil
}

func registerSIPModemCallStateListener(wm *worker.Manager, callMgr *calling.Manager, stdLogger *log.Logger) {
	if wm == nil || callMgr == nil || !callMgr.SIPEnabled() {
		return
	}

	wm.AddCallStateListener(func(w *worker.ModemWorker, state worker.CallState) {
		if w == nil {
			return
		}

		rt, ok := w.RuntimeModemState()
		if !ok || strings.TrimSpace(rt.ICCID) == "" {
			return
		}

		target := calling.ModemTarget{}
		if w.IsUACReady() {
			vid, pid := w.UACIdentity()
			target = calling.ModemTarget{PortName: w.PortName, VID: vid, PID: pid}
		}

		modemState := calling.ModemIncomingState{
			State:           state.State,
			Reason:          state.Reason,
			Number:          state.Number,
			Direction:       state.Direction,
			Stat:            state.Stat,
			Mode:            state.Mode,
			Incoming:        state.Incoming,
			Voice:           state.Voice,
			IncomingRinging: state.IncomingRinging,
			UpdatedAt:       state.UpdatedAt,
		}
		if !w.IsUACReady() {
			modemState.State = "idle"
			if strings.TrimSpace(modemState.Reason) == "" {
				modemState.Reason = "modem_unavailable"
			}
		}

		if err := callMgr.SyncModemIncomingSIP(rt.ICCID, target, modemState); err != nil {
			stdLogger.Printf("sip modem state event sync failed [%s]: %v", rt.ICCID, err)
		}
	})
}

func runSIPInboundSyncLoop(db *gorm.DB, wm *worker.Manager, callMgr *calling.Manager, baseCfg calling.SIPConfig, stdLogger *log.Logger, stop <-chan struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	syncOnce := func() {
		if err := syncSIPInboundLines(db, wm, callMgr, baseCfg); err != nil {
			stdLogger.Printf("sip inbound sync failed: %v", err)
		}
	}

	syncOnce()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func syncSIPInboundLines(db *gorm.DB, wm *worker.Manager, callMgr *calling.Manager, baseCfg calling.SIPConfig) error {
	var modems []model.Modem
	if err := db.Find(&modems).Error; err != nil {
		return err
	}

	reservedPorts := map[int]string{}
	activeLines := map[string]struct{}{}

	for _, modem := range modems {
		lineID := sipLineID(modem.ICCID)
		workerForModem := wm.GetWorkerByICCID(modem.ICCID)
		if workerForModem == nil || !workerForModem.IsUACReady() || !modem.SIPEnabled {
			continue
		}

		cfg, ok := buildModemSIPConfig(modem, baseCfg)
		if !ok {
			continue
		}

		listenPort, err := ensureSIPListenerPort(db, modem.ICCID, cfg.LocalPort, cfg.Transport, reservedPorts)
		if err != nil {
			return fmt.Errorf("assign listener port for %s: %w", modem.ICCID, err)
		}
		cfg.LocalPort = listenPort

		iccid := modem.ICCID
		err = callMgr.SyncSIPInbound(lineID, cfg, calling.SIPInboundHooks{
			ICCID: iccid,
			ResolveModem: func() (string, calling.ModemTarget, error) {
				w := wm.GetWorkerByICCID(iccid)
				if w == nil {
					return "", calling.ModemTarget{}, fmt.Errorf("modem %s not active", iccid)
				}
				if !w.IsUACReady() {
					return "", calling.ModemTarget{}, fmt.Errorf("modem %s uac not ready", iccid)
				}
				vid, pid := w.UACIdentity()
				return iccid, calling.ModemTarget{PortName: w.PortName, VID: vid, PID: pid}, nil
			},
			DialModem: func(usedICCID, number string) error {
				w := wm.GetWorkerByICCID(usedICCID)
				if w == nil {
					return fmt.Errorf("modem %s not active", usedICCID)
				}
				return w.Dial(number)
			},
			AnswerModem: func(usedICCID string) error {
				w := wm.GetWorkerByICCID(usedICCID)
				if w == nil {
					return fmt.Errorf("modem %s not active", usedICCID)
				}
				return w.Answer()
			},
			HangupModem: func(usedICCID string) error {
				w := wm.GetWorkerByICCID(usedICCID)
				if w == nil {
					return nil
				}
				return w.Hangup()
			},
			SendDTMF: func(usedICCID, tone string) error {
				w := wm.GetWorkerByICCID(usedICCID)
				if w == nil {
					return fmt.Errorf("modem %s not active", usedICCID)
				}
				_, err := w.ExecuteAT(`AT+VTS="`+tone+`"`, 5*time.Second)
				return err
			},
		})
		if err != nil {
			return fmt.Errorf("sync line %s: %w", lineID, err)
		}

		activeLines[lineID] = struct{}{}
	}

	return callMgr.PruneSIPInboundLines(activeLines)
}

func buildModemSIPConfig(modem model.Modem, base calling.SIPConfig) (calling.SIPConfig, bool) {
	username := strings.TrimSpace(modem.SIPUsername)
	proxy := strings.TrimSpace(modem.SIPProxy)
	if username == "" || proxy == "" {
		return calling.SIPConfig{}, false
	}

	transport := strings.ToLower(strings.TrimSpace(modem.SIPTransport))
	if transport == "" {
		transport = "udp"
	}
	port := modem.SIPPort
	if port <= 0 {
		if transport == "tls" {
			port = 5061
		} else {
			port = 5060
		}
	}

	cfg := base
	cfg.Enabled = true
	cfg.Username = username
	cfg.Password = modem.SIPPassword
	cfg.Proxy = proxy
	cfg.Port = port
	cfg.Domain = strings.TrimSpace(modem.SIPDomain)
	cfg.Transport = transport
	cfg.TLSSkipVerify = modem.SIPTLSSkipVerify
	cfg.Register = modem.SIPRegister
	cfg.AcceptIncoming = modem.SIPAcceptIncoming
	cfg.InviteTarget = strings.TrimSpace(modem.SIPInviteTarget)
	return cfg, true
}

func sipLineID(iccid string) string {
	return fmt.Sprintf("sip-line-%s", strings.TrimSpace(iccid))
}

func ensureSIPListenerPort(db *gorm.DB, iccid string, basePort int, transport string, reserved map[int]string) (int, error) {
	if basePort <= 0 {
		basePort = 5060
	}

	var modem model.Modem
	err := db.First(&modem, "iccid = ?", iccid).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		modem = model.Modem{ICCID: iccid}
		if err := db.Create(&modem).Error; err != nil {
			return 0, err
		}
	}

	if modem.SIPListenPort > 0 {
		if other, ok := reserved[modem.SIPListenPort]; ok && other != iccid {
			return 0, fmt.Errorf("fixed sip listen port %d already reserved by %s", modem.SIPListenPort, other)
		}
		reserved[modem.SIPListenPort] = iccid
		return modem.SIPListenPort, nil
	}

	for port := basePort; port <= 65535; port++ {
		if other, ok := reserved[port]; ok && other != iccid {
			continue
		}
		available, err := isSIPListenPortAvailable(transport, port)
		if err != nil {
			return 0, err
		}
		if !available {
			continue
		}
		if err := db.Model(&model.Modem{}).Where("iccid = ?", iccid).Update("sip_listen_port", port).Error; err != nil {
			return 0, err
		}
		reserved[port] = iccid
		return port, nil
	}

	return 0, fmt.Errorf("no free sip listen port available from %d", basePort)
}

func isSIPListenPortAvailable(transport string, port int) (bool, error) {
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "udp":
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "only one usage") || strings.Contains(strings.ToLower(err.Error()), "address already in use") {
				return false, nil
			}
			return false, err
		}
		_ = conn.Close()
		return true, nil
	default:
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "only one usage") || strings.Contains(strings.ToLower(err.Error()), "address already in use") {
				return false, nil
			}
			return false, err
		}
		_ = ln.Close()
		return true, nil
	}
}
