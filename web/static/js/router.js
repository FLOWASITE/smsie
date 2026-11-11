const APP_VIEWS = new Set([
    'overview', 'slots', 'sms', 'calls', 'maintenance', 'alerts', 'reports', 'audit',
    'modems', 'apikeys', 'users'
]);

function parseAppRoute(hash) {
    const parts = String(hash || '').replace(/^#\/?/, '').split('/').filter(Boolean);
    const view = APP_VIEWS.has(parts[0]) ? parts[0] : 'overview';
    if (view === 'overview' && parts[0] !== 'overview') return { view, iccid: '' };
    try {
        return { view, iccid: parts[1] ? decodeURIComponent(parts[1]) : '' };
    } catch (_) {
        return { view: 'overview', iccid: '' };
    }
}

function buildAppRoute(view, iccid) {
    const safeView = APP_VIEWS.has(view) ? view : 'overview';
    return `#/${safeView}${iccid ? `/${encodeURIComponent(iccid)}` : ''}`;
}

// Mã lỗi /oidc/callback trả về qua ?sso_error=… → khoá i18n. Mã lạ rơi về thông báo chung.
const SSO_ERROR_KEYS = {
    disabled: 'sso_err_disabled',
    unavailable: 'sso_err_unavailable',
    session: 'sso_err_session',
    denied: 'sso_err_denied',
    not_allowed: 'sso_err_not_allowed',
    email_unverified: 'sso_err_email_unverified',
    token: 'sso_err_failed',
    failed: 'sso_err_failed'
};

// Đọc kết quả đăng nhập Jione từ query (?sso=1 hoặc ?sso_error=mã) và trả query đã gỡ hai tham số đó,
// để URL trên thanh địa chỉ sạch lại và F5 không lặp lại việc đổi phiên.
function parseSSOResult(search) {
    const params = new URLSearchParams(String(search || '').replace(/^\?/, ''));
    const pending = params.get('sso') === '1';
    const code = params.get('sso_error');
    params.delete('sso');
    params.delete('sso_error');
    const rest = params.toString();
    return {
        pending: pending && !code,
        errorKey: code ? (SSO_ERROR_KEYS[code] || 'sso_err_failed') : '',
        search: rest ? `?${rest}` : ''
    };
}

if (typeof window !== 'undefined') window.smsieRouter = { buildAppRoute, parseAppRoute, parseSSOResult };
if (typeof module !== 'undefined') module.exports = { buildAppRoute, parseAppRoute, parseSSOResult };
