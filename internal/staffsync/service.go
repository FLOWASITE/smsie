package staffsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

// Outcome — kết quả một lượt đồng bộ, giữ trong bộ nhớ để trang Nhân sự hiện "lần gần nhất".
type Outcome struct {
	At       time.Time                `json:"at"`
	Auto     bool                     `json:"auto"` // true = lượt tự chạy theo lịch
	OK       bool                     `json:"ok"`
	Error    string                   `json:"error,omitempty"` // câu báo lỗi khi không OK
	Code     string                   `json:"code,omitempty"`  // mã lỗi gốc của hợp đồng
	Result   *repository.ImportResult `json:"-"`
	Created  int                      `json:"created"`
	Updated  int                      `json:"updated"`
	Left     int                      `json:"left"`
	Returned int                      `json:"returned"`
}

// Service ghép client Connect với luật nhập của kho nhân sự.
type Service struct {
	db      *gorm.DB
	repo    *repository.StaffRepository
	client  *Client
	enabled bool
	hour    int
	now     func() time.Time

	mu   sync.Mutex
	last *Outcome
	run  sync.Mutex // một lượt đồng bộ tại một thời điểm
}

func NewService(db *gorm.DB, client *Client, enabled bool, hour int) *Service {
	return &Service{db: db, repo: repository.NewStaffRepository(db), client: client, enabled: enabled, hour: hour, now: time.Now}
}

// Status — thứ giao diện cần để quyết định có hiện nút "Đồng bộ từ KeHub" không. Không bao giờ chứa khoá.
type Status struct {
	Enabled    bool     `json:"enabled"`
	Configured bool     `json:"configured"`
	DonVi      string   `json:"don_vi,omitempty"`
	AutoHour   *int     `json:"auto_hour,omitempty"`
	Last       *Outcome `json:"last,omitempty"`
}

func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Enabled: s.enabled, Configured: s.client.Configured(), DonVi: s.client.DonVi(), Last: s.last}
	if s.enabled && s.hour >= 0 && s.hour <= 23 {
		h := s.hour
		st.AutoHour = &h
	}
	return st
}

// ErrDisabled — staff_sync.enabled = false.
var ErrDisabled = errors.New("staff sync is disabled")

// Sync lấy danh sách từ Connect rồi xem trước (apply=false) hoặc ghi (apply=true) theo đúng luật của nhập Excel:
// khớp theo mã nhân viên rồi email, chỉ thêm và cập nhật, người nghỉ việc không bị tự thu hồi SIM.
// Lỗi phía xa không đụng tới dữ liệu đang có.
func (s *Service) Sync(ctx context.Context, apply, auto bool) (*repository.ImportResult, []string, error) {
	if !s.enabled {
		return nil, nil, ErrDisabled
	}
	s.run.Lock()
	defer s.run.Unlock()

	fetched, err := s.client.Fetch(ctx)
	if err != nil {
		if apply {
			s.remember(&Outcome{At: s.now(), Auto: auto, Error: userMessage(err), Code: errorCode(err)})
		}
		return nil, nil, err
	}
	// Hợp đồng luôn có trường sdt nên số điện thoại được phép cập nhật (ô trống vẫn không xoá số đang có).
	res, err := s.repo.Import(fetched.People, true, apply, s.now())
	if err != nil {
		if apply {
			s.remember(&Outcome{At: s.now(), Auto: auto, Error: "Không ghi được danh sách nhân sự.", Code: "luu_loi"})
		}
		return nil, nil, err
	}
	if apply {
		s.remember(&Outcome{At: s.now(), Auto: auto, OK: true, Result: res, Created: res.Created, Updated: res.Updated, Left: res.Left, Returned: res.Returned})
	}
	return res, fetched.Warnings, nil
}

func (s *Service) remember(o *Outcome) {
	s.mu.Lock()
	s.last = o
	s.mu.Unlock()
}

func userMessage(err error) string {
	var re *RemoteError
	if errors.As(err, &re) {
		return re.Message
	}
	if errors.Is(err, ErrNotConfigured) {
		return "Đồng bộ chưa được cấu hình đủ (cần mã app, mã đơn vị và khoá máy)."
	}
	return "Không đồng bộ được."
}

func errorCode(err error) string {
	var re *RemoteError
	if errors.As(err, &re) {
		return re.Code
	}
	if errors.Is(err, ErrNotConfigured) {
		return "chua_cau_hinh"
	}
	return "loi"
}

// UserMessage / ErrorCode cho tầng API dùng lại đúng câu chữ.
func UserMessage(err error) string { return userMessage(err) }
func ErrorCode(err error) string   { return errorCode(err) }

// nextRun: lần chạy kế tiếp lúc `hour`:00 giờ địa phương.
func nextRun(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Run tự đồng bộ mỗi ngày lúc hour:00 (không chạy lúc khởi động). Kết quả vào nhật ký với người thực hiện "system".
func (s *Service) Run(stop <-chan struct{}) {
	if !s.enabled || s.hour < 0 || s.hour > 23 {
		return
	}
	for {
		wait := time.Until(nextRun(s.now(), s.hour))
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		res, _, err := s.Sync(ctx, true, true)
		cancel()
		entry := audit.Entry{Username: "system", Action: "staff.sync", Status: 200}
		if err != nil {
			entry.Status, entry.Detail = 502, userMessage(err)
			logger.Log.Warnf("staff sync: lượt tự động thất bại: %s", userMessage(err))
		} else {
			entry.Target = Summary(res)
		}
		if aerr := audit.Record(s.db, entry); aerr != nil {
			logger.Log.Warnf("staff sync: không ghi được nhật ký: %v", aerr)
		}
	}
}

// Summary — một dòng tóm tắt cho nhật ký.
func Summary(res *repository.ImportResult) string {
	if res == nil {
		return ""
	}
	return fmt.Sprintf("thêm %d, sửa %d, nghỉ %d, làm lại %d", res.Created, res.Updated, res.Left, res.Returned)
}
