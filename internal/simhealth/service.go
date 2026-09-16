package simhealth

import (
	"fmt"
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

type Service struct {
	db       *gorm.DB
	wm       *worker.Manager
	webhooks *logic.WebhookService
	cfg      config.SimHealthConfig
	modems   *repository.ModemRepository
	alerts   *repository.SimAlertRepository
	mu       sync.Mutex // Evaluate: hai lượt chồng nhau không chèn alert trùng
}

func NewService(db *gorm.DB, wm *worker.Manager, webhooks *logic.WebhookService, cfg config.SimHealthConfig) *Service {
	return &Service{db: db, wm: wm, webhooks: webhooks, cfg: cfg, modems: repository.NewModemRepository(db), alerts: repository.NewSimAlertRepository(db)}
}

// Item là một SIM đã đánh giá — API /sim-health trả nguyên mảng này.
type Item struct {
	ICCID            string     `json:"iccid"`
	PhoneNumber      string     `json:"phone_number,omitempty"`
	SlotNumber       *int       `json:"slot_number,omitempty"`
	Online           bool       `json:"online"`
	LastRegisteredAt *time.Time `json:"last_registered_at,omitempty"`
	LastSMSAt        *time.Time `json:"last_sms_at,omitempty"`
	FirstSeenAt      *time.Time `json:"first_seen_at,omitempty"`
	InBay            bool       `json:"in_bay"`
	LastRemovedAt    *time.Time `json:"last_removed_at,omitempty"`
	Findings         []Finding  `json:"findings"`
}

// Collect gom modems + khay + SMS cuối + lần rút gần nhất rồi chạy Evaluate; không ghi gì.
func (s *Service) Collect() ([]Item, error) {
	var modems []model.Modem
	if err := s.db.Order("slot_number, iccid").Find(&modems).Error; err != nil {
		return nil, err
	}
	var bays []model.ModemBay
	if err := s.db.Where("current_iccid <> ''").Find(&bays).Error; err != nil {
		return nil, err
	}
	slotByICCID := make(map[string]*int, len(bays))
	for _, b := range bays {
		slotByICCID[b.CurrentICCID] = b.SlotNumber
	}
	lastSMS, err := s.modems.LastReceivedSMSAt()
	if err != nil {
		return nil, err
	}
	// Cùng bẫy SQLite MAX(datetime) như LastReceivedSMSAt: lấy dòng theo MAX(id) rồi đọc detected_at.
	var removed []model.SimSlotEvent
	if err := s.db.Select("iccid, detected_at").
		Where("id IN (?)", s.db.Model(&model.SimSlotEvent{}).Select("MAX(id)").Where("event = ?", model.SlotEventRemoved).Group("iccid")).
		Find(&removed).Error; err != nil {
		return nil, err
	}
	lastRemoved := make(map[string]time.Time, len(removed))
	for _, e := range removed {
		lastRemoved[e.ICCID] = e.DetectedAt
	}
	now := time.Now()
	cfg := Config{NoSMSDays: s.cfg.NoSMSDays, UnregisteredHours: s.cfg.UnregisteredHours, AbsentDays: s.cfg.AbsentDays}
	out := make([]Item, 0, len(modems))
	for _, m := range modems {
		it := Item{ICCID: m.ICCID, PhoneNumber: m.PhoneNumber, SlotNumber: m.SlotNumber, LastRegisteredAt: m.LastRegisteredAt, FirstSeenAt: m.FirstSeenAt, Findings: []Finding{}}
		if slot, ok := slotByICCID[m.ICCID]; ok {
			it.InBay = true
			if slot != nil {
				it.SlotNumber = slot
			}
		}
		if t, ok := lastSMS[m.ICCID]; ok {
			it.LastSMSAt = &t
		}
		if t, ok := lastRemoved[m.ICCID]; ok {
			it.LastRemovedAt = &t
		}
		if s.wm != nil {
			if rt, ok := s.wm.GetWorkerByICCID(m.ICCID).RuntimeModemState(); ok && rt.Status == "online" {
				it.Online = true
			}
		}
		in := Input{ICCID: m.ICCID, Online: it.Online, LastRegisteredAt: it.LastRegisteredAt, FirstSeenAt: it.FirstSeenAt, LastSMSAt: it.LastSMSAt, LastRemovedAt: it.LastRemovedAt, InBay: it.InBay}
		for _, f := range Evaluate(now, cfg, []Input{in}) {
			f.ICCID = "" // đã nằm trong Item
			it.Findings = append(it.Findings, f)
		}
		out = append(out, it)
	}
	return out, nil
}

// Evaluate = Collect rồi ghi sim_alerts + bắn webhook cho finding chưa cảnh báo trong RemindDays.
func (s *Service) Evaluate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.Collect()
	if err != nil {
		return err
	}
	remind := time.Duration(s.cfg.RemindDays) * 24 * time.Hour
	for _, it := range items {
		for _, f := range it.Findings {
			if done, err := s.alerts.AlertedWithin(it.ICCID, f.Kind, remind); err != nil || done {
				if err != nil {
					logger.Log.Errorf("simhealth: kiểm alert %s lỗi: %v", it.ICCID, err)
				}
				continue
			}
			if err := s.alerts.Add(&model.SimAlert{ICCID: it.ICCID, Kind: f.Kind, Detail: f.Detail, SentAt: time.Now()}); err != nil {
				logger.Log.Errorf("simhealth: ghi alert %s lỗi: %v", it.ICCID, err)
				continue
			}
			s.webhooks.DispatchText(it.ICCID, alertText(it, f))
		}
	}
	return nil
}

var emoji = map[string]string{model.SimAlertNoSMS: "🪦", model.SimAlertUnregistered: "📡", model.SimAlertAbsent: "📤"}

func alertText(it Item, f Finding) string {
	name := it.PhoneNumber
	if name == "" {
		name = it.ICCID
	}
	if it.SlotNumber != nil {
		name += fmt.Sprintf(" (khe %d)", *it.SlotNumber)
	}
	return fmt.Sprintf("%s SIM %s: %s", emoji[f.Kind], name, f.Detail)
}
