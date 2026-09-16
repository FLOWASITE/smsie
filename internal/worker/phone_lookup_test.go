package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestParsePhoneNumber(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`+CUSD: 0,"So thue bao cua quy khach la 84912345678",15`, "0912345678", true},
		{"So dien thoai: 0356789012.", "0356789012", true},
		{"Your number: +84987654321", "0987654321", true},
		{"Ma OTP cua ban la 123456", "", false},
		{"So 0123456789 khong hop le", "", false},
		{"Tong 0912345678901 khong phai so", "", false},
		{"IMEI 352090912345678 ICCID 89840509241455290254", "", false},
		{"LH 0912345678,0987654321", "0912345678", true},
	}
	for _, c := range cases {
		got, ok := parsePhoneNumber(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parsePhoneNumber(%q) = %q,%v; want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLookupCodeFor(t *testing.T) {
	codes := map[string]string{"viettel": "*098#", "Vinaphone": "*110#"}
	if lookupCodeFor("VIETTEL", codes) != "*098#" || lookupCodeFor("VN Vinaphone", codes) != "*110#" || lookupCodeFor("Mobifone", codes) != "" || lookupCodeFor("", codes) != "" {
		t.Fatal("lookupCodeFor mismatch")
	}
}

func newPhoneTestWorker(t *testing.T) (*ModemWorker, *gorm.DB) {
	t.Helper()
	initTestLogger()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.PhoneNumberHistory{}); err != nil {
		t.Fatal(err)
	}
	const iccid = "89840509241455299254"
	if err := db.Create(&model.Modem{ICCID: iccid}).Error; err != nil {
		t.Fatal(err)
	}
	w := NewModemWorker("COM19", db, nil)
	w.setModem(&model.Modem{ICCID: iccid, Operator: "Viettel"})
	return w, db
}

func TestCapturePhoneNumberOnlyInsideWindow(t *testing.T) {
	w, db := newPhoneTestWorker(t)
	if w.capturePhoneNumber("So cua ban: 0912345678") {
		t.Fatal("captured outside window")
	}
	w.phoneLookupUntil = time.Now().Add(time.Minute)
	if w.capturePhoneNumber("Ma OTP 123456") {
		t.Fatal("OTP must not match")
	}
	if !w.capturePhoneNumber(`+CUSD: 0,"So thue bao: 84912345678",15`) {
		t.Fatal("expected capture")
	}
	if !w.phoneLookupUntil.IsZero() {
		t.Fatal("window should close after capture")
	}
	var m model.Modem
	db.First(&m, "iccid = ?", "89840509241455299254")
	var n int64
	db.Model(&model.PhoneNumberHistory{}).Count(&n)
	if m.PhoneNumber != "0912345678" || n != 1 {
		t.Fatalf("phone=%q history=%d", m.PhoneNumber, n)
	}
}

func TestRequestPhoneNumberSendsOperatorUSSD(t *testing.T) {
	w, _ := newPhoneTestWorker(t)
	old := config.AppConfig.PhoneLookup
	config.AppConfig.PhoneLookup = config.PhoneLookupConfig{Enabled: true, Codes: map[string]string{"viettel": "*098#"}}
	t.Cleanup(func() { config.AppConfig.PhoneLookup = old })
	port := &scriptedPort{worker: w}
	port.script = func(command string) []string { return []string{"OK"} }
	w.port = port
	startTransactionWorker(t, w)

	if err := w.RequestPhoneNumber(); err != nil {
		t.Fatal(err)
	}
	writes := port.recordedWrites()
	if len(writes) != 1 || !strings.Contains(writes[0], `AT+CUSD=1,"*098#",15`) {
		t.Fatalf("writes = %q", writes)
	}
	if !time.Now().Before(w.phoneLookupUntil) {
		t.Fatal("window not opened")
	}
	w.setModem(&model.Modem{ICCID: "89840509241455299254", Operator: "Mobifone"})
	if err := w.RequestPhoneNumber(); err == nil || !strings.Contains(err.Error(), "chưa cấu hình mã tra số") {
		t.Fatalf("err = %v", err)
	}
}
