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
	return r.AlertedSince(iccid, kind, time.Now().Add(-d))
}

// AlertedSince: đã có cảnh báo loại này từ mốc `since` chưa. Nhận mốc thời gian tường minh để bên gọi dùng
// đồng hồ của chính nó (keepalive tiêm đồng hồ khi test) thay vì lệch với time.Now() ở đây.
func (r *SimAlertRepository) AlertedSince(iccid, kind string, since time.Time) (bool, error) {
	var n int64
	err := r.db.Model(&model.SimAlert{}).Where("iccid = ? AND kind = ? AND sent_at >= ?", iccid, kind, since).Count(&n).Error
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
