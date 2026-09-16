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
	runMu    sync.Mutex // Request: một lượt *101# tuần tự tại một thời điểm
	evalMu   sync.Mutex // Evaluate: daily và RunNow không chèn alert trùng
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
		daily := time.NewTimer(time.Until(nextCheck(time.Now(), s.cfg.CheckHour)))
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
			n := s.ReadAll(stop)
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

func nextCheck(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
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

// ReadAll = Request(Eligible(), stop): yêu cầu đọc *101# tuần tự, 3s giữa các SIM. Trả số SIM đã yêu cầu.
func (s *Scheduler) ReadAll(stop <-chan struct{}) int { return s.Request(s.Eligible(), stop) }

// Eligible liệt kê SIM online, đã đăng ký mạng và chưa đọc số dư trong 20h — không ngủ, không khoá.
func (s *Scheduler) Eligible() []*worker.ModemWorker {
	cutoff := time.Now().Add(-20 * time.Hour)
	var out []*worker.ModemWorker
	for _, w := range s.wm.ActiveWorkers() {
		rt, ok := w.RuntimeModemState()
		if !ok || rt.Status != "online" || !registered(rt.Registration) {
			continue
		}
		var m model.Modem
		if err := s.db.Select("balance_updated_at").First(&m, "iccid = ?", rt.ICCID).Error; err == nil && m.BalanceUpdatedAt != nil && m.BalanceUpdatedAt.After(cutoff) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Request gửi *101# cho từng worker, cách nhau 3s; stop=nil = không huỷ. Trả số SIM thực sự đã yêu cầu.
func (s *Scheduler) Request(ws []*worker.ModemWorker, stop <-chan struct{}) int {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	n := 0
	for i, w := range ws {
		if i > 0 {
			select {
			case <-stop:
				return n
			case <-time.After(3 * time.Second):
			}
		}
		if err := w.RequestBalance(); err != nil {
			if !errors.Is(err, worker.ErrBalanceCheckInProgress) {
				rt, _ := w.RuntimeModemState()
				logger.Log.Warnf("balance: yêu cầu đọc số dư %s lỗi: %v", rt.ICCID, err)
			}
			continue
		}
		n++
	}
	return n
}

// Evaluate đánh giá mọi SIM, ghi alert + bắn webhook cho low/forecast chưa cảnh báo trong 24h.
func (s *Scheduler) Evaluate() ([]StatusItem, error) {
	s.evalMu.Lock()
	defer s.evalMu.Unlock()
	return s.collect(true)
}

// Status như Evaluate nhưng không ghi gì.
func (s *Scheduler) Status() ([]StatusItem, error) { return s.collect(false) }

func (s *Scheduler) collect(write bool) ([]StatusItem, error) {
	var modems []model.Modem
	if err := s.db.Order("slot_number, iccid").Find(&modems).Error; err != nil {
		return nil, err
	}
	// Số khe thật nằm ở modem_bays (tray hiệu chuẩn); modems.slot_number chỉ là cache, có thể NULL.
	var bays []model.ModemBay
	if err := s.db.Where("current_iccid <> ''").Find(&bays).Error; err != nil {
		return nil, err
	}
	slotByICCID := make(map[string]*int, len(bays))
	for _, b := range bays {
		if b.SlotNumber != nil {
			slotByICCID[b.CurrentICCID] = b.SlotNumber
		}
	}
	cfg := Config{LowThresholdVND: s.cfg.LowThresholdVND, ForecastDays: s.cfg.ForecastDays}
	out := make([]StatusItem, 0, len(modems))
	for _, m := range modems {
		slot := m.SlotNumber
		if bs, ok := slotByICCID[m.ICCID]; ok {
			slot = bs
		}
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
			ICCID: m.ICCID, PhoneNumber: m.PhoneNumber, SlotNumber: slot,
			BalanceVND: m.BalanceVND, BalanceUpdatedAt: m.BalanceUpdatedAt,
			ThresholdVND: r.ThresholdVND, ThresholdSource: r.ThresholdSource,
			DaysLeft: r.DaysLeft, Level: r.Level, Snapshots: snaps,
		}
		out = append(out, item)
		if !write || (r.Level != LevelLow && r.Level != LevelForecast) {
			continue
		}
		if done, err := s.repo.AlertedWithin(m.ICCID, 24*time.Hour); err != nil || done {
			if err != nil {
				logger.Log.Errorf("balance: kiểm alert %s lỗi: %v", m.ICCID, err)
			}
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
