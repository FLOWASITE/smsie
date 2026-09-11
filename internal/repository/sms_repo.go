package repository

import (
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

type SMSRepository struct {
	db *gorm.DB
}

func NewSMSRepository(db *gorm.DB) *SMSRepository {
	return &SMSRepository{db: db}
}

func (r *SMSRepository) Create(sms *model.SMS) error {
	return r.db.Create(sms).Error
}

// The per-modem worker serializes receive processing. A delete retry must not
// create another inbox entry or dispatch the same webhook again.
func (r *SMSRepository) CreateReceivedOnce(message *model.SMS) (bool, error) {
	var count int64
	if err := r.db.Model(&model.SMS{}).Where("iccid = ? AND type = ? AND raw_pdu = ?", message.ICCID, "received", message.RawPDU).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if err := r.db.Create(message).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (r *SMSRepository) FindByICCID(iccid string) ([]model.SMS, error) {
	var smsList []model.SMS
	err := r.db.Where("iccid = ?", iccid).Order("timestamp desc").Find(&smsList).Error
	return smsList, err
}
