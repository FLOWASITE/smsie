package worker

import (
	"encoding/hex"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/warthog618/sms"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestReceiveDeletesOnlySavedIndexAndDeduplicatesRetry(t *testing.T) {
	initTestLogger()
	for _, failDB := range []bool{false, true} {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		defer sqlDB.Close()
		if !failDB {
			if err := db.AutoMigrate(&model.SMS{}, &model.Webhook{}); err != nil {
				t.Fatal(err)
			}
		}
		packets, err := sms.Encode([]byte("OTP 123456"), sms.AsDeliver, sms.From("+84123456789"))
		if err != nil {
			t.Fatal(err)
		}
		data, err := packets[0].MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		pdu := strings.ToUpper(hex.EncodeToString(append([]byte{0}, data...)))
		w := NewModemWorker("test", db, nil)
		w.setModem(&model.Modem{ICCID: "test"})
		port := &scriptedPort{worker: w, script: func(command string) []string {
			switch command {
			case "AT+CMGL=4\r":
				return []string{"+CMGL: 7,0,,20", pdu, "+CMTI: \"MT\",8", "+CMGL: 9,0,,1", "FF", "OK"}
			case "AT+CMGD=7,0\r":
				return []string{"ERROR"} // retained on modem, next poll sees same PDU
			case "AT+CMGD=9,0\r":
				// PDU "FF" không giải mã được: vẫn được lưu dạng thô nên được phép xoá khỏi SIM.
				if failDB {
					t.Error("deleted undecodable SMS that was never saved")
				}
				return []string{"ERROR"}
			default:
				t.Errorf("unexpected destructive command: %q", command)
				return []string{"ERROR"}
			}
		}}
		w.port = port
		for i := 0; i < 2; i++ {
			w.checkSMS(&atSession{worker: w})
		}
		deletes := 0
		for _, cmd := range port.recordedWrites() {
			if strings.HasPrefix(cmd, "AT+CMGD") {
				deletes++
				if failDB {
					t.Error("deleted SMS after DB failure")
				}
			}
		}
		if !failDB {
			var count int64
			db.Model(&model.SMS{}).Count(&count)
			// 2 tin (1 đọc được + 1 thô), mỗi lượt quét thử xoá cả hai; lượt hai không lưu trùng.
			if count != 2 || deletes != 4 {
				t.Fatalf("saved=%d deletion attempts=%d", count, deletes)
			}
			var rawOnly model.SMS
			if err := db.Where("raw_pdu = ?", "FF").First(&rawOnly).Error; err != nil ||
				!strings.HasPrefix(rawOnly.Content, undecodedPrefix) || rawOnly.Phone != "unknown" || rawOnly.Type != "received" {
				t.Fatalf("tin thô = %+v, err = %v", rawOnly, err)
			}
			var ok model.SMS
			if err := db.Where("raw_pdu = ?", pdu).First(&ok).Error; err != nil || ok.Content != "OTP 123456" {
				t.Fatalf("tin đọc được = %+v, err = %v", ok, err)
			}
		}
	}
}

func TestStoredSMSKeepsIndexesAcrossURCs(t *testing.T) {
	entries := parseStoredSMS("+CMGL: 7,0,,3\n+CMTI: MT,8\n001122\n+CMGL: invalid\n001122\nOK", func(s string) bool { return strings.HasPrefix(s, "+CMTI:") })
	if len(entries) != 1 || entries[0].index != 7 || entries[0].pdu != "001122" {
		t.Fatalf("entries=%v", entries)
	}
}
