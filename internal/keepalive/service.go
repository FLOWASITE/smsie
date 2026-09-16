package keepalive

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/worker"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

// ErrDisabled: công tắc tổng keepalive.enabled=false — không gửi ở bất kỳ tầng nào.
var ErrDisabled = errors.New("keepalive.enabled=false trong config.yaml")

// Sender gửi SMS từ SIM iccid; tách interface để test không cần modem thật.
type Sender interface {
	SendSMS(iccid, phone, msg string) error
}

type workerSender struct{ wm *worker.Manager }

func (s workerSender) SendSMS(iccid, phone, msg string) error {
	if s.wm == nil {
		return errors.New("modem offline")
	}
	w := s.wm.GetWorkerByICCID(iccid)
	if w == nil {
		return errors.New("modem offline")
	}
	return w.SendSMS(phone, msg)
}

type Service struct {
	db       *gorm.DB
	wm       *worker.Manager
	sender   Sender
	webhooks *logic.WebhookService
	cfg      config.KeepaliveConfig
	repo     *repository.KeepaliveRepository
	alerts   *repository.SimAlertRepository
	mu       sync.Mutex // runOne: RunAll và RunNow không chèn nhau trên cùng SIM
	now      func() time.Time
	gap      time.Duration // nghỉ giữa hai SIM trong RunAll; test đặt 0
}

// NewService: sender nil → gửi qua worker.Manager.
func NewService(db *gorm.DB, wm *worker.Manager, sender Sender, webhooks *logic.WebhookService, cfg config.KeepaliveConfig) *Service {
	if sender == nil {
		sender = workerSender{wm: wm}
	}
	return &Service{
		db: db, wm: wm, sender: sender, webhooks: webhooks, cfg: cfg,
		repo: repository.NewKeepaliveRepository(db), alerts: repository.NewSimAlertRepository(db),
		now: time.Now, gap: 5 * time.Second,
	}
}

func (s *Service) Config() config.KeepaliveConfig { return s.cfg }

// Item là một SIM đã đánh giá — API /keepalive/status trả mảng này.
type Item struct {
	ICCID          string              `json:"iccid"`
	PhoneNumber    string              `json:"phone_number,omitempty"`
	SlotNumber     *int                `json:"slot_number,omitempty"`
	Enabled        bool                `json:"enabled"`
	IntervalDays   int                 `json:"interval_days"`
	LastActivityAt *time.Time          `json:"last_activity_at,omitempty"`
	NextDueAt      *time.Time          `json:"next_due_at,omitempty"`
	SentThisMonth  int                 `json:"sent_this_month"`
	LastRun        *model.KeepaliveRun `json:"last_run,omitempty"`
	Online         bool                `json:"online"`

	firstSeen  *time.Time
	inBay      bool
	registered bool
	operator   string
}

