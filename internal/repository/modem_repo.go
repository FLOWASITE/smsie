package repository

import (
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ModemRepository struct {
	db *gorm.DB
}

func NewModemRepository(db *gorm.DB) *ModemRepository {
	return &ModemRepository{db: db}
}

func (r *ModemRepository) Upsert(modem *model.Modem) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "iccid"}},
		DoUpdates: clause.AssignmentColumns([]string{"imei"}),
	}).Create(modem).Error
}

func (r *ModemRepository) FindByICCID(iccid string) (*model.Modem, error) {
	var modem model.Modem
	err := r.db.First(&modem, "iccid = ?", iccid).Error
	return &modem, err
}

func (r *ModemRepository) MarkAllOffline() {
	// Runtime status is in-memory and should not be persisted.
}

func (r *ModemRepository) UpdateBalance(iccid string, balance int64, updatedAt time.Time) error {
	return r.db.Model(&model.Modem{}).Where("iccid = ?", iccid).Updates(map[string]interface{}{
		"balance_vnd":        balance,
		"balance_updated_at": updatedAt,
	}).Error
}

// UpdatePlanInfo chỉ ghi đè trường nào tin nhà mạng có; plan_updated_at/plan_raw luôn cập nhật.
func (r *ModemRepository) UpdatePlanInfo(iccid string, info model.PlanInfo, updatedAt time.Time) error {
	fields := map[string]interface{}{"plan_updated_at": updatedAt, "plan_raw": info.Raw}
	if info.PlanExpiresAt != nil {
		fields["plan_expires_at"] = info.PlanExpiresAt
	}
	if info.FreeMinutes != nil {
		fields["free_minutes"] = info.FreeMinutes
		fields["free_minutes_expires_at"] = info.FreeMinutesExpiresAt
	}
	if info.FreeSMS != nil {
		fields["free_sms"] = info.FreeSMS
		fields["free_sms_expires_at"] = info.FreeSMSExpiresAt
	}
	if info.DataMB != nil {
		fields["data_mb"] = info.DataMB
	}
	return r.db.Model(&model.Modem{}).Where("iccid = ?", iccid).Updates(fields).Error
}

func (r *ModemRepository) TouchRegistered(iccid string, at time.Time) error {
	return r.db.Model(&model.Modem{}).Where("iccid = ?", iccid).Update("last_registered_at", at).Error
}

func (r *ModemRepository) SetFirstSeenIfNull(iccid string, at time.Time) error {
	return r.db.Model(&model.Modem{}).Where("iccid = ? AND first_seen_at IS NULL", iccid).Update("first_seen_at", at).Error
}

// LastReceivedSMSAt trả mốc hệ thống nhận SMS gần nhất theo ICCID (created_at, không phải
// timestamp SCTS của mạng — có thể lệch thứ tự). Dùng MAX(id) thay vì MAX(created_at) vì
// SQLite trả MAX(datetime) dạng chuỗi không scan được vào time.Time.
func (r *ModemRepository) LastReceivedSMSAt() (map[string]time.Time, error) {
	var rows []model.SMS
	err := r.db.Select("iccid, created_at").
		Where("id IN (?)", r.db.Model(&model.SMS{}).Select("MAX(id)").Where("type = ?", "received").Group("iccid")).
		Find(&rows).Error
	out := make(map[string]time.Time, len(rows))
	for _, s := range rows {
		out[s.ICCID] = s.CreatedAt
	}
	return out, err
}

// SetPhoneNumber ghi số mới nếu khác số hiện tại: UPDATE modems + INSERT phone_number_history
// trong một transaction. Trả changed=false (không ghi gì) khi số không đổi.
func (r *ModemRepository) SetPhoneNumber(iccid, phone, source string) (bool, error) {
	changed := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var m model.Modem
		if err := tx.First(&m, "iccid = ?", iccid).Error; err != nil {
			return err
		}
		if m.PhoneNumber == phone {
			return nil
		}
		if err := tx.Model(&model.Modem{}).Where("iccid = ?", iccid).Update("phone_number", phone).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.PhoneNumberHistory{ICCID: iccid, OldPhone: m.PhoneNumber, NewPhone: phone, Source: source, At: time.Now()}).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

// PhoneHistory trả lịch sử đổi số, mới nhất trước.
func (r *ModemRepository) PhoneHistory(iccid string) ([]model.PhoneNumberHistory, error) {
	var rows []model.PhoneNumberHistory
	err := r.db.Where("iccid = ?", iccid).Order("id DESC").Find(&rows).Error
	return rows, err
}
