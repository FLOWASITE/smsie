package worker

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/pkg/logger"
)

var (
	ErrBalanceCheckInProgress = errors.New("balance check already requested")
	balancePattern            = regexp.MustCompile(`(?:tk chinh|tai khoan chinh)\s*:\s*([0-9][0-9., ]*)\s*(?:d|vnd)`)
)

func parseBalanceVND(text string) (int64, bool) {
	normalized := strings.ToLower(text)
	normalized = strings.NewReplacer(
		"tài khoản chính", "tai khoan chinh",
		"tk chính", "tk chinh",
		"đ", "d",
	).Replace(normalized)
	match := balancePattern.FindStringSubmatch(normalized)
	if len(match) != 2 {
		return 0, false
	}
	digits := strings.NewReplacer(".", "", ",", "", " ", "").Replace(match[1])
	value, err := strconv.ParseInt(digits, 10, 64)
	return value, err == nil
}

func (w *ModemWorker) captureBalance(text string) bool {
	value, ok := parseBalanceVND(text)
	if !ok {
		return false
	}
	iccid := w.modemICCID()
	if iccid == "" {
		return false
	}
	updatedAt := time.Now()
	if err := w.repo.UpdateBalance(iccid, value, updatedAt); err != nil {
		logger.Log.Warnf("[%s] Failed to persist balance for %s: %v", w.PortName, iccid, err)
		return false
	}
	logger.Log.Infof("[%s] Balance updated for %s: %d VND", w.PortName, iccid, value)
	if err := w.bayRepo.FillBalance(iccid, value, updatedAt); err != nil {
		logger.Log.Warnf("[%s] Failed to attach balance to slot event: %v", w.PortName, err)
	}
	if err := w.balanceRepo.AddSnapshot(iccid, value, updatedAt); err != nil {
		logger.Log.Warnf("[%s] Failed to store balance snapshot: %v", w.PortName, err)
	}
	return true
}

func (w *ModemWorker) RequestBalance() error {
	w.balanceMu.Lock()
	if time.Since(w.balanceRequestedAt) < 30*time.Second {
		w.balanceMu.Unlock()
		return ErrBalanceCheckInProgress
	}
	w.balanceRequestedAt = time.Now()
	w.balanceMu.Unlock()

	code := config.AppConfig.Balance.USSDCode
	if code == "" {
		code = "*101#"
	}
	if _, err := w.ExecuteATSilent(`AT+CUSD=1,"`+code+`",15`, 10*time.Second); err != nil {
		w.balanceMu.Lock()
		w.balanceRequestedAt = time.Time{}
		w.balanceMu.Unlock()
		return err
	}
	return nil
}
