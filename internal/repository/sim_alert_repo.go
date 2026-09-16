package repository

import (
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

type SimAlertRepository struct {
	db *gorm.DB
}

func NewSimAlertRepository(db *gorm.DB) *SimAlertRepository { return &SimAlertRepository{db: db} }

func (r *SimAlertRepository) AlertedWithin(iccid, kind string, d time.Duration) (bool, error) {
	var n int64
	err := r.db.Model(&model.SimAlert{}).Where("iccid = ? AND kind = ? AND sent_at >= ?", iccid, kind, time.Now().Add(-d)).Count(&n).Error
	return n > 0, err
}

func (r *SimAlertRepository) Add(a *model.SimAlert) error {
	return r.db.Create(a).Error
}

func (r *SimAlertRepository) List(page, size int) ([]model.SimAlert, int64, error) {
	if size <= 0 {
		size = 50
	}
	if page <= 0 {
		page = 1
	}
	q := r.db.Model(&model.SimAlert{})
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.SimAlert
	err := q.Order("sent_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&out).Error
	return out, total, err
}
