package repository

import (
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/slotlog"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrSlotTaken = errors.New("slot already assigned to another modem")

// imeiPattern lọc IMEI rác parser cũ có thể đã lưu (vd "+CPIN: READY").
var imeiPattern = regexp.MustCompile(`^\d{14,16}$`)

// ponytail: khoá toàn cục vì SQLite không có busy_timeout; đủ cho 32 worker, nâng lên busy_timeout DSN nếu đổi driver
var bayWriteMu sync.Mutex

type BayRepository struct {
	db *gorm.DB
}

func NewBayRepository(db *gorm.DB) *BayRepository { return &BayRepository{db: db} }

// syncSlotCache ghi modems.slot_number (cache của modem_bays): bỏ cache cũ của SIM khác đang giữ slot đó trước, vì cột vẫn UNIQUE.
func syncSlotCache(tx *gorm.DB, iccid string, slot *int) error {
	if slot != nil {
		if err := tx.Model(&model.Modem{}).Where("slot_number = ? AND iccid <> ?", *slot, iccid).Update("slot_number", nil).Error; err != nil {
			return err
		}
	}
	return tx.Model(&model.Modem{}).Where("iccid = ?", iccid).Update("slot_number", slot).Error
}

// Observation là kết quả probe một modem: worker gọi Observe sau khi đã lưu modems.
type Observation struct {
	IMEI     string
	ICCID    string
	Operator string
	PortName string
	At       time.Time
}

// Observe so sánh (IMEI, ICCID) với bays, ghi event nếu đổi, đồng bộ modems.slot_number. Trả events đã ghi.
func (r *BayRepository) Observe(o Observation) ([]model.SimSlotEvent, error) {
	if o.IMEI == "" || o.ICCID == "" {
		return nil, nil
	}
	bayWriteMu.Lock()
	defer bayWriteMu.Unlock()
	var written []model.SimSlotEvent
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var bays []model.ModemBay
		if err := tx.Find(&bays).Error; err != nil {
			return err
		}
		d := slotlog.DecideSlotEvents(bays, o.IMEI, o.ICCID)
		d.Bay.LastSeenAt = &o.At
		if d.Bay.SlotNumber == nil && len(d.Events) > 0 && d.Events[0].Event == model.SlotEventInserted {
			d.Bay.SlotNumber = slotlog.InferSlot(knownSlotPorts(tx, bays), o.PortName)
			d.Events[0].ToSlot = d.Bay.SlotNumber
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "imei"}},
			DoUpdates: clause.AssignmentColumns([]string{"current_iccid", "last_seen_at"}),
		}).Create(&d.Bay).Error; err != nil {
			return err
		}
		if d.VacatedBay != nil {
			if err := tx.Model(&model.ModemBay{}).Where("imei = ?", d.VacatedBay.IMEI).Update("current_iccid", "").Error; err != nil {
				return err
			}
		}
		if len(d.Events) == 0 {
			return nil
		}
		ids := make([]string, 0, len(d.Events))
		for i := range d.Events {
			ids = append(ids, d.Events[i].ICCID)
		}
		var modems []model.Modem
		tx.Where("iccid IN ?", ids).Find(&modems)
		phoneByICCID := make(map[string]string, len(modems))
		for _, m := range modems {
			phoneByICCID[m.ICCID] = m.PhoneNumber
		}
		for i := range d.Events {
			e := &d.Events[i]
			e.DetectedAt = o.At
			e.PortName = o.PortName
			e.PhoneNumber = phoneByICCID[e.ICCID]
			if e.ICCID == o.ICCID {
				e.Operator = o.Operator
			}
		}
		if err := tx.Create(&d.Events).Error; err != nil {
			return err
		}
		if err := syncSlotCache(tx, o.ICCID, d.Bay.SlotNumber); err != nil {
			return err
		}
		for i := range d.Events {
			if d.Events[i].Event == model.SlotEventRemoved {
				if err := syncSlotCache(tx, d.Events[i].ICCID, nil); err != nil {
					return err
				}
			}
		}
		written = d.Events
		return nil
	})
	return written, err
}

// MarkEmpty ghi removed khi modem báo không có SIM; lần gọi lặp không ghi thêm.
func (r *BayRepository) MarkEmpty(imei, portName string, at time.Time) error {
	bayWriteMu.Lock()
	defer bayWriteMu.Unlock()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var bay model.ModemBay
		if err := tx.First(&bay, "imei = ?", imei).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if bay.CurrentICCID == "" {
			return nil
		}
		ev := model.SimSlotEvent{DetectedAt: at, ICCID: bay.CurrentICCID, IMEI: imei, Event: model.SlotEventRemoved, FromSlot: bay.SlotNumber, PortName: portName}
		if err := tx.Create(&ev).Error; err != nil {
			return err
		}
		if err := syncSlotCache(tx, bay.CurrentICCID, nil); err != nil {
			return err
		}
		return tx.Model(&bay).Updates(map[string]interface{}{"current_iccid": "", "last_seen_at": at}).Error
	})
}

