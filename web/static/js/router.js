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

if (typeof window !== 'undefined') window.smsieRouter = { buildAppRoute, parseAppRoute };
if (typeof module !== 'undefined') module.exports = { buildAppRoute, parseAppRoute };
