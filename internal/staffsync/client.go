// Package staffsync lấy danh sách nhân sự KeHub qua JiOne Connect (chức năng `kehub.nhan_su`, route C12 của
// hợp đồng JiOne v1) để đồng bộ vào trang Nhân sự của smsie.
//
//	smsie ──GET /v1/kehub.nhan_su?client_id&don_vi, Bearer <khoá máy của app>──▶ JiOne Connect ──▶ KeHub
//
// smsie chỉ giữ khoá máy của chính nó; khoá tích hợp của KeHub nằm ở JiOne, không bao giờ tới đây.
// Gói này chỉ ĐỌC: không có đường nào ghi ngược về KeHub.
package staffsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pccr10001/smsie/internal/repository"
)

const (
	DefaultURL = "https://connect.jione.vn/v1/kehub.nhan_su"

	maxBody   = 8 << 20
	maxPeople = 5000
)

// ErrNotConfigured — thiếu địa chỉ, mã đơn vị hoặc khoá máy.
var ErrNotConfigured = errors.New("staff sync is not configured")

// RemoteError — JiOne Connect (hoặc KeHub phía sau nó) từ chối hay lỗi. Message là câu tiếng Việt nói rõ ai cần
// làm gì; Code là mã lỗi gốc để tra hợp đồng.
type RemoteError struct {
	Status  int
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("jione connect %d %s: %s", e.Status, e.Code, e.Message)
}

