package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestParseBalanceVNDFromVietnamobileResponse(t *testing.T) {
	for _, input := range []string{
		`+CUSD: 0,"TK Chinh: 20.735d, het han 17/10/2026",15`,
		"Tài khoản chính: 20,735 đ",
	} {
		got, ok := parseBalanceVND(input)
		if !ok || got != 20735 {
			t.Fatalf("parseBalanceVND(%q) = %d, %v; want 20735, true", input, got, ok)
		}
	}
}

func TestBalanceResponsePersistsAgainstICCID(t *testing.T) {
	initTestLogger()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}
	const iccid = "89840509241455299254"
	if err := db.Create(&model.Modem{ICCID: iccid}).Error; err != nil {
		t.Fatal(err)
	}

	w := NewModemWorker("COM19", db, nil)
	w.setModem(&model.Modem{ICCID: iccid})
	w.captureBalance("TK Chinh: 20.735d, het han 17/10/2026")

	var modem model.Modem
	if err := db.First(&modem, "iccid = ?", iccid).Error; err != nil {
		t.Fatal(err)
	}
	if modem.BalanceVND != 20735 || modem.BalanceUpdatedAt == nil {
		t.Fatalf("balance = %d at %v; want 20735 with timestamp", modem.BalanceVND, modem.BalanceUpdatedAt)
	}
}

func TestRequestBalanceSendsPrepaidUSSDOnce(t *testing.T) {
	initTestLogger()
	w := NewModemWorker("COM19", nil, nil)
	port := &scriptedPort{worker: w}
	port.script = func(command string) []string { return []string{"OK"} }
	w.port = port
	startTransactionWorker(t, w)

	if err := w.RequestBalance(); err != nil {
		t.Fatal(err)
	}
	writes := port.recordedWrites()
	if len(writes) != 1 || !strings.Contains(writes[0], `AT+CUSD=1,"*101#",15`) {
		t.Fatalf("writes = %q", writes)
	}
	if err := w.RequestBalance(); err != ErrBalanceCheckInProgress {
		t.Fatalf("second request error = %v; want %v", err, ErrBalanceCheckInProgress)
	}

	// Keep the test independent from the production cooldown duration.
	w.balanceMu.Lock()
	w.balanceRequestedAt = time.Time{}
	w.balanceMu.Unlock()
}