// FillBalance điền số dư vào event mới nhất của ICCID trong 5 phút gần nhất còn thiếu số dư.
func (r *BayRepository) FillBalance(iccid string, balance int64, at time.Time) error {
	var ev model.SimSlotEvent
	err := r.db.Where("iccid = ? AND balance_vnd IS NULL AND detected_at >= ?", iccid, at.Add(-5*time.Minute)).
		Order("detected_at DESC, id DESC").First(&ev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.db.Model(&ev).Update("balance_vnd", balance).Error
}

// AssignSlot gán/bỏ gán số khe cho IMEI (hiệu chuẩn).
func (r *BayRepository) AssignSlot(imei string, slot *int) error {
	bayWriteMu.Lock()
	defer bayWriteMu.Unlock()
	return r.db.Transaction(func(tx *gorm.DB) error {
		if slot != nil {
			var n int64
			tx.Model(&model.ModemBay{}).Where("slot_number = ? AND imei <> ?", *slot, imei).Count(&n)
			if n > 0 {
				return ErrSlotTaken
			}
		}
		var bay model.ModemBay
		if err := tx.First(&bay, "imei = ?", imei).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ModemBay{}).Where("imei = ?", imei).Update("slot_number", slot).Error; err != nil {
			return err
		}
		if bay.CurrentICCID != "" {
			return syncSlotCache(tx, bay.CurrentICCID, slot)
		}
		return nil
	})
}

func (r *BayRepository) List() ([]model.ModemBay, error) {
	var bays []model.ModemBay
	err := r.db.Order("slot_number IS NULL, slot_number").Find(&bays).Error
	return bays, err
}

type EventFilter struct {
	ICCID    string
	Slot     *int
	From, To *time.Time
	Page     int
	PageSize int
}

func (r *BayRepository) ListEvents(f EventFilter) ([]model.SimSlotEvent, int64, error) {
	q := r.db.Model(&model.SimSlotEvent{})
	if f.ICCID != "" {
		q = q.Where("iccid = ?", f.ICCID)
	}
	if f.Slot != nil {
		q = q.Where("from_slot = ? OR to_slot = ?", *f.Slot, *f.Slot)
	}
	if f.From != nil {
		q = q.Where("detected_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("detected_at < ?", *f.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if f.PageSize <= 0 {
		f.PageSize = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	var evs []model.SimSlotEvent
	err := q.Order("detected_at DESC, id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&evs).Error
	return evs, total, err
}

// MigrateFromModems chạy một lần lúc khởi động: copy slot_number gán tay theo ICCID sang bay theo IMEI.
// Bỏ qua dòng không IMEI/IMEI rác, trùng IMEI hoặc trùng slot; idempotent (bay đã có thì không đụng).
func (r *BayRepository) MigrateFromModems() error {
	var modems []model.Modem
	if err := r.db.Where("slot_number IS NOT NULL AND imei <> ''").Order("slot_number").Find(&modems).Error; err != nil {
		return err
	}
	seenIMEI := map[string]bool{}
	for _, m := range modems {
		if !imeiPattern.MatchString(m.IMEI) {
			if logger.Log != nil {
				logger.Log.Warnf("MigrateFromModems: bỏ qua ICCID=%s vì IMEI=%q không hợp lệ", m.ICCID, m.IMEI)
			}
			continue
		}
		if seenIMEI[m.IMEI] {
			if logger.Log != nil {
				logger.Log.Warnf("MigrateFromModems: bỏ qua ICCID=%s vì IMEI=%s trùng", m.ICCID, m.IMEI)
			}
			continue
		}
		seenIMEI[m.IMEI] = true
		var n int64
		r.db.Model(&model.ModemBay{}).Where("imei = ? OR slot_number = ?", m.IMEI, *m.SlotNumber).Count(&n)
		if n > 0 {
			if logger.Log != nil {
				logger.Log.Warnf("MigrateFromModems: bỏ qua IMEI=%s slot=%d vì bay/slot đã tồn tại", m.IMEI, *m.SlotNumber)
			}
			continue
		}
		if err := r.db.Create(&model.ModemBay{IMEI: m.IMEI, SlotNumber: m.SlotNumber, CurrentICCID: m.ICCID}).Error; err != nil {
			return err
		}
	}
	return nil
}

// knownSlotPorts trả khe đã hiệu chuẩn → cổng COM lần cuối thấy modem đó (từ sim_slot_events,
// vì modems.port_name không lưu DB). Dùng cho slotlog.InferSlot.
func knownSlotPorts(tx *gorm.DB, bays []model.ModemBay) map[int]string {
	var rows []model.SimSlotEvent
	tx.Where("id IN (SELECT MAX(id) FROM sim_slot_events GROUP BY imei)").Find(&rows)
	portByIMEI := make(map[string]string, len(rows))
	for _, r := range rows {
		portByIMEI[r.IMEI] = r.PortName
	}
	known := map[int]string{}
	for _, b := range bays {
		if b.SlotNumber != nil {
			if p := portByIMEI[b.IMEI]; p != "" {
				known[*b.SlotNumber] = p
			}
		}
	}
	return known
}
