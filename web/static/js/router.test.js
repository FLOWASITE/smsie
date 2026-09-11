const test = require('node:test');
const assert = require('node:assert/strict');

const { buildAppRoute, parseAppRoute } = require('./router.js');

test('each application tab has a stable hash route', () => {
    assert.deepEqual(parseAppRoute('#/maintenance'), { view: 'maintenance', iccid: '' });
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