type Config struct {
	URL      string
	ClientID string
	DonVi    string
	Key      string
	HTTP     *http.Client
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.URL == "" {
		cfg.URL = DefaultURL
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.DonVi = strings.TrimSpace(cfg.DonVi)
	cfg.Key = strings.TrimSpace(cfg.Key)
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{
			Timeout: 20 * time.Second,
			// Không theo redirect: khoá máy nằm trong header, chỉ được tới đúng địa chỉ đã cấu hình.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Client{cfg: cfg, http: h}
}

// Configured: đủ thông tin để gọi.
func (c *Client) Configured() bool {
	return c.cfg.ClientID != "" && c.cfg.DonVi != "" && c.cfg.Key != ""
}

func (c *Client) DonVi() string { return c.cfg.DonVi }

// endpoint dựng URL gọi; chỉ nhận https, trừ địa chỉ loopback (chạy thử cục bộ) — khoá máy không đi qua http trần.
func (c *Client) endpoint() (string, error) {
	u, err := url.Parse(c.cfg.URL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%w: staff_sync.url không hợp lệ", ErrNotConfigured)
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !(u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
			return "", fmt.Errorf("%w: staff_sync.url phải là https", ErrNotConfigured)
		}
	}
	q := u.Query()
	q.Set("client_id", c.cfg.ClientID)
	q.Set("don_vi", c.cfg.DonVi)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type remotePerson struct {
	MaNV     string `json:"ma_nv"`
	HoTen    string `json:"ho_ten"`
	PhongBan string `json:"phong_ban"`
	BoPhan   string `json:"bo_phan"`
	Email    string `json:"email"`
	SDT      string `json:"sdt"`
	DaNghi   bool   `json:"da_nghi"`
}

// Fetched — danh sách đã chuẩn hoá cho repository.Import, kèm các dòng bị bỏ lúc đọc.
type Fetched struct {
	People   []repository.ImportPerson
	Warnings []string
}

func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// Fetch gọi Connect một lần và trả toàn bộ nhân sự của đơn vị (kể cả người đã nghỉ).
func (c *Client) Fetch(ctx context.Context) (*Fetched, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	target, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// Không chép err vào thông báo: lỗi của net/http có thể mang cả URL.
		return nil, &RemoteError{Status: 0, Code: "khong_ket_noi_duoc", Message: "Không kết nối được JiOne Connect. Dữ liệu hiện có được giữ nguyên; thử lại sau."}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, &RemoteError{Status: resp.StatusCode, Code: "phan_hoi_qua_lon", Message: "Phản hồi từ JiOne Connect không đọc được hoặc quá lớn."}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, describeRemote(resp.StatusCode, body)
	}

	var payload struct {
		NhanSu []remotePerson `json:"nhan_su"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.NhanSu == nil {
		return nil, &RemoteError{Status: resp.StatusCode, Code: "phan_hoi_sai_dang", Message: "Phản hồi từ JiOne Connect không đúng hợp đồng (thiếu danh sách nhan_su)."}
	}
	if len(payload.NhanSu) > maxPeople {
		return nil, &RemoteError{Status: resp.StatusCode, Code: "qua_nhieu_nhan_su", Message: fmt.Sprintf("Danh sách có hơn %d nhân sự, vượt giới hạn một lần đồng bộ.", maxPeople)}
	}
	out := &Fetched{People: make([]repository.ImportPerson, 0, len(payload.NhanSu)), Warnings: []string{}}
	for i, p := range payload.NhanSu {
		person := repository.ImportPerson{
			Row:        i + 1,
			Code:       trimTo(p.MaNV, 40),
			Name:       trimTo(p.HoTen, 120),
			Department: trimTo(p.PhongBan, 120),
			Email:      strings.ToLower(trimTo(p.Email, 254)),
			Phone:      trimTo(p.SDT, 32),
			Left:       p.DaNghi,
		}
		if person.Department == "" {
			person.Department = trimTo(p.BoPhan, 120)
		}
		if person.Email != "" && !strings.Contains(person.Email, "@") {
			person.Email = ""
		}
		if person.Name == "" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("Mục %d (%s): không có họ tên", i+1, person.Code))
			continue
		}
		out.People = append(out.People, person)
	}
	return out, nil
}

// describeRemote đổi mã lỗi của hợp đồng nhóm C thành câu nói rõ AI phải làm gì — hầu hết là việc của người
// vận hành JiOne / KeHub chứ không phải của người đang bấm nút.
func describeRemote(status int, body []byte) *RemoteError {
	var e struct {
		Error string `json:"error"`
		LyDo  string `json:"ly_do"`
	}
	_ = json.Unmarshal(body, &e)
	code := e.Error
	msg := ""
	switch code {
	case "invalid_request":
		msg = "JiOne Connect không nhận yêu cầu: kiểm tra staff_sync.client_id và staff_sync.don_vi trong cấu hình smsie."
	case "invalid_client":
		msg = "JiOne từ chối khoá máy của smsie. Nhờ người vận hành JiOne cấp lại khoá, rồi đặt vào STAFF_SYNC_KEY."
	case "don_vi_la":
		msg = "JiOne không biết đơn vị này: kiểm tra staff_sync.don_vi trong cấu hình smsie."
	case "chuc_nang_la":
		msg = "JiOne chưa có chức năng kehub.nhan_su (phía JiOne chưa triển khai)."
	case "chuc_nang_chua_cap":
		msg = "Đơn vị chưa được cấp chức năng kehub.nhan_su cho smsie. Nhờ người vận hành JiOne cấp trong Console."
		if e.LyDo != "" {
			msg += " (lý do: " + e.LyDo + ")"
		}
	case "chua_ket_noi":
		msg = "Đơn vị chưa được nối với KeHub ở JiOne. Nhờ người vận hành nhập khoá tích hợp KeHub của đơn vị."
	case "ket_noi_het_han":
		msg = "KeHub từ chối khoá tích hợp của đơn vị (đã thu hồi hoặc sai). Nhờ người vận hành KeHub cấp lại và nhập vào JiOne."
	case "chua_cau_hinh_kehub":
		msg = "JiOne chưa được khai địa chỉ KeHub. Việc của người vận hành JiOne."
	case "nen_tang_loi":
		msg = "KeHub đang lỗi hoặc quá tải. Dữ liệu hiện có được giữ nguyên; thử lại sau."
	default:
		code = fmt.Sprintf("http_%d", status)
		msg = fmt.Sprintf("JiOne Connect trả mã %d. Dữ liệu hiện có được giữ nguyên; thử lại sau.", status)
	}
	return &RemoteError{Status: status, Code: code, Message: msg}
}
