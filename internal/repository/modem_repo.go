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
