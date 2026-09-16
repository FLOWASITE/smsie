// Package simhealth phát hiện SIM "chết": im lặng, không đăng ký mạng, vắng khay quá lâu.
package simhealth

import (
	"fmt"
	"time"

	"github.com/pccr10001/smsie/internal/model"
)

type Config struct{ NoSMSDays, UnregisteredHours, AbsentDays, PlanWarnDays int }

type Input struct {
	ICCID                                                   string
	Online                                                  bool
	LastRegisteredAt, FirstSeenAt, LastSMSAt, LastRemovedAt *time.Time
	InBay                                                   bool
	PlanExpiresAt                                           *time.Time // hạn TK chính bóc từ tin nhà mạng
}

type Finding struct {
	ICCID  string `json:"iccid,omitempty"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Evaluate thuần: không DB, không đồng hồ — now do caller đưa vào.
// SIM đã rút khỏi khay chỉ có thể là absent — no_sms/unregistered cần SIM đang cắm.
func Evaluate(now time.Time, cfg Config, inputs []Input) []Finding {
	var out []Finding
	for _, in := range inputs {
		ref := in.LastSMSAt
		if ref == nil {
			ref = in.FirstSeenAt
		}
		if in.InBay && ref != nil && cfg.NoSMSDays > 0 && now.Sub(*ref) >= days(cfg.NoSMSDays) {
			out = append(out, Finding{in.ICCID, model.SimAlertNoSMS, fmt.Sprintf("không nhận SMS nào %d ngày — có thể bị thu hồi", int(now.Sub(*ref).Hours()/24))})
		}
		if in.Online && cfg.UnregisteredHours > 0 {
			var since time.Duration
			if in.LastRegisteredAt != nil {
				since = now.Sub(*in.LastRegisteredAt)
			} else if in.FirstSeenAt != nil {
				since = now.Sub(*in.FirstSeenAt)
			}
			if since >= time.Duration(cfg.UnregisteredHours)*time.Hour {
				out = append(out, Finding{in.ICCID, model.SimAlertUnregistered, fmt.Sprintf("không đăng ký mạng %d giờ dù modem online", int(since.Hours()))})
			}
		}
		if !in.InBay && in.LastRemovedAt != nil && cfg.AbsentDays > 0 && now.Sub(*in.LastRemovedAt) >= days(cfg.AbsentDays) {
			out = append(out, Finding{in.ICCID, model.SimAlertAbsent, fmt.Sprintf("đã rút khỏi khay %d ngày", int(now.Sub(*in.LastRemovedAt).Hours()/24))})
		}
		if in.PlanExpiresAt != nil && cfg.PlanWarnDays > 0 && in.PlanExpiresAt.Sub(now) <= days(cfg.PlanWarnDays) {
			left := int(in.PlanExpiresAt.Sub(now).Hours() / 24)
			msg := fmt.Sprintf("còn %d ngày", left)
			if left < 0 {
				msg = fmt.Sprintf("đã quá hạn %d ngày", -left)
			}
			out = append(out, Finding{in.ICCID, model.SimAlertPlanExpiring, fmt.Sprintf("TK chính hết hạn %s (%s)", in.PlanExpiresAt.Format("02/01/2006"), msg)})
		}
	}
	return out
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
