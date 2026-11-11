const test = require('node:test');
const assert = require('node:assert/strict');

const { buildAppRoute, parseAppRoute, parseSSOResult } = require('./router.js');

test('each application tab has a stable hash route', () => {
    assert.deepEqual(parseAppRoute('#/maintenance'), { view: 'maintenance', iccid: '' });
    assert.deepEqual(parseAppRoute('#/calls/sim-1'), { view: 'calls', iccid: 'sim-1' });
    assert.equal(buildAppRoute('reports'), '#/reports');
});

test('SIM selection is encoded in and restored from the route', () => {
    const iccid = '89840509241455299254';
    assert.equal(buildAppRoute('sms', iccid), `#/sms/${iccid}`);
    assert.deepEqual(parseAppRoute(`#/sms/${iccid}`), { view: 'sms', iccid });
});

test('unknown or malformed routes fall back to overview', () => {
    assert.deepEqual(parseAppRoute('#/not-a-view/%E0%A4%A'), { view: 'overview', iccid: '' });
    assert.deepEqual(parseAppRoute(''), { view: 'overview', iccid: '' });
});

test('parseSSOResult: ?sso=1 chờ đổi phiên, gỡ tham số khỏi URL', () => {
    assert.deepEqual(parseSSOResult('?sso=1'), { pending: true, errorKey: '', search: '' });
    assert.deepEqual(parseSSOResult('?sso=1&x=2'), { pending: true, errorKey: '', search: '?x=2' });
    assert.deepEqual(parseSSOResult(''), { pending: false, errorKey: '', search: '' });
    assert.deepEqual(parseSSOResult('?x=2'), { pending: false, errorKey: '', search: '?x=2' });
});

test('parseSSOResult: mã lỗi → khoá i18n, mã lạ về thông báo chung, lỗi thắng sso=1', () => {
    assert.equal(parseSSOResult('?sso_error=not_allowed').errorKey, 'sso_err_not_allowed');
    assert.equal(parseSSOResult('?sso_error=email_unverified').errorKey, 'sso_err_email_unverified');
    assert.equal(parseSSOResult('?sso_error=%3Cscript%3E').errorKey, 'sso_err_failed');
    assert.deepEqual(parseSSOResult('?sso=1&sso_error=denied'), { pending: false, errorKey: 'sso_err_denied', search: '' });
});
