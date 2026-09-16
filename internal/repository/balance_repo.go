package repository

import (
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

type BalanceRepository struct {
	db *gorm.DB
}

func NewBalanceRepository(db *gorm.DB) *BalanceRepository { return &BalanceRepository{db: db} }

func (r *BalanceRepository) AddSnapshot(iccid string, vnd int64, at time.Time) error {
	return r.db.Create(&model.BalanceSnapshot{ICCID: iccid, BalanceVND: vnd, ReadAt: at}).Error
}

// RecentSnapshots trả n mốc mới nhất, đã đảo lại tăng dần theo thời gian.
func (r *BalanceRepository) RecentSnapshots(iccid string, n int) ([]model.BalanceSnapshot, error) {
	var out []model.BalanceSnapshot
	err := r.db.Where("iccid = ?", iccid).Order("read_at DESC, id DESC").Limit(n).Find(&out).Error
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}

func (r *BalanceRepository) AlertedWithin(iccid string, d time.Duration) (bool, error) {
	var n int64
	err := r.db.Model(&model.BalanceAlert{}).Where("iccid = ? AND sent_at >= ?", iccid, time.Now().Add(-d)).Count(&n).Error
	return n > 0, err
}

func (r *BalanceRepository) AddAlert(a *model.BalanceAlert) error {
	return r.db.Create(a).Error
}

func (r *BalanceRepository) ListAlerts(page, size int) ([]model.BalanceAlert, int64, error) {
	if size <= 0 {
		size = 50
	}
	if page <= 0 {
		page = 1
	}
	q := r.db.Model(&model.BalanceAlert{})
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.BalanceAlert
	err := q.Order("sent_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&out).Error
	return out, total, err
}