// Collect gom modems + khay + hoạt động cuối; không ghi gì.
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
	lastSMS, err := s.repo.LastActivityAll()
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]Item, 0, len(modems))
	for _, m := range modems {
		it := Item{ICCID: m.ICCID, PhoneNumber: m.PhoneNumber, SlotNumber: m.SlotNumber, Enabled: m.KeepaliveEnabled, IntervalDays: s.cfg.IntervalDays, firstSeen: m.FirstSeenAt}
		if m.KeepaliveInterval != nil && *m.KeepaliveInterval > 0 {
			it.IntervalDays = *m.KeepaliveInterval
		}
		if slot, ok := slotByICCID[m.ICCID]; ok {
			it.inBay = true
			if slot != nil {
				it.SlotNumber = slot
			}
		}
		if t, ok := lastSMS[m.ICCID]; ok {
			it.LastActivityAt = &t
		}
		if t, err := s.repo.LastSentAt(m.ICCID); err != nil {
			return nil, err
		} else if t != nil && (it.LastActivityAt == nil || t.After(*it.LastActivityAt)) {
			it.LastActivityAt = t
		}
		if ref := firstOf(it.LastActivityAt, it.firstSeen); ref != nil {
			due := ref.AddDate(0, 0, it.IntervalDays)
			it.NextDueAt = &due
		}
		if it.SentThisMonth, err = s.repo.SentCountThisMonth(m.ICCID, now); err != nil {
			return nil, err
		}
		if it.LastRun, err = s.repo.LastRun(m.ICCID); err != nil {
			return nil, err
		}
		if s.wm != nil {
			if rt, ok := s.wm.GetWorkerByICCID(m.ICCID).RuntimeModemState(); ok && rt.Status == "online" {
				it.Online = true
				it.operator = rt.Operator
				it.registered = rt.Registration == "Home Network" || rt.Registration == "Roaming"
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func firstOf(a, b *time.Time) *time.Time {
	if a != nil {
		return a
	}
	return b
}

// RunAll duyệt SIM bật + online + đăng ký mạng, chạy những SIM tới hạn; trả số run đã ghi.
func (s *Service) RunAll() (int, error) { return s.runAll(nil) }

func (s *Service) runAll(stop <-chan struct{}) (int, error) {
	if !s.cfg.Enabled {
		return 0, nil
	}
	items, err := s.Collect()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if !it.Enabled || !it.Online || !it.registered || !isDue(s.now(), it.LastActivityAt, it.firstSeen, it.IntervalDays) {
			continue
		}
		if n > 0 {
			select {
			case <-stop:
				return n, nil
			case <-time.After(s.gap):
			}
		}
		run, err := s.runOne(it, items, false)
		if err != nil {
			logger.Log.Errorf("keepalive: %s lỗi: %v", it.ICCID, err)
			continue
		}
		if run != nil {
			n++
		}
	}
	return n, nil
}

// RunOne chạy ngay một SIM; force bỏ qua điều kiện tới hạn nhưng vẫn tôn trọng trần tháng.
// Không tới hạn và !force → (nil, nil), không ghi run.
func (s *Service) RunOne(iccid string, force bool) (*model.KeepaliveRun, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	items, err := s.Collect()
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ICCID != iccid {
			continue
		}
		if !it.Enabled {
			return nil, errors.New("SIM chưa bật nuôi SIM")
		}
		if !force && !isDue(s.now(), it.LastActivityAt, it.firstSeen, it.IntervalDays) {
			return nil, nil
		}
		return s.runOne(it, items, force)
	}
	return nil, gorm.ErrRecordNotFound
}

// runOne: Collect() chạy NGOÀI khoá nên hai lượt chồng nhau (scheduler + RunNow) mang cùng số cũ;
// trần tháng và mốc gửi cuối phải đọc lại từ nhật ký TRONG khoá, nếu không gửi vượt trần / gửi đúp.
func (s *Service) runOne(it Item, all []Item, force bool) (*model.KeepaliveRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var err error
	if it.SentThisMonth, err = s.repo.SentCountThisMonth(it.ICCID, now); err != nil {
		return nil, err
	}
	if !force {
		last, err := s.repo.LastSentAt(it.ICCID)
		if err != nil {
			return nil, err
		}
		if last != nil && (it.LastActivityAt == nil || last.After(*it.LastActivityAt)) {
			it.LastActivityAt = last
		}
		if !isDue(now, it.LastActivityAt, it.firstSeen, it.IntervalDays) {
			return nil, nil
		}
	}
	run := &model.KeepaliveRun{ICCID: it.ICCID, RanAt: now}
	alertWindow := 7 * 24 * time.Hour
	if it.SentThisMonth >= s.cfg.MaxPerMonth {
		run.Status, run.Reason = model.KeepaliveSkipped, fmt.Sprintf("đã đạt trần %d lần/tháng", s.cfg.MaxPerMonth)
		alertWindow = 30 * 24 * time.Hour
	} else if target, ok := pickTarget(s.candidate(it), s.candidates(all, now)); !ok {
		run.Status, run.Reason = model.KeepaliveSkipped, "không có SIM cùng nhà mạng có số điện thoại trong khay"
	} else if msg, err := s.render(now); err != nil {
		run.Status, run.Reason = model.KeepaliveFailed, "mẫu tin nhắn lỗi: "+err.Error()
	} else {
		run.TargetICCID, run.TargetPhone = target.ICCID, target.Phone
		if err := s.sender.SendSMS(it.ICCID, target.Phone, msg); err != nil {
			run.Status, run.Reason = model.KeepaliveFailed, err.Error()
		} else {
			run.Status = model.KeepaliveSent
		}
	}
	if err := s.repo.Add(run); err != nil {
		return nil, err
	}
	if run.Status == model.KeepaliveSent {
		_ = audit.Record(s.db, audit.Entry{Username: "system", Action: "keepalive.send", ICCID: it.ICCID, Target: run.TargetPhone, Status: 200})
	} else if run.Status == model.KeepaliveFailed {
		_ = audit.Record(s.db, audit.Entry{Username: "system", Action: "keepalive.send", ICCID: it.ICCID, Target: run.TargetPhone, Detail: run.Reason, Status: 500})
	}
	if run.Status != model.KeepaliveSent {
		s.alert(it, run.Reason, alertWindow)
	}
	return run, nil
}

func (s *Service) candidate(it Item) Candidate {
	return Candidate{ICCID: it.ICCID, Phone: it.PhoneNumber, Operator: it.operator, Online: it.Online}
}

// candidates: chỉ SIM đang trong khay; TargetCount đếm 30 ngày để xoay vòng.
func (s *Service) candidates(all []Item, now time.Time) []Candidate {
	counts, err := s.repo.TargetCounts(now.AddDate(0, 0, -30))
	if err != nil {
		logger.Log.Errorf("keepalive: đếm đích lỗi: %v", err)
	}
	var out []Candidate
	for _, it := range all {
		if !it.inBay {
			continue
		}
		c := s.candidate(it)
		c.TargetCount = counts[it.ICCID]
		out = append(out, c)
	}
	return out
}

// render nội dung SMS từ cfg.Message qua text/template với {{.Date}} = yyyy-mm-dd;
// lỗi template → trả lỗi, KHÔNG gửi nguyên văn "{{" (tốn tiền cho tin rác).
func (s *Service) render(now time.Time) (string, error) {
	t, err := template.New("msg").Parse(s.cfg.Message)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, struct{ Date string }{now.Format("2006-01-02")}); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (s *Service) alert(it Item, reason string, window time.Duration) {
	if done, err := s.alerts.AlertedWithin(it.ICCID, model.SimAlertKeepalive, window); err != nil || done {
		if err != nil {
			logger.Log.Errorf("keepalive: kiểm alert %s lỗi: %v", it.ICCID, err)
		}
		return
	}
	if err := s.alerts.Add(&model.SimAlert{ICCID: it.ICCID, Kind: model.SimAlertKeepalive, Detail: reason, SentAt: s.now()}); err != nil {
		logger.Log.Errorf("keepalive: ghi alert %s lỗi: %v", it.ICCID, err)
		return
	}
	if s.webhooks != nil {
		s.webhooks.DispatchText(it.ICCID, alertText(it, reason))
	}
}

func alertText(it Item, reason string) string {
	name := it.PhoneNumber
	if name == "" {
		name = it.ICCID
	}
	if it.SlotNumber != nil {
		name += fmt.Sprintf(" (khe %d)", *it.SlotNumber)
	}
	return fmt.Sprintf("🔁 SIM %s: nuôi SIM thất bại — %s", name, reason)
}

// Run chạy tới khi stop đóng: mỗi ngày cfg.RunHour:00 địa phương gọi RunAll (không chạy lúc boot).
func (s *Service) Run(stop <-chan struct{}) {
	for {
		daily := time.NewTimer(time.Until(nextRun(time.Now(), s.cfg.RunHour)))
		select {
		case <-stop:
			daily.Stop()
			return
		case <-daily.C:
		}
		n, err := s.runAll(stop)
		if err != nil {
			logger.Log.Errorf("keepalive: lượt chạy lỗi: %v", err)
		} else {
			logger.Log.Infof("keepalive: đã chạy %d SIM", n)
		}
	}
}

func nextRun(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}
