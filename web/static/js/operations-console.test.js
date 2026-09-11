const test = require('node:test');
const assert = require('node:assert/strict');

const { buildOpsCsv, groupOpsMessages, summarizeOpsData } = require('./operations-console.js');

test('groupOpsMessages groups by phone and sorts each thread oldest first', () => {
    const grouped = groupOpsMessages([
        { id: 3, phone: '+842', timestamp: '2026-09-11T10:00:00Z', content: 'other' },
        { id: 2, phone: '+841', timestamp: '2026-09-11T11:00:00Z', content: 'new' },
        { id: 1, phone: '+841', timestamp: '2026-09-11T09:00:00Z', content: 'old' }
    ]);

    assert.equal(grouped.length, 2);
    assert.equal(grouped[0].phone, '+841');
    assert.deepEqual(grouped[0].messages.map(message => message.content), ['old', 'new']);
});

test('summarizeOpsData reports modem and message health', () => {
    const summary = summarizeOpsData(
        [{ status: 'online', signal_strength: 82 }, { status: 'offline', signal_strength: 0 }],
        [{ type: 'received', is_read: false }, { type: 'sent', status: 'delivered' }]
    );

    assert.deepEqual(summary, {
        modemTotal: 2,
        modemOnline: 1,
        weakSignal: 1,
        messageTotal: 2,
        received: 1,
        sent: 1,
        delivered: 1,
        failed: 0,
        unread: 1
    });
});

test('buildOpsCsv quotes commas and double quotes', () => {
    const csv = buildOpsCsv([{ timestamp: '2026-09-11T10:00:00Z', type: 'sent', phone: '+841', iccid: 'sim-1', status: 'sent', content: 'xin "chao", ban' }]);
    assert.match(csv, /"xin ""chao"", ban"/);
});
