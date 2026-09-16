const test = require('node:test');
const assert = require('node:assert/strict');

const { balanceNeedsRefresh, buildOpsCsv, describeOpsMessageRoute, groupOpsMessages, summarizeOpsData, buildTrayCells, groupSlotEventsByDay, describeSlotEvent, bayBalanceLabel, describeBalanceLevel, balanceSparkline, describeHealthFinding, describeKeepalive, keepaliveNextRun, describePhoneLookup, describeAudit, auditCsv, formatReportRow, localMonth } = require('./operations-console.js');

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

test('describeHealthFinding: no_sms/unregistered danger, absent warning, label = detail', () => {
    assert.deepEqual(describeHealthFinding({ kind: 'no_sms', detail: 'không nhận SMS nào 31 ngày — có thể bị thu hồi' }), { tone: 'danger', label: 'không nhận SMS nào 31 ngày — có thể bị thu hồi' });
    assert.deepEqual(describeHealthFinding({ kind: 'unregistered', detail: 'không đăng ký mạng 25 giờ dù modem online' }), { tone: 'danger', label: 'không đăng ký mạng 25 giờ dù modem online' });
    assert.deepEqual(describeHealthFinding({ kind: 'absent', detail: 'đã rút khỏi khay 8 ngày' }), { tone: 'warning', label: 'đã rút khỏi khay 8 ngày' });
    assert.deepEqual(describeHealthFinding(undefined), { tone: 'danger', label: '' });
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

test('describeKeepalive: tắt → muted; failed/skipped ≤7 ngày → danger/warning kèm lý do; bật → ngày kế tiếp', () => {
    const now = new Date('2026-09-16T10:00:00').getTime();
    assert.deepEqual(describeKeepalive({ enabled: false, next_due_at: '2026-10-01T00:00:00Z' }, now), { tone: 'muted', label: 'Tắt' });
    assert.deepEqual(describeKeepalive({ enabled: true, last_run: { status: 'failed', reason: 'modem offline', ran_at: '2026-09-15T07:00:00Z' } }, now), { tone: 'danger', label: 'Nuôi SIM failed: modem offline' });
    assert.deepEqual(describeKeepalive({ enabled: true, last_run: { status: 'skipped', reason: 'không có SIM đích', ran_at: '2026-09-12T07:00:00Z' } }, now), { tone: 'warning', label: 'Nuôi SIM skipped: không có SIM đích' });
    // failed nhưng đã quá 7 ngày → không cảnh báo nữa, hiện lần kế tiếp
    assert.deepEqual(describeKeepalive({ enabled: true, next_due_at: '2026-10-05T07:00:00', last_run: { status: 'failed', reason: 'modem offline', ran_at: '2026-09-01T07:00:00Z' } }, now), { tone: 'ok', label: 'Lần kế tiếp 05/10' });
});

test('keepaliveNextRun: run_hour hôm nay nếu chưa qua, ngày mai nếu đã qua', () => {
    assert.equal(keepaliveNextRun(7, new Date('2026-09-16T05:30:00').getTime()).getTime(), new Date('2026-09-16T07:00:00').getTime());
    assert.equal(keepaliveNextRun(7, new Date('2026-09-16T07:00:00').getTime()).getTime(), new Date('2026-09-17T07:00:00').getTime());
});

test('describePhoneLookup: chưa bấm → Đọc số; đang đọc → busy; xong → số; hết giờ/lỗi → cảnh báo', () => {
    assert.deepEqual(describePhoneLookup(undefined), { label: 'Đọc số', tone: 'muted', busy: false });
    assert.deepEqual(describePhoneLookup({ state: 'checking' }), { label: 'Đang đọc số…', tone: 'muted', busy: true });
    assert.deepEqual(describePhoneLookup({ state: 'done', phone: '0912345678' }), { label: 'Đã đọc: 0912345678', tone: 'ok', busy: false });
    assert.equal(describePhoneLookup({ state: 'timeout' }).tone, 'warning');
    assert.deepEqual(describePhoneLookup({ state: 'error', message: 'modem offline' }), { label: 'modem offline', tone: 'danger', busy: false });
    assert.equal(describePhoneLookup({ state: 'error' }).label, 'Không gửi được yêu cầu đọc số');
});

test('describeAudit: nhãn Việt cho mã hành động, mã lạ trả nguyên', () => {
    assert.equal(describeAudit('sms.send'), 'Gửi SMS');
    assert.equal(describeAudit('keepalive.send'), 'Nuôi SIM (tự động)');
    assert.equal(describeAudit('auth.password'), 'Đổi mật khẩu');
    assert.equal(describeAudit('POST /api/v1/modems/:iccid/scan'), 'POST /api/v1/modems/:iccid/scan');
    assert.equal(describeAudit(undefined), '—');
});

test('auditCsv: tiêu đề + nhãn Việt + escape dấu nháy', () => {
    const csv = auditCsv([{ at: '2026-09-16T10:00:00Z', username: 'alice', action: 'sms.send', iccid: '89', target: '0912', status: 200, ip: '::1', detail: '{"message":"a \"b\""}' }]);
    const lines = csv.split('\r\n');
    assert.equal(lines[0], '"at","username","action","label","iccid","target","status","ip","detail"');
    assert.equal(lines[1], '"2026-09-16T10:00:00Z","alice","sms.send","Gửi SMS","89","0912","200","::1","{""message"":""a ""b""""}"');
});

test('formatReportRow: số có dấu chấm nghìn, null → —, delta có dấu', () => {
    const f = formatReportRow({ iccid: '89', phone_number: '0911', slot_number: 3, sms_received: 1200, sms_sent: 5, sms_failed: 0, calls: 2, call_seconds: 75, balance_start: 50000, balance_end: 35000, balance_delta: -15000, slot_events: 1, keepalive_sent: 1, alerts: 2 });
    assert.equal(f.slot, '#3');
    assert.equal(f.smsReceived, '1.200');
    assert.equal(f.callMinutes, '1,3');
    assert.equal(f.balanceDelta, '-15.000');
    assert.equal(f.deltaTone, 'text-danger');
    const empty = formatReportRow({ iccid: 'x', slot_number: null, balance_start: null, balance_end: null, balance_delta: null });
    assert.equal(empty.slot, '—');
    assert.equal(empty.phone, '—');
    assert.equal(empty.balanceStart, '—');
    assert.equal(empty.balanceDelta, '—');
    assert.equal(empty.smsReceived, '0');
    assert.equal(formatReportRow({ balance_delta: 500 }).balanceDelta, '+500');
});

test('localMonth: theo giờ máy, đệm 0', () => {
    assert.equal(localMonth(new Date(2026, 0, 1, 0, 30)), '2026-01');
    assert.equal(localMonth(new Date(2026, 8, 16)), '2026-09');
});
