package staffsync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pccr10001/smsie/internal/repository"
)

func server(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, New(Config{URL: srv.URL + "/v1/kehub.nhan_su", ClientID: "smsie", DonVi: "dv-1", Key: "khoa-may-bi-mat"})
}

func TestFetchSendsContractRequestAndMapsPeople(t *testing.T) {
	var gotAuth, gotQuery, gotPath, gotMethod string
	_, c := server(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotQuery, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.RawQuery, r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"to_chuc":"cty_abc","luc":"2026-11-11T09:00:00+07:00","nhan_su":[
			{"ma_nv":" NV012 ","ho_ten":"Nguyễn Thị Lan","phong_ban":"Kinh doanh","bo_phan":"Tổ 1","email":"Lan@Cty.vn","sdt":"0988111222","da_nghi":false,"ngay_nghi_viec":null,"so_cccd":"079123456789"},
			{"ma_nv":"NV027","ho_ten":"Trần Văn Minh","phong_ban":"","bo_phan":"CSKH","email":"khong-phai-email","sdt":null,"da_nghi":true,"ngay_nghi_viec":"2026-10-31"},
			{"ma_nv":"NV099","ho_ten":"  ","da_nghi":false}
		]}`))
	})
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/kehub.nhan_su" || gotAuth != "Bearer khoa-may-bi-mat" || gotQuery != "client_id=smsie&don_vi=dv-1" {
		t.Fatalf("yêu cầu sai hợp đồng: %s %s?%s auth=%q", gotMethod, gotPath, gotQuery, gotAuth)
	}
	want := []repository.ImportPerson{
		{Row: 1, Code: "NV012", Name: "Nguyễn Thị Lan", Department: "Kinh doanh", Email: "lan@cty.vn", Phone: "0988111222"},
		{Row: 2, Code: "NV027", Name: "Trần Văn Minh", Department: "CSKH", Left: true}, // thiếu phòng ban → bộ phận; email rác bị bỏ
	}
	if len(got.People) != 2 || got.People[0] != want[0] || got.People[1] != want[1] {
		t.Fatalf("people = %+v", got.People)
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "Mục 3 (NV099): không có họ tên" {
		t.Fatalf("warnings = %v", got.Warnings)
	}
}

func TestFetchExplainsEachContractError(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
		says   string
	}{
		{401, `{"error":"invalid_client"}`, "invalid_client", "khoá máy"},
		{403, `{"error":"chuc_nang_chua_cap","ly_do":"chua_cap","chuc_nang":"kehub.nhan_su"}`, "chuc_nang_chua_cap", "lý do: chua_cap"},
		{404, `{"error":"don_vi_la"}`, "don_vi_la", "staff_sync.don_vi"},
		{409, `{"error":"chua_ket_noi"}`, "chua_ket_noi", "nối với KeHub"},
		{409, `{"error":"ket_noi_het_han"}`, "ket_noi_het_han", "KeHub từ chối"},
		{503, `{"error":"chua_cau_hinh_kehub"}`, "chua_cau_hinh_kehub", "địa chỉ KeHub"},
		{502, `{"error":"nen_tang_loi","ma_nen_tang":"502"}`, "nen_tang_loi", "giữ nguyên"},
		{500, `<html>oops</html>`, "http_500", "mã 500"},
	}
	for _, tc := range cases {
		_, c := server(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.Fetch(context.Background())
		var re *RemoteError
		if !errors.As(err, &re) || re.Status != tc.status || re.Code != tc.code || !strings.Contains(re.Message, tc.says) {
			t.Errorf("%d %s → %+v (err %v), muốn mã %s, câu có %q", tc.status, tc.body, re, err, tc.code, tc.says)
		}
		if re != nil && strings.Contains(re.Error(), "khoa-may-bi-mat") {
			t.Errorf("thông báo lỗi lộ khoá máy: %s", re.Error())
		}
	}
}

func TestFetchRejectsMalformedAndOversizedPayloads(t *testing.T) {
	for name, body := range map[string]string{
		"không phải JSON":    `xin chào`,
		"thiếu nhan_su":      `{"to_chuc":"cty_abc"}`,
		"nhan_su không mảng": `{"nhan_su":"x"}`,
	} {
		_, c := server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		var re *RemoteError
		if _, err := c.Fetch(context.Background()); !errors.As(err, &re) || re.Code != "phan_hoi_sai_dang" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Danh sách rỗng hợp lệ: đơn vị chưa có nhân sự nào.
	_, c := server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"nhan_su":[]}`)) })
	if got, err := c.Fetch(context.Background()); err != nil || len(got.People) != 0 {
		t.Fatalf("danh sách rỗng: %+v %v", got, err)
	}
}

func TestNotConfiguredAndInsecureURL(t *testing.T) {
	for _, cfg := range []Config{
		{ClientID: "smsie", DonVi: "dv"}, // thiếu khoá
		{ClientID: "smsie", Key: "k"},    // thiếu đơn vị
		{DonVi: "dv", Key: "k"},          // thiếu client_id
		{URL: "http://connect.example.com/v1/kehub.nhan_su", ClientID: "smsie", DonVi: "dv", Key: "k"}, // http trần ra ngoài
		{URL: "://hỏng", ClientID: "smsie", DonVi: "dv", Key: "k"},
	} {
		if _, err := New(cfg).Fetch(context.Background()); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("%+v: err = %v, muốn ErrNotConfigured", cfg, err)
		}
	}
	if c := New(Config{ClientID: "smsie", DonVi: "dv", Key: "k"}); !c.Configured() {
		t.Fatal("đủ ba giá trị thì phải tính là đã cấu hình (URL lấy mặc định)")
	}
	if u, err := New(Config{ClientID: "smsie", DonVi: "dv", Key: "k"}).endpoint(); err != nil || u != "https://connect.jione.vn/v1/kehub.nhan_su?client_id=smsie&don_vi=dv" {
		t.Fatalf("địa chỉ mặc định = %q, err = %v", u, err)
	}
}

func TestFetchDoesNotFollowRedirectsWithTheKey(t *testing.T) {
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
		_, _ = w.Write([]byte(`{"nhan_su":[]}`))
	}))
	defer other.Close()
	_, c := server(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) })
	_, err := c.Fetch(context.Background())
	var re *RemoteError
	if !errors.As(err, &re) || re.Status != http.StatusFound || leaked {
		t.Fatalf("redirect phải bị coi là lỗi và khoá không được đi theo: err = %v, lộ khoá = %v", err, leaked)
	}
}
