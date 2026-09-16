// Package report: báo cáo tháng theo SIM (spec §3) — một truy vấn gộp mỗi bảng, không N+1.
package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

type Row struct {
	ICCID         string `json:"iccid"`
	PhoneNumber   string `json:"phone_number"`
	SlotNumber    *int   `json:"slot_number"`
	SMSReceived   int64  `json:"sms_received"`
	SMSSent       int64  `json:"sms_sent"`
	SMSFailed     int64  `json:"sms_failed"` // audit_logs sms.send status>=400 (model.SMS không có cột status)
	Calls         int64  `json:"calls"`
	CallSeconds   int64  `json:"call_seconds"`
	BalanceStart  *int64 `json:"balance_start"`
	BalanceEnd    *int64 `json:"balance_end"`
	BalanceDelta  *int64 `json:"balance_delta"`
	SlotEvents    int64  `json:"slot_events"`
	KeepaliveSent int64  `json:"keepalive_sent"`
	Alerts        int64  `json:"alerts"`
}

type Report struct {
	Month  string `json:"month"`
	Rows   []Row  `json:"rows"`
	Totals Row    `json:"totals"`
}

type agg struct {
	ICCID string `gorm:"column:iccid"`
	N     int64
	S     int64
}

// Monthly gộp số liệu tháng chứa `month` (giờ local) cho mọi modem; allowed nil = tất cả,
// allowed rỗng = không SIM nào.
func Monthly(db *gorm.DB, month time.Time, allowed []string) (Report, error) {
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)
	r := Report{Month: start.Format("2006-01"), Rows: []Row{}}

	var modems []model.Modem
	q := db.Order("iccid")
	if allowed != nil {
		q = q.Where("iccid IN ?", allowed)
	}
	if err := q.Find(&modems).Error; err != nil {
		return r, err
	}
	if len(modems) == 0 {
		return r, nil
	}
	var bays []model.ModemBay
	if err := db.Where("current_iccid <> ''").Find(&bays).Error; err != nil {
		return r, err
	}
	slotOf := map[string]*int{}
	for _, b := range bays {
		if b.SlotNumber != nil {
			slotOf[b.CurrentICCID] = b.SlotNumber
		}
	}

	group := func(model interface{}, col string, sel string, where string, args ...interface{}) (map[string]agg, error) {
		var out []agg
		q := db.Model(model).Select("iccid, "+sel).Where(col+" >= ? AND "+col+" < ?", start, end)
		if where != "" {
			q = q.Where(where, args...)
		}
		if err := q.Group("iccid").Scan(&out).Error; err != nil {
			return nil, err
		}
		m := make(map[string]agg, len(out))
		for _, a := range out {
			m[a.ICCID] = a
		}
		return m, nil
	}
	// Snapshot đầu/cuối tháng: MIN/MAX(id) trong khoảng (SQLite trả MAX(datetime) dạng chuỗi).
	edge := func(fn string) (map[string]int64, error) {
		var rows []model.BalanceSnapshot
		sub := db.Model(&model.BalanceSnapshot{}).Select(fn+"(id)").Where("read_at >= ? AND read_at < ?", start, end).Group("iccid")
		if err := db.Where("id IN (?)", sub).Find(&rows).Error; err != nil {
			return nil, err
		}
		m := make(map[string]int64, len(rows))
		for _, s := range rows {
			m[s.ICCID] = s.BalanceVND
		}
		return m, nil
	}

	recv, err := group(&model.SMS{}, "timestamp", "COUNT(*) AS n", "type = ?", "received")
	if err != nil {
		return r, err
	}
	sent, err := group(&model.SMS{}, "timestamp", "COUNT(*) AS n", "type = ?", "sent")
	if err != nil {
		return r, err
	}
	failed, err := group(&model.AuditLog{}, "at", "COUNT(*) AS n", "action = ? AND status >= 400", "sms.send")
	if err != nil {
		return r, err
	}
	calls, err := group(&model.CallRecording{}, "created_at", "COUNT(*) AS n, COALESCE(SUM(duration_seconds),0) AS s", "")
	if err != nil {
		return r, err
	}
	slots, err := group(&model.SimSlotEvent{}, "detected_at", "COUNT(*) AS n", "")
	if err != nil {
		return r, err
	}
	keep, err := group(&model.KeepaliveRun{}, "ran_at", "COUNT(*) AS n", "status = ?", model.KeepaliveSent)
	if err != nil {
		return r, err
	}
	balAlerts, err := group(&model.BalanceAlert{}, "sent_at", "COUNT(*) AS n", "")
	if err != nil {
		return r, err
	}
	simAlerts, err := group(&model.SimAlert{}, "sent_at", "COUNT(*) AS n", "")
	if err != nil {
		return r, err
	}
	first, err := edge("MIN")
	if err != nil {
		return r, err
	}
	last, err := edge("MAX")
	if err != nil {
		return r, err
	}

	for _, m := range modems {
		row := Row{ICCID: m.ICCID, PhoneNumber: m.PhoneNumber, SlotNumber: m.SlotNumber}
		if s, ok := slotOf[m.ICCID]; ok {
			row.SlotNumber = s
		}
		row.SMSReceived = recv[m.ICCID].N
		row.SMSSent = sent[m.ICCID].N
		row.SMSFailed = failed[m.ICCID].N
		row.Calls, row.CallSeconds = calls[m.ICCID].N, calls[m.ICCID].S
		row.SlotEvents = slots[m.ICCID].N
		row.KeepaliveSent = keep[m.ICCID].N
		row.Alerts = balAlerts[m.ICCID].N + simAlerts[m.ICCID].N
		if v, ok := first[m.ICCID]; ok {
			row.BalanceStart = &v
		}
		if v, ok := last[m.ICCID]; ok {
			row.BalanceEnd = &v
		}
		if row.BalanceStart != nil && row.BalanceEnd != nil {
			d := *row.BalanceEnd - *row.BalanceStart
			row.BalanceDelta = &d
		}
		r.Rows = append(r.Rows, row)
	}
	sort.SliceStable(r.Rows, func(i, j int) bool {
		a, b := r.Rows[i].SlotNumber, r.Rows[j].SlotNumber
		switch {
		case a != nil && b != nil && *a != *b:
			return *a < *b
		case (a == nil) != (b == nil):
			return a != nil // có khe lên trước
		}
		return r.Rows[i].ICCID < r.Rows[j].ICCID
	})

	t := &r.Totals
	for _, row := range r.Rows {
		t.SMSReceived += row.SMSReceived
		t.SMSSent += row.SMSSent
		t.SMSFailed += row.SMSFailed
		t.Calls += row.Calls
		t.CallSeconds += row.CallSeconds
		t.SlotEvents += row.SlotEvents
		t.KeepaliveSent += row.KeepaliveSent
		t.Alerts += row.Alerts
		add := func(dst **int64, v *int64) {
			if v == nil {
				return
			}
			if *dst == nil {
				*dst = new(int64)
			}
			**dst += *v
		}
		add(&t.BalanceStart, row.BalanceStart)
		add(&t.BalanceEnd, row.BalanceEnd)
		add(&t.BalanceDelta, row.BalanceDelta)
	}
	return r, nil
}

