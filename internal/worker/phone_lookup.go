package worker

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/pkg/logger"
)

const phoneLookupWindow = 90 * time.Second

var ErrNoPhoneLookupCode = errors.New("chưa cấu hình mã tra số")

// phonePattern: 84xxxxxxxxx / +84xxxxxxxxx / 0xxxxxxxxx (9 số sau đầu số); chặn đầu bằng ký tự
// không phải chữ số để không nhặt đoạn giữa ICCID/IMEI.
var phonePattern = regexp.MustCompile(`(?:^|[^\d])(?:\+?84|0)(\d{9})\b`)

// parsePhoneNumber nhặt số thuê bao VN đầu tiên trong text, chuẩn hoá 0XXXXXXXXX.
// Chỉ nhận đầu số di động 03/05/07/08/09 để không nhặt nhầm mã OTP hay số tiền.
func parsePhoneNumber(text string) (string, bool) {
	for _, m := range phonePattern.FindAllStringSubmatch(text, -1) {
		if strings.ContainsRune("35789", rune(m[1][0])) {
			return "0" + m[1], true
		}
	}
	return "", false
}

// lookupCodeFor tra mã USSD theo tên nhà mạng: không phân biệt hoa thường, khớp chứa chuỗi
// (viper hạ chữ thường key trong map đọc từ YAML).
func lookupCodeFor(operator string, codes map[string]string) string {
	op := strings.ToLower(strings.TrimSpace(operator))
	if op == "" {
		return ""
	}
	for name, code := range codes {
		n := strings.ToLower(name)
		if strings.Contains(op, n) || strings.Contains(n, op) {
			return code
		}
	}
	return ""
}

// RequestPhoneNumber gửi USSD tra số theo nhà mạng và mở cửa sổ bắt trả lời 90 s.
func (w *ModemWorker) RequestPhoneNumber() error {
	modem, _ := w.modemSnapshot()
	code := lookupCodeFor(modem.Operator, config.AppConfig.PhoneLookup.Codes)
	if code == "" {
		return fmt.Errorf("%w cho %q", ErrNoPhoneLookupCode, modem.Operator)
	}
	w.phoneLookupMu.Lock()
	w.phoneLookupUntil = time.Now().Add(phoneLookupWindow)
	w.phoneLookupMu.Unlock()
	if _, err := w.ExecuteATSilent(`AT+CUSD=1,"`+code+`",15`, 10*time.Second); err != nil {
		w.phoneLookupMu.Lock()
		w.phoneLookupUntil = time.Time{}
		w.phoneLookupMu.Unlock()
		return err
	}
	return nil
}

// capturePhoneNumber chỉ parse khi cửa sổ tra số đang mở (tránh nhặt số trong tin OTP).
// Thành công → đóng cửa sổ, ghi DB + history.
func (w *ModemWorker) capturePhoneNumber(text string) bool {
	w.phoneLookupMu.Lock()
	open := time.Now().Before(w.phoneLookupUntil)
	w.phoneLookupMu.Unlock()
	if !open {
		return false
	}
	phone, ok := parsePhoneNumber(text)
	if !ok {
		return false
	}
	iccid := w.modemICCID()
	if iccid == "" {
		return false
	}
	w.phoneLookupMu.Lock()
	w.phoneLookupUntil = time.Time{}
	w.phoneLookupMu.Unlock()
	changed, err := w.repo.SetPhoneNumber(iccid, phone, "ussd")
	if err != nil {
		logger.Log.Warnf("[%s] Failed to persist phone number for %s: %v", w.PortName, iccid, err)
		return false
	}
	if changed {
		logger.Log.Infof("[%s] Phone number for %s: %s", w.PortName, iccid, phone)
	}
	return true
}

// lookupPhoneIfMissing kích hoạt tra số khi SIM chưa có số (sau probe / đảo SIM).
func (w *ModemWorker) lookupPhoneIfMissing(iccid string) {
	if !config.AppConfig.PhoneLookup.Enabled {
		return
	}
	m, err := w.repo.FindByICCID(iccid)
	if err != nil || m.PhoneNumber != "" {
		return
	}
	if err := w.RequestPhoneNumber(); err != nil {
		logger.Log.Debugf("[%s] Phone lookup skipped: %v", w.PortName, err)
	}
}
