package worker

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestSendSMSDestinationTypeOnSerialWire(t *testing.T) {
	initTestLogger()
	for _, tc := range []struct {
		number string
		toa    byte
	}{
		{"6020", 0x81}, {"8066", 0x81}, {"123", 0x81},
		{"0944134544", 0x81}, {"+84944134544", 0x91},
	} {
		t.Run(tc.number, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, _ := db.DB()
			defer sqlDB.Close()
			if err := db.AutoMigrate(&model.SMS{}); err != nil {
				t.Fatal(err)
			}
			w := NewModemWorker("test", db, nil)
			w.setModem(&model.Modem{ICCID: "test-sim"})
			port := &scriptedPort{worker: w, script: func(command string) []string {
				if strings.HasPrefix(command, "AT+CMGS=") {
					return []string{">"}
				}
				if strings.HasSuffix(command, "\x1A") {
					return []string{"+CMGS: 1", "OK"}
				}
				return []string{"OK"}
			}}
			w.port = port
			startTransactionWorker(t, w)
			// Exercise every segment, not just the encoder's first packet.
			if err := w.SendSMS(tc.number, strings.Repeat("ZALO ", 40)); err != nil {
				t.Fatal(err)
			}
			segments := 0
			for _, write := range port.recordedWrites() {
				if !strings.HasSuffix(write, "\x1A") {
					continue
				}
				raw, err := hex.DecodeString(strings.TrimSuffix(write, "\x1A"))
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) < 5 || raw[0] != 0 {
					t.Fatalf("unexpected SMSC/TPDU: %X", raw)
				}
				if raw[4] != tc.toa {
					t.Errorf("destination %s: wire TOA=0x%02X, want 0x%02X", tc.number, raw[4], tc.toa)
				}
				segments++
			}
			if segments < 2 {
				t.Fatalf("expected multipart SMS, got %d segments", segments)
			}
		})
	}
}
