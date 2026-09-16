package simhealth

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	cfg := Config{NoSMSDays: 30, UnregisteredHours: 24, AbsentDays: 7, PlanWarnDays: 7}
	cases := []struct {
		name   string
		in     Input
		kind   string // "" = không báo
		detail string
	}{
		{"mới cắm 3 ngày chưa SMS → im", Input{FirstSeenAt: ago(72 * time.Hour), InBay: true}, "", ""},
		{"im 31 ngày → no_sms", Input{LastSMSAt: ago(31 * 24 * time.Hour), InBay: true}, "no_sms", "không nhận SMS nào 31 ngày — có thể bị thu hồi"},
		{"chưa SMS, thấy 30 ngày → no_sms", Input{FirstSeenAt: ago(30 * 24 * time.Hour), InBay: true}, "no_sms", "không nhận SMS nào 30 ngày — có thể bị thu hồi"},
		{"online, không đăng ký 25h → unregistered", Input{Online: true, LastRegisteredAt: ago(25 * time.Hour), LastSMSAt: ago(time.Hour), InBay: true}, "unregistered", "không đăng ký mạng 25 giờ dù modem online"},
		{"online, chưa từng đăng ký, thấy 26h → unregistered", Input{Online: true, FirstSeenAt: ago(26 * time.Hour), InBay: true}, "unregistered", "không đăng ký mạng 26 giờ dù modem online"},
		{"offline không báo unregistered", Input{Online: false, LastRegisteredAt: ago(48 * time.Hour), LastSMSAt: ago(time.Hour), InBay: true}, "", ""},
		{"vắng khay 8 ngày → absent", Input{LastRemovedAt: ago(8 * 24 * time.Hour), LastSMSAt: ago(time.Hour)}, "absent", "đã rút khỏi khay 8 ngày"},
		{"đã rút 8 ngày, im 60 ngày → chỉ absent", Input{LastRemovedAt: ago(8 * 24 * time.Hour), LastSMSAt: ago(60 * 24 * time.Hour)}, "absent", "đã rút khỏi khay 8 ngày"},
		{"vắng 3 ngày → im", Input{LastRemovedAt: ago(3 * 24 * time.Hour), LastSMSAt: ago(time.Hour)}, "", ""},
		{"TK chính còn 30 ngày → im", Input{LastSMSAt: ago(time.Hour), InBay: true, PlanExpiresAt: ago(-30 * 24 * time.Hour)}, "", ""},
		{"TK chính còn 5 ngày → plan_expiring", Input{LastSMSAt: ago(time.Hour), InBay: true, PlanExpiresAt: ago(-5 * 24 * time.Hour)}, "plan_expiring", "TK chính hết hạn 21/09/2026 (còn 5 ngày)"},
		{"TK chính quá hạn 3 ngày → plan_expiring", Input{LastSMSAt: ago(time.Hour), InBay: true, PlanExpiresAt: ago(3 * 24 * time.Hour)}, "plan_expiring", "TK chính hết hạn 13/09/2026 (đã quá hạn 3 ngày)"},
		{"removed cũ nhưng đang trong khay → im", Input{LastRemovedAt: ago(9 * 24 * time.Hour), LastSMSAt: ago(time.Hour), InBay: true}, "", ""},
	}
	for _, c := range cases {
		c.in.ICCID = "X"
		got := Evaluate(now, cfg, []Input{c.in})
		if c.kind == "" {
			if len(got) != 0 {
				t.Errorf("%s: got %+v", c.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Kind != c.kind || got[0].Detail != c.detail || got[0].ICCID != "X" {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
	// Một SIM có thể mang nhiều finding cùng lúc.
	multi := Evaluate(now, cfg, []Input{{ICCID: "M", Online: true, LastSMSAt: ago(40 * 24 * time.Hour), LastRegisteredAt: ago(30 * time.Hour), InBay: true}})
	if len(multi) != 2 {
		t.Errorf("multi = %+v", multi)
	}
}
