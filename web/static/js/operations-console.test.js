const test = require('node:test');
const assert = require('node:assert/strict');

const { balanceNeedsRefresh, buildOpsCsv, describeOpsMessageRoute, groupOpsMessages, summarizeOpsData, buildTrayCells, groupSlotEventsByDay, describeSlotEvent, bayBalanceLabel, describeBalanceLevel, balanceSparkline } = require('./operations-console.js');

test('balance refresh is automatic only when missing or older than one day', () => {
    assert.equal(balanceNeedsRefresh({}), true);
    assert.equal(balanceNeedsRefresh({ balance_updated_at: new Date().toISOString() }), false);
    assert.equal(balanceNeedsRefresh({ balance_updated_at: new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString() }), true);
});

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

test('describeOpsMessageRoute identifies the physical SIM used', () => {
    const modems = [{ iccid: 'sim-1', port_name: 'COM19' }];
    const mappings = { 'sim-1': 16 };
    const phones = { 'sim-1': '0924875662' };

    assert.equal(
        describeOpsMessageRoute({ type: 'sent', iccid: 'sim-1' }, modems, mappings, phones),
        'Gửi từ Khe 16 · 0924875662 · COM19'
    );
    assert.equal(
        describeOpsMessageRoute({ type: 'received', iccid: 'sim-1' }, modems, mappings, phones),
        'Nhận tại Khe 16 · 0924875662 · COM19'
    );
});

test('buildTrayCells renders exactly 32 cells, unassigned bays separately', () => {
    const now = Date.now();
    const bays = [
        { imei: 'A', slot_number: 16, current_iccid: 'I1', status: 'online', signal_strength: 71, last_event_at: new Date(now - 3600e3).toISOString() },
        { imei: 'B', slot_number: 3, current_iccid: '', status: 'empty' },
        { imei: 'C', slot_number: 9, current_iccid: 'I3', status: 'online', signal_strength: 12 },
        { imei: 'Z', slot_number: null, current_iccid: 'I9', status: 'online' }
    ];
    const { cells, unassigned } = buildTrayCells(bays, now);
    assert.equal(cells.length, 32);
    assert.equal(cells[15].tone, 'online');
    assert.equal(cells[15].swapped, true);
    assert.equal(cells[2].tone, 'empty');
    assert.equal(cells[8].tone, 'weak');
    assert.equal(cells[0].tone, 'missing');
    assert.deepEqual(unassigned.map(b => b.imei), ['Z']);
});

test('groupSlotEventsByDay keeps newest day first', () => {
    const grouped = groupSlotEventsByDay([
        { id: 1, detected_at: '2026-09-15T14:22:08+07:00', event: 'moved' },
        { id: 2, detected_at: '2026-07-21T16:05:33+07:00', event: 'inserted' },
        { id: 3, detected_at: '2026-09-15T14:21:40+07:00', event: 'removed' }
    ]);
    assert.equal(grouped.length, 2);
    assert.equal(grouped[0].day, '2026-09-15');
    assert.deepEqual(grouped[0].events.map(e => e.id), [1, 3]);
});

test('describeSlotEvent renders path and balance label', () => {
    assert.deepEqual(describeSlotEvent({ event: 'moved', from_slot: 15, to_slot: 16, balance_vnd: 48500 }),
        { path: 'Khe 15 → Khe 16', tone: 'moved', balance: '48.500 đ', balanceNote: 'số dư lúc vào khe' });
    assert.deepEqual(describeSlotEvent({ event: 'removed', from_slot: 15 }),
        { path: 'Khe 15 → rút ra', tone: 'removed', balance: '—', balanceNote: 'không đọc số dư' });
    assert.equal(describeSlotEvent({ event: 'inserted', to_slot: null }).path, 'Chưa gán khe');
});

test('bayBalanceLabel shows Chưa kiểm tra until USSD has run', () => {
    assert.equal(bayBalanceLabel({ balance_vnd: 0 }), 'Chưa kiểm tra');
    assert.equal(bayBalanceLabel({ balance_vnd: 48500, balance_updated_at: '2026-09-16T00:00:00Z' }), '48.500 đ');
});

test('describeBalanceLevel maps level to tone and label', () => {
    assert.deepEqual(describeBalanceLevel({ level: 'low', balance_vnd: 12000, threshold_vnd: 20000 }),
        { tone: 'danger', label: 'Số dư 12.000 đ dưới ngưỡng 20.000 đ' });
    assert.deepEqual(describeBalanceLevel({ level: 'forecast', balance_vnd: 90000, threshold_vnd: 20000, days_left: 3.4 }),
        { tone: 'warning', label: 'Dự kiến hết tiền sau ≈3 ngày' });
    assert.deepEqual(describeBalanceLevel({ level: 'ok', balance_vnd: 150000, days_left: 12.6 }), { tone: 'ok', label: 'Còn ≈13 ngày' });
    assert.deepEqual(describeBalanceLevel({ level: 'ok', balance_vnd: 150000 }), { tone: 'ok', label: 'Số dư ổn' });
    assert.deepEqual(describeBalanceLevel({ level: 'unknown' }), { tone: 'muted', label: 'Chưa đọc số dư' });
    assert.deepEqual(describeBalanceLevel(undefined), { tone: 'muted', label: 'Chưa đọc số dư' });
});

test('balanceSparkline joins up to 7 snapshots in thousands', () => {
    assert.equal(balanceSparkline([{ balance_vnd: 52400 }, { balance_vnd: 48000 }, { balance_vnd: 41200 }]), '52k → 48k → 41k');
    assert.equal(balanceSparkline([]), '');
    assert.equal(balanceSparkline(undefined), '');
    assert.equal(balanceSparkline(Array.from({ length: 9 }, (_, i) => ({ balance_vnd: (i + 1) * 1000 }))), '3k → 4k → 5k → 6k → 7k → 8k → 9k');
});
