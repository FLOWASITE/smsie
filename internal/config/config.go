package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Serial    SerialConfig    `mapstructure:"serial"`
	Calling   CallingConfig   `mapstructure:"calling"`
	Webhook   WebhookConfig   `mapstructure:"webhook"`
	Users     UsersConfig     `mapstructure:"users"`
	Log       LogConfig       `mapstructure:"log"`
	Balance   BalanceConfig   `mapstructure:"balance"`
	SimHealth SimHealthConfig `mapstructure:"sim_health"`
	Keepalive KeepaliveConfig `mapstructure:"keepalive"`
	// PhoneLookup — tự tra số thuê bao qua USSD theo nhà mạng (Codes: tên nhà mạng → mã).
	PhoneLookup PhoneLookupConfig `mapstructure:"phone_lookup"`
	// Audit — nhật ký hành động; KeepDays: giữ bao nhiêu ngày (dọn lúc khởi động + hằng ngày).
	Audit AuditConfig `mapstructure:"audit"`
	// Backup — sao lưu SQLite hằng ngày lúc Hour:00 vào Dir, giữ Keep bản mới nhất.
	Backup BackupConfig `mapstructure:"backup"`
}

type BackupConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Dir     string `mapstructure:"dir"`
	Hour    int    `mapstructure:"hour"`
	Keep    int    `mapstructure:"keep"`
}

type AuditConfig struct {
	KeepDays int `mapstructure:"keep_days"`
}

type PhoneLookupConfig struct {
	Enabled bool              `mapstructure:"enabled"`
	Codes   map[string]string `mapstructure:"codes"`
}

// KeepaliveConfig — nuôi SIM bằng SMS nội mạng định kỳ. Tốn tiền nên Enabled mặc định false.
type KeepaliveConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	IntervalDays int    `mapstructure:"interval_days"`
	MaxPerMonth  int    `mapstructure:"max_per_month"`
	Message      string `mapstructure:"message"`
	RunHour      int    `mapstructure:"run_hour"`
}

type SimHealthConfig struct {
	Enabled           bool `mapstructure:"enabled"`
	NoSMSDays         int  `mapstructure:"no_sms_days"`
	UnregisteredHours int  `mapstructure:"unregistered_hours"`
	AbsentDays        int  `mapstructure:"absent_days"`
	RemindDays        int  `mapstructure:"remind_days"`
}

type BalanceConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	CheckHour       int    `mapstructure:"check_hour"`
	LowThresholdVND int64  `mapstructure:"low_threshold_vnd"`
	ForecastDays    int    `mapstructure:"forecast_days"`
	USSDCode        string `mapstructure:"ussd_code"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

type DatabaseConfig struct {
	Driver string `mapstructure:"driver"`
	DSN    string `mapstructure:"dsn"`
}

type SerialConfig struct {
	ScanInterval   string   `mapstructure:"scan_interval"`
	ExcludePorts   []string `mapstructure:"exclude_ports"`
	InitATCommands []string `mapstructure:"init_at_commands"`
}

type CallingConfig struct {
	STUNServers []string    `mapstructure:"stun_servers"`
	UDPPortMin  uint16      `mapstructure:"udp_port_min"`
	UDPPortMax  uint16      `mapstructure:"udp_port_max"`
	Audio       AudioConfig `mapstructure:"audio"`
	SIP         SIPConfig   `mapstructure:"sip"`
}

type AudioConfig struct {
	DeviceKeyword    string `mapstructure:"device_keyword"`
	OutputDeviceName string `mapstructure:"output_device_name"`
	SampleRate       int    `mapstructure:"sample_rate"`
	Channels         int    `mapstructure:"channels"`
	BitsPerSample    int    `mapstructure:"bits_per_sample"`
	CaptureChunkMs   int    `mapstructure:"capture_chunk_ms"`
	PlaybackChunkMs  int    `mapstructure:"playback_chunk_ms"`
}

type SIPConfig struct {
	RegisterExpires    int    `mapstructure:"register_expires"`
	LocalHost          string `mapstructure:"local_host"`
	LocalPort          int    `mapstructure:"local_port"`
	RTPBindIP          string `mapstructure:"rtp_bind_ip"`
	RTPPortMin         int    `mapstructure:"rtp_port_min"`
	RTPPortMax         int    `mapstructure:"rtp_port_max"`
	InviteTimeoutSec   int    `mapstructure:"invite_timeout_sec"`
	DTMFMethod         string `mapstructure:"dtmf_method"`
	DTMFDurationMillis int    `mapstructure:"dtmf_duration_ms"`
}

type WebhookConfig struct {
	TelegramToken  string `mapstructure:"telegram_token"`
	TelegramChatID string `mapstructure:"telegram_chat_id"`
	SlackURL       string `mapstructure:"slack_url"`
}

type UsersConfig struct {
	DefaultAdminPassword string `mapstructure:"default_admin_password"`
}

var AppConfig Config

func LoadConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	viper.SetDefault("balance.enabled", true)
	viper.SetDefault("balance.check_hour", 6)
	viper.SetDefault("balance.low_threshold_vnd", 20000)
	viper.SetDefault("balance.forecast_days", 7)
	viper.SetDefault("balance.ussd_code", "*101#")
	viper.SetDefault("sim_health.enabled", true)
	viper.SetDefault("sim_health.no_sms_days", 30)
	viper.SetDefault("sim_health.unregistered_hours", 24)
	viper.SetDefault("sim_health.absent_days", 7)
	viper.SetDefault("sim_health.remind_days", 7)
	viper.SetDefault("keepalive.enabled", false)
	viper.SetDefault("keepalive.interval_days", 25)
	viper.SetDefault("keepalive.max_per_month", 3)
	viper.SetDefault("keepalive.message", "keepalive {{.Date}}")
	viper.SetDefault("keepalive.run_hour", 7)
	viper.SetDefault("phone_lookup.enabled", true)
	viper.SetDefault("phone_lookup.codes", map[string]string{"Viettel": "*098#", "Vinaphone": "*110#", "Mobifone": "*0#", "Vietnamobile": "*102#"})
	viper.SetDefault("audit.keep_days", 365)
	viper.SetDefault("backup.enabled", true)
	viper.SetDefault("backup.dir", "backups")
	viper.SetDefault("backup.hour", 3)
	viper.SetDefault("backup.keep", 14)

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Warning: Config file not found, using defaults. Error: %v", err)
	}

	if err := viper.Unmarshal(&AppConfig); err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
	}

	if len(AppConfig.Calling.STUNServers) == 0 {
		AppConfig.Calling.STUNServers = []string{"stun:stun.l.google.com:19302"}
	}
	if AppConfig.Calling.Audio.DeviceKeyword == "" {
		AppConfig.Calling.Audio.DeviceKeyword = "AC Interface"
	}
	if AppConfig.Calling.Audio.SampleRate <= 0 {
		AppConfig.Calling.Audio.SampleRate = 8000
	}
	if AppConfig.Calling.Audio.Channels <= 0 {
		AppConfig.Calling.Audio.Channels = 1
	}
	if AppConfig.Calling.Audio.BitsPerSample <= 0 {
		AppConfig.Calling.Audio.BitsPerSample = 16
	}
	if AppConfig.Calling.Audio.CaptureChunkMs <= 0 {
		AppConfig.Calling.Audio.CaptureChunkMs = 40
	}
	if AppConfig.Calling.Audio.PlaybackChunkMs <= 0 {
		AppConfig.Calling.Audio.PlaybackChunkMs = 100
	}
	if AppConfig.Calling.SIP.LocalPort <= 0 {
		AppConfig.Calling.SIP.LocalPort = 5060
	}
	if AppConfig.Calling.SIP.RegisterExpires <= 0 {
		AppConfig.Calling.SIP.RegisterExpires = 300
	}
	if AppConfig.Calling.SIP.RTPPortMin <= 0 {
		AppConfig.Calling.SIP.RTPPortMin = 30000
	}
	if AppConfig.Calling.SIP.RTPPortMax < AppConfig.Calling.SIP.RTPPortMin {
		AppConfig.Calling.SIP.RTPPortMax = AppConfig.Calling.SIP.RTPPortMin
	}
	if AppConfig.Calling.SIP.InviteTimeoutSec <= 0 {
		AppConfig.Calling.SIP.InviteTimeoutSec = 30
	}
	if AppConfig.Calling.SIP.DTMFMethod == "" {
		AppConfig.Calling.SIP.DTMFMethod = "info"
	}
	if AppConfig.Calling.SIP.DTMFDurationMillis <= 0 {
		AppConfig.Calling.SIP.DTMFDurationMillis = 160
	}

	log.Println("Configuration loaded successfully")
}
