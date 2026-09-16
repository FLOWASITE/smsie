package worker

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/pkg/logger"
)

type PlanInfo = model.PlanInfo

const planRawMax = 500

var (
	planDate        = `(\d{1,2}/\d{1,2}/\d{4})`
	planMainExpiry  = regexp.MustCompile(`(?:tk chinh|tai khoan chinh|tkc)[^\n]*?het han\s*` + planDate) // [^\n] chứ không [^.]: "15.002d" có dấu chấm
	planFreeMinutes = regexp.MustCompile(`thoai\s*nm[^\d]*(\d+(?:[.,]\d+)?)\s*phut(?:[^.\n]*?het han\s*` + planDate + `)?`)
	planFreeSMS     = regexp.MustCompile(`sms\s*noi\s*mang[^\d]*(\d+)\s*sms(?:[^.\n]*?het han\s*` + planDate + `)?`)
	planData        = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*(mb|gb)\b`)
	planNormalizer  = strings.NewReplacer(
		"tài khoản chính", "tai khoan chinh",
		"tk chính", "tk chinh",
		"hết hạn", "het han",
		"thoại", "thoai",
		"nội mạng", "noi mang",
		"dữ liệu", "du lieu",
		"phút", "phut",
		"đ", "d",
	)
)

// parsePlanInfo thuần: bóc hạn TK chính, phút/SMS nội mạng, data từ text tin nhà mạng.
// ok=false khi không thấy trường nào (OTP, tin quảng cáo...).
func parsePlanInfo(text string) (PlanInfo, bool) {
	n := planNormalizer.Replace(strings.ToLower(text))
	var info PlanInfo
	ok := false
	if m := planMainExpiry.FindStringSubmatch(n); m != nil {
		if t := planParseDate(m[1]); t != nil {
			info.PlanExpiresAt, ok = t, true
		}
	}
	if m := planFreeMinutes.FindStringSubmatch(n); m != nil {
		if v, err := planParseFloat(m[1]); err == nil {
			info.FreeMinutes, info.FreeMinutesExpiresAt, ok = &v, planParseDate(m[2]), true
		}
	}
	if m := planFreeSMS.FindStringSubmatch(n); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			info.FreeSMS, info.FreeSMSExpiresAt, ok = &v, planParseDate(m[2]), true
		}
	}
	if m := planData.FindStringSubmatch(n); m != nil {
		if v, err := planParseFloat(m[1]); err == nil {
			if m[2] == "gb" {
				v *= 1024
			}
			info.DataMB, ok = &v, true
		}
	}
	if !ok {
		return info, false
	}
	raw := strings.TrimSpace(text)
	if r := []rune(raw); len(r) > planRawMax {
		raw = string(r[:planRawMax])
	}
	info.Raw = raw
	return info, true
}

func planParseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
}

func planParseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.ParseInLocation("02/01/2006", s, time.Local)
	if err != nil {
		return nil
	}
	return &t
}

func (w *ModemWorker) capturePlanInfo(text string) bool {
	info, ok := parsePlanInfo(text)
	if !ok {
		return false
	}
	iccid := w.modemICCID()
	if iccid == "" {
		return false
	}
	if err := w.repo.UpdatePlanInfo(iccid, info, time.Now()); err != nil {
		if logger.Log != nil {
			logger.Log.Warnf("[%s] Failed to persist plan info for %s: %v", w.PortName, iccid, err)
		}
		return false
	}
	if logger.Log != nil {
		logger.Log.Infof("[%s] Plan info updated for %s", w.PortName, iccid)
	}
	return true
}
