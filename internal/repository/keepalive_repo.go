package repository

import (
	"time"

	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

type KeepaliveRepository struct {
	db *gorm.DB
}

func NewKeepaliveRepository(db *gorm.DB) *KeepaliveRepository { return &KeepaliveRepository{db: db} }

func (r *KeepaliveRepository) Add(run *model.KeepaliveRun) error {
	return r.db.Create(run).Error
}

// SentCountThisMonth đếm run 'sent' trong tháng dương lịch chứa now, theo time.Local.
func (r *KeepaliveRepository) SentCountThisMonth(iccid string, now time.Time) (int, error) {
	now = now.In(time.Local)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)
	var n int64
	err := r.db.Model(&model.KeepaliveRun{}).
		Where("iccid = ? AND status = ? AND ran_at >= ? AND ran_at < ?", iccid, model.KeepaliveSent, start, end).
		Count(&n).Error
	return int(n), err
}

// LastRun trả run mới nhất mọi trạng thái, nil nếu chưa có.
func (r *KeepaliveRepository) LastRun(iccid string) (*model.KeepaliveRun, error) {
	var runs []model.KeepaliveRun
	err := r.db.Where("iccid = ?", iccid).Order("ran_at DESC, id DESC").Limit(1).Find(&runs).Error
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

// LastSentAt trả mốc run 'sent' mới nhất, nil nếu chưa có.
func (r *KeepaliveRepository) LastSentAt(iccid string) (*time.Time, error) {
	var runs []model.KeepaliveRun
	err := r.db.Where("iccid = ? AND status = ?", iccid, model.KeepaliveSent).Order("ran_at DESC, id DESC").Limit(1).Find(&runs).Error
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0].RanAt, nil
}

// TargetCounts đếm số lần mỗi ICCID được làm đích (status 'sent') kể từ since — dùng xoay vòng.
func (r *KeepaliveRepository) TargetCounts(since time.Time) (map[string]int, error) {
	var rows []struct {
		TargetICCID string `gorm:"column:target_iccid"`
		N           int
	}
	err := r.db.Model(&model.KeepaliveRun{}).Select("target_iccid, COUNT(*) AS n").
		Where("status = ? AND ran_at >= ? AND target_iccid <> ''", model.KeepaliveSent, since).
		Group("target_iccid").Scan(&rows).Error
	out := make(map[string]int, len(rows))
	for _, row := range rows {
		out[row.TargetICCID] = row.N
	}
	return out, err
}

// List phân trang nhật ký, mới nhất trước; iccid rỗng = mọi SIM.
func (r *KeepaliveRepository) List(iccid string, page, size int) ([]model.KeepaliveRun, int64, error) {
	if size <= 0 {
		size = 50
	}
	if page <= 0 {
		page = 1
	}
	q := r.db.Model(&model.KeepaliveRun{})
	if iccid != "" {
		q = q.Where("iccid = ?", iccid)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []model.KeepaliveRun
	err := q.Order("ran_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&out).Error
	return out, total, err
}

// LastActivityAll trả mốc hệ thống ghi SMS gần nhất theo ICCID, mọi type (sent + received).
// Dùng MAX(id) thay vì MAX(created_at) vì SQLite trả MAX(datetime) dạng chuỗi không scan được vào time.Time.
func (r *KeepaliveRepository) LastActivityAll() (map[string]time.Time, error) {
	var rows []model.SMS
	err := r.db.Select("iccid, created_at").
		Where("id IN (?)", r.db.Model(&model.SMS{}).Select("MAX(id)").Group("iccid")).
		Find(&rows).Error
	out := make(map[string]time.Time, len(rows))
	for _, s := range rows {
		out[s.ICCID] = s.CreatedAt
	}
	return out, err
}
