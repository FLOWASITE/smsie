# Đăng nhập smsie bằng tài khoản Jione

smsie là một sản phẩm của Jione, nên ngoài tài khoản/mật khẩu nội bộ, người dùng có thể đăng nhập bằng
tài khoản Jione qua OpenID Connect (Authorization Code + PKCE S256, client công khai, không `client_secret`) —
cùng hợp đồng với 1Web, FinAI, MapScan. Mật khẩu Jione chỉ được nhập tại `id.jione.vn`, smsie không bao giờ thấy.

## Bật đăng nhập

### 1. Đăng ký smsie là một client ở Jione

Thêm một dòng vào bảng `oidc_client` của Jione (cùng chỗ với các app khác trong
`jione-platform/db/control_plane_jione.sql`). `redirect_uris` phải khớp **chính xác** từng ký tự:

```sql
insert into jione.oidc_client (client_id, ten, redirect_uris) values
  ('smsie', 'SMSie', array[
    'https://smsie.jione.vn/oidc/callback',
    'https://sms.jione.vn/oidc/callback'
  ])
on conflict (client_id) do nothing;

-- Tuỳ chọn: để Jione báo cho smsie khi người dùng đăng xuất khỏi Jione (Back-Channel Logout)
update jione.oidc_client
   set backchannel_logout_uri = 'https://smsie.jione.vn/oidc/backchannel-logout'
 where client_id = 'smsie';
```

### 2. Cấu hình smsie

Trong `config.yaml` (hoặc biến môi trường `JIONE_ENABLED`, `JIONE_CLIENT_ID`…) rồi khởi động lại:

```yaml
jione:
  enabled: true
  issuer: "https://id.jione.vn"
  client_id: "smsie"
  redirect_origin: "https://smsie.jione.vn"   # origin công khai; bỏ trống = suy từ request (dùng khi có 2 tên miền)
  admin_emails: ["ban@congty.vn"]             # email Jione được vào với quyền admin
  auto_create: false
```

Khi `enabled: true` và có `client_id`, trang đăng nhập hiện thêm nút **Đăng nhập với Jione**.
Đăng nhập bằng mật khẩu vẫn dùng được song song.

## Ai được vào

smsie giữ OTP và tin nhắn, mà tài khoản Jione thì ai cũng tự đăng ký được, nên mặc định **không** mở cửa cho mọi
tài khoản Jione. Thứ tự xét khi một người đăng nhập bằng Jione:

1. Tài khoản Jione này đã gắn với một người dùng smsie → vào với đúng người đó.
2. Admin đã tạo sẵn người dùng có **Email Jione** trùng email (đã xác minh) của tài khoản → gắn vào, giữ nguyên
   vai trò và quyền SIM admin đã cấp. Tạo ở *Người dùng → Add*: điền email, mật khẩu được bỏ trống.
3. Email nằm trong `jione.admin_emails` → tạo (hoặc nâng) thành admin.
4. `jione.auto_create: true` → tạo user thường, **chưa có quyền với SIM nào** cho tới khi admin cấp.
5. Còn lại → từ chối, báo "chưa được cấp quyền dùng smsie".

Email chưa xác minh ở Jione luôn bị từ chối. Tài khoản tạo qua Jione không có mật khẩu nội bộ, nên không đăng
nhập bằng form mật khẩu được.

## Luồng và các điểm bảo vệ

| Bước | Đường dẫn | Việc xảy ra |
|---|---|---|
| Bắt đầu | `GET /oidc/login` | Sinh `state`, `nonce`, PKCE verifier; giữ trong cookie mã hoá AES-GCM (HttpOnly, SameSite=Lax, 10 phút) rồi chuyển sang Jione |
| Quay về | `GET /oidc/callback` | Kiểm `state` với cookie, đổi `code` kèm verifier, kiểm `id_token` (chữ ký RS256 qua JWKS, `iss`, `aud`, `exp`, `nonce`, email đã xác minh), tìm/gắn người dùng |
| Nhận phiên | `POST /api/v1/auth/jione/session` | Giao diện đổi cookie bàn giao (HttpOnly, 60 giây, dùng một lần) lấy JWT phiên — JWT không bao giờ nằm trên URL |
| Jione báo đăng xuất | `POST /oidc/backchannel-logout` | Kiểm `logout_token` rồi thu hồi mọi phiên smsie của người đó |

- Cookie lượt đăng nhập bị xoá ở mọi nhánh của callback; dán lại link callback cũ không tạo được phiên.
- Discovery phải khai đúng `issuer` đã cấu hình và các endpoint phải là HTTPS.
- Khi Jione xoay khoá ký, smsie tự tải lại JWKS khi gặp `kid` mới (không quá một lần mỗi phút).
- Mã `code`, `state`, `token` trên URL được che (`***`) trong log truy cập.
- Mọi lần đăng nhập Jione (được/không được) và đăng xuất do Jione báo đều ghi vào *Nhật ký*
  (`auth.jione_login`, `auth.jione_logout`).

Nút **Đăng xuất** trong smsie chỉ kết thúc phiên smsie trên trình duyệt đó; phiên Jione vẫn còn (giống 1Web).

## Kiểm tra

- Tự động: `go test -tags nouac ./internal/jione/... ./internal/api/` chạy trọn luồng với một IdP Jione giả
  (`internal/jione/jionetest`) — không cần mạng.
- Nghiệm thu với Jione thật: mở smsie khi chưa đăng nhập → *Đăng nhập với Jione* → đăng nhập tài khoản đã xác
  minh email → về bảng điều khiển; thử một tài khoản chưa được cấp quyền (phải bị từ chối); thử bấm huỷ ở Jione.