var csvHeader = []string{"ICCID", "Số thuê bao", "Khe", "SMS nhận", "SMS gửi", "SMS lỗi", "Cuộc gọi", "Giây gọi", "Số dư đầu tháng", "Số dư cuối tháng", "Chênh lệch", "Sự kiện khe", "Nuôi SIM đã gửi", "Cảnh báo"}

// WriteCSV: BOM + tiêu đề Việt, dấu phẩy, dòng cuối là Tổng.
func WriteCSV(w io.Writer, r Report) error {
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	opt := func(v *int64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatInt(*v, 10)
	}
	line := func(name, phone, slot string, x Row) []string {
		return []string{name, phone, slot, fmt.Sprint(x.SMSReceived), fmt.Sprint(x.SMSSent), fmt.Sprint(x.SMSFailed), fmt.Sprint(x.Calls), fmt.Sprint(x.CallSeconds),
			opt(x.BalanceStart), opt(x.BalanceEnd), opt(x.BalanceDelta), fmt.Sprint(x.SlotEvents), fmt.Sprint(x.KeepaliveSent), fmt.Sprint(x.Alerts)}
	}
	for _, x := range r.Rows {
		slot := ""
		if x.SlotNumber != nil {
			slot = strconv.Itoa(*x.SlotNumber)
		}
		if err := cw.Write(line(x.ICCID, x.PhoneNumber, slot, x)); err != nil {
			return err
		}
	}
	if err := cw.Write(line("Tổng", "", "", r.Totals)); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}
