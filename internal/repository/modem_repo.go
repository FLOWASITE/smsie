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

func (r *ModemRepository) TouchRegistered(iccid string, at time.Time) error {
	return r.db.Model(&model.Modem{}).Where("iccid = ?", iccid).Update("last_registered_at", at).Error
}

func (r *ModemRepository) SetFirstSeenIfNull(iccid string, at time.Time) error {
	return r.db.Model(&model.Modem{}).Where("iccid = ? AND first_seen_at IS NULL", iccid).Update("first_seen_at", at).Error
}

// LastReceivedSMSAt trả mốc SMS nhận gần nhất theo ICCID. Dùng MAX(id) thay vì
// MAX(timestamp) vì SQLite trả MAX(datetime) dạng chuỗi không scan được vào time.Time.
func (r *ModemRepository) LastReceivedSMSAt() (map[string]time.Time, error) {
	var rows []model.SMS
	err := r.db.Select("iccid, timestamp").
		Where("id IN (?)", r.db.Model(&model.SMS{}).Select("MAX(id)").Where("type = ?", "received").Group("iccid")).
		Find(&rows).Error
	out := make(map[string]time.Time, len(rows))
	for _, s := range rows {
		out[s.ICCID] = s.Timestamp
	}
	return out, err
}
