package balance

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/worker"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

type Scheduler struct {
	db       *gorm.DB
	wm       *worker.Manager
	webhooks *logic.WebhookService
	cfg      config.BalanceConfig
	repo     *repository.BalanceRepository
	runMu    sync.Mutex
}

func NewScheduler(db *gorm.DB, wm *worker.Manager, webhooks *logic.WebhookService, cfg config.BalanceConfig) *Scheduler {
	return &Scheduler{db: db, wm: wm, webhooks: webhooks, cfg: cfg, repo: repository.NewBalanceRepository(db)}
}

// StatusItem là một SIM đã đánh giá — API /balance/status trả nguyên mảng này.
type StatusItem struct {
	ICCID            string                  `json:"iccid"`
	PhoneNumber      string                  `json:"phone_number,omitempty"`
	SlotNumber       *int                    `json:"slot_number,omitempty"`
	BalanceVND       int64                   `json:"balance_vnd"`
	BalanceUpdatedAt *time.Time              `json:"balance_updated_at,omitempty"`
	ThresholdVND     int64                   `json:"threshold_vnd"`
	ThresholdSource  string                  `json:"threshold_source"`
	DaysLeft         *float64                `json:"days_left,omitempty"`
	Level            string                  `json:"level"`
	Snapshots        []model.BalanceSnapshot `json:"snapshots"`
}

// Run chạy tới khi stop đóng: Evaluate sau 2 phút; mỗi ngày CheckHour:00 địa phương ReadAll rồi 15 phút sau Evaluate.
func (s *Scheduler) Run(stop <-chan struct{}) {
	initial := time.NewTimer(2 * time.Minute)
	defer initial.Stop()
	for {
		daily := time.NewTimer(time.Until(s.nextCheck(time.Now())))
		select {
		case <-stop:
			daily.Stop()
			return
		case <-initial.C:
			daily.Stop()
			s.evaluateLogged()
			continue
		case <-daily.C:
		}
		if s.cfg.Enabled {
			n := s.ReadAll()
			logger.Log.Infof("balance: đã yêu cầu đọc số dư %d SIM", n)
			after := time.NewTimer(15 * time.Minute)
			select {
			case <-stop:
				after.Stop()
				return
			case <-after.C:
			}
		}
		s.evaluateLogged()
	}
}

func (s *Scheduler) nextCheck(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), s.cfg.CheckHour, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func (s *Scheduler) evaluateLogged() {
	if _, err := s.Evaluate(); err != nil {
		logger.Log.Errorf("balance: đánh giá số dư lỗi: %v", err)
	}
}

func registered(reg string) bool { return reg == "Home Network" || reg == "Roaming" }

// ReadAll yêu cầu đọc *101# trên mọi SIM online, đã đăng ký mạng và chưa đọc trong 20h. Trả số SIM đã yêu cầu.
func (s *Scheduler) ReadAll() int {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	cutoff := time.Now().Add(-20 * time.Hour)
	n := 0
	for _, w := range s.wm.ActiveWorkers() {
		rt, ok := w.RuntimeModemState()
		if !ok || rt.Status != "online" || !registered(rt.Registration) {
			continue
		}
		var m model.Modem
		if err := s.db.Select("balance_updated_at").First(&m, "iccid = ?", rt.ICCID).Error; err == nil && m.BalanceUpdatedAt != nil && m.BalanceUpdatedAt.After(cutoff) {
			continue
		}
		if n > 0 {
			time.Sleep(3 * time.Second)
		}
		if err := w.RequestBalance(); err != nil && !errors.Is(err, worker.ErrBalanceCheckInProgress) {
			logger.Log.Warnf("balance: yêu cầu đọc số dư %s lỗi: %v", rt.ICCID, err)
			continue
		}
		n++
	}
	return n
}

// Evaluate đánh giá mọi SIM, ghi alert + bắn webhook cho low/forecast chưa cảnh báo trong 24h.
func (s *Scheduler) Evaluate() ([]StatusItem, error) { return s.collect(true) }

// Status như Evaluate nhưng không ghi gì.
func (s *Scheduler) Status() ([]StatusItem, error) { return s.collect(false) }

func (s *Scheduler) collect(write bool) ([]StatusItem, error) {
	var modems []model.Modem
	if err := s.db.Order("slot_number, iccid").Find(&modems).Error; err != nil {
		return nil, err
	}
	cfg := Config{LowThresholdVND: s.cfg.LowThresholdVND, ForecastDays: s.cfg.ForecastDays}
	out := make([]StatusItem, 0, len(modems))
	for _, m := range modems {
		snaps, err := s.repo.RecentSnapshots(m.ICCID, 7)
		if err != nil {
			return nil, err
		}
		in := Input{HasBalance: m.BalanceUpdatedAt != nil, VND: m.BalanceVND, SimThreshold: m.LowBalanceVND}
		for _, sn := range snaps {
			in.Points = append(in.Points, Point{At: sn.ReadAt, VND: sn.BalanceVND})
		}
		r := Evaluate(in, cfg)
		item := StatusItem{
			ICCID: m.ICCID, PhoneNumber: m.PhoneNumber, SlotNumber: m.SlotNumber,
			BalanceVND: m.BalanceVND, BalanceUpdatedAt: m.BalanceUpdatedAt,
			ThresholdVND: r.ThresholdVND, ThresholdSource: r.ThresholdSource,
			DaysLeft: r.DaysLeft, Level: r.Level, Snapshots: snaps,
		}
		out = append(out, item)
		if !write || (r.Level != LevelLow && r.Level != LevelForecast) {
			continue
		}
		if done, err := s.repo.AlertedWithin(m.ICCID, 24*time.Hour); err != nil || done {
			continue
		}
		if err := s.repo.AddAlert(&model.BalanceAlert{ICCID: m.ICCID, Kind: r.Level, BalanceVND: m.BalanceVND, DaysLeft: r.DaysLeft, SentAt: time.Now()}); err != nil {
			logger.Log.Errorf("balance: ghi alert %s lỗi: %v", m.ICCID, err)
			continue
		}
		s.webhooks.DispatchText(m.ICCID, alertText(item))
	}
	return out, nil
}

func alertText(it StatusItem) string {
	name := it.PhoneNumber
	if name == "" {
		name = it.ICCID
	}
	if it.SlotNumber != nil {
		name += fmt.Sprintf(" (khe %d)", *it.SlotNumber)
	}
	if it.Level == LevelLow {
		return fmt.Sprintf("⚠️ SIM %s: số dư %s đ, dưới ngưỡng %s đ", name, fmtVND(it.BalanceVND), fmtVND(it.ThresholdVND))
	}
	return fmt.Sprintf("⏳ SIM %s: số dư %s đ, dự kiến hết tiền sau ≈%.0f ngày", name, fmtVND(it.BalanceVND), *it.DaysLeft)
}

func fmtVND(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := ""
	if v < 0 {
		neg, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return neg + s
}
