package worker

import (
	"testing"
	"time"
)

func TestParsePlanInfo(t *testing.T) {
	d := func(s string) time.Time { t, _ := time.ParseInLocation("02/01/2006", s, time.Local); return t }
	sms := "TK Chinh: 15.002d, het han 13/10/2026.\nThoai NM: 10,0 phut, het han 14/10/2026.\nSMS noi mang: 9 SMS, het han 14/10/2026.\nTk du lieu:\n- NPS100: 100 MB, h"
	info, ok := parsePlanInfo(sms)
	if !ok || info.PlanExpiresAt == nil || !info.PlanExpiresAt.Equal(d("13/10/2026")) {
		t.Fatalf("plan expiry: %+v ok=%v", info, ok)
	}
	if info.FreeMinutes == nil || *info.FreeMinutes != 10 || info.FreeMinutesExpiresAt == nil || !info.FreeMinutesExpiresAt.Equal(d("14/10/2026")) {
		t.Errorf("minutes: %+v", info)
	}
	if info.FreeSMS == nil || *info.FreeSMS != 9 || info.FreeSMSExpiresAt == nil || !info.FreeSMSExpiresAt.Equal(d("14/10/2026")) {
		t.Errorf("sms: %+v", info)
	}
	if info.DataMB == nil || *info.DataMB != 100 || info.Raw != sms {
		t.Errorf("data/raw: %+v", info)
	}

	ussd := "Xin chao 0924914356 TKC: 15.002d Du Lieu: 100,0MB 1. Goi cuoc Thoai & SMS 2. Nap tien"
	info, ok = parsePlanInfo(ussd)
	if !ok || info.DataMB == nil || *info.DataMB != 100 || info.PlanExpiresAt != nil || info.FreeMinutes != nil || info.FreeSMS != nil {
		t.Errorf("ussd: %+v ok=%v", info, ok)
	}

	if _, ok := parsePlanInfo("Ma xac thuc OTP cua ban la 483920. Khong chia se cho bat ky ai."); ok {
		t.Error("OTP must not parse")
	}

	info, ok = parsePlanInfo("Tài khoản chính hết hạn 01/01/2027. Dữ liệu: 1,5 GB")
	if !ok || info.DataMB == nil || *info.DataMB != 1536 || info.PlanExpiresAt == nil || !info.PlanExpiresAt.Equal(d("01/01/2027")) {
		t.Errorf("gb/accents: %+v", info)
	}
}
