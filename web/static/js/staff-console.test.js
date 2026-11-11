const test = require('node:test');
const assert = require('node:assert/strict');

const { simLabel, buildSimRows, filterSimRows, summarizeStaff, holdDuration, describeAssignment, assignmentCsv, describeStaffError, describeImportItem, summarizeImport } = require('./staff-console.js');

test('simLabel: khe + số, thiếu thì đuôi ICCID', () => {
    assert.equal(simLabel({ slot_number: 3, phone_number: '0900000001', iccid: '8984' }), 'Khe 3 · 0900000001');
    assert.equal(simLabel({ phone_number: '0900000001' }), '0900000001');
    assert.equal(simLabel({ slot_number: 0 }), 'Khe 0');
    assert.equal(simLabel({ iccid: '89840480001234567890' }), 'SIM …567890');
    assert.equal(simLabel({ iccid: 'SIM-A' }), 'SIM SIM-A');
    assert.equal(simLabel(null), 'SIM chưa rõ');
});

const modems = [
    { iccid: 'C', phone_number: '0900000003' },
    { iccid: 'A', phone_number: '0900000001' },
    { iccid: 'B', phone_number: '' }
];
const holders = [{ iccid: 'A', staff_id: 7, staff_name: 'Lan', department: 'Kinh doanh', assigned_at: '2026-11-01T09:00:00', note: 'OTP' }];

test('buildSimRows: gắn người giữ và khe, xếp theo khe rồi SIM chưa có khe', () => {
    const rows = buildSimRows(modems, holders, { A: 12, C: 2 });
    assert.deepEqual(rows.map(r => r.iccid), ['C', 'A', 'B']);
    assert.deepEqual(rows[1], { iccid: 'A', slot_number: 12, phone_number: '0900000001', staff_id: 7, staff_name: 'Lan', department: 'Kinh doanh', assigned_at: '2026-11-01T09:00:00', note: 'OTP' });
    assert.equal(rows[0].staff_id, null);
    assert.equal(rows[2].slot_number, null);
    assert.deepEqual(buildSimRows(null, null, null), []);
});

test('filterSimRows: lọc đã giao/chưa giao và tìm theo số, ICCID, tên, khe', () => {
    const rows = buildSimRows(modems, holders, { A: 12, C: 2 });
    assert.deepEqual(filterSimRows(rows, 'assigned', '').map(r => r.iccid), ['A']);
    assert.deepEqual(filterSimRows(rows, 'unassigned', '').map(r => r.iccid), ['C', 'B']);
    assert.deepEqual(filterSimRows(rows, 'all', ' lan ').map(r => r.iccid), ['A']);
    assert.deepEqual(filterSimRows(rows, 'all', '0003').map(r => r.iccid), ['C']);
    assert.deepEqual(filterSimRows(rows, 'all', 'khe 12').map(r => r.iccid), ['A']);
    assert.deepEqual(filterSimRows(rows, 'unassigned', 'lan'), []);
});

test('summarizeStaff: đếm người đang làm, SIM đã/chưa giao, và người đã nghỉ còn đứng tên SIM', () => {
    const staff = [
        { id: 1, active: true, sims: [{ iccid: 'A' }] },
        { id: 2, active: false, left_at: '2026-10-01T00:00:00', sims: [] },
        { id: 3, active: false, left_at: '2026-10-02T00:00:00', sims: [{ iccid: 'X' }] }
    ];
    assert.deepEqual(summarizeStaff(staff, buildSimRows(modems, holders, {})), { active: 1, left: 2, totalSims: 3, assigned: 1, unassigned: 2, leftHolding: 1 });
    assert.deepEqual(summarizeStaff([], []), { active: 0, left: 0, totalSims: 0, assigned: 0, unassigned: 0, leftHolding: 0 });
});

test('holdDuration: trong ngày, ngày, tháng, năm; mốc sai → —', () => {
    assert.equal(holdDuration('2026-11-11T08:00:00', '2026-11-11T17:00:00'), 'trong ngày');
    assert.equal(holdDuration('2026-11-01T08:00:00', '2026-11-11T08:00:00'), '10 ngày');
    assert.equal(holdDuration('2026-08-01T08:00:00', '2026-11-11T08:00:00'), '3 tháng');
    assert.equal(holdDuration('2023-11-01T08:00:00', '2026-11-11T08:00:00'), '3 năm');
    assert.equal(holdDuration('2026-11-11T08:00:00', '2026-11-01T08:00:00'), '—');
    assert.equal(holdDuration('rác', '2026-11-01T08:00:00'), '—');
});

test('describeAssignment: đang giữ vs đã trả, ghi chú nối lúc giao → lúc trả', () => {
    const now = new Date('2026-11-11T09:00:00').getTime();
    const open = describeAssignment({ assigned_at: '2026-11-01T09:00:00', note: 'OTP ngân hàng' }, now);
    assert.deepEqual(open, { open: true, status: 'Đang giữ', tone: 'ok', duration: '10 ngày', note: 'OTP ngân hàng' });
    const closed = describeAssignment({ assigned_at: '2026-09-01T09:00:00', released_at: '2026-09-04T09:00:00', note: 'OTP', release_note: 'Chuyển cho Minh' }, now);
    assert.deepEqual(closed, { open: false, status: 'Đã trả', tone: 'off', duration: '3 ngày', note: 'OTP → Chuyển cho Minh' });
    assert.equal(describeAssignment({ assigned_at: '2026-09-01T09:00:00', released_at: '2026-09-04T09:00:00' }, now).note, '');
});

test('assignmentCsv: có tiêu đề, thoát dấu nháy, null → rỗng', () => {
    const csv = assignmentCsv([{ assigned_at: 't1', released_at: null, iccid: 'A', phone_number: '09', staff_name: 'Lan "KD"', assigned_by: 'sep', note: 'a,b' }]);
    const lines = csv.split('\r\n');
    assert.equal(lines[0], '"assigned_at","released_at","iccid","phone_number","staff_name","assigned_by","released_by","note","release_note"');
    assert.equal(lines[1], '"t1","","A","09","Lan ""KD""","sep","","a,b",""');
    assert.equal(assignmentCsv(null).split('\r\n').length, 1);
});

test('describeStaffError: mã lỗi → câu tiếng Việt chỉ rõ việc cần làm', () => {
    assert.match(describeStaffError({ code: 'staff_holds_sims', error: 'x' }), /bàn giao hoặc thu hồi/);
    assert.match(describeStaffError({ code: 'staff_has_history' }), /nghỉ việc/);
    assert.equal(describeStaffError({ error: 'name is required' }), 'name is required');
    assert.equal(describeStaffError(undefined, 'Không lưu được.'), 'Không lưu được.');
    assert.equal(describeStaffError({ code: 'ma_la' }), 'Không thực hiện được. Thử lại.');
});

test('describeImportItem: nhãn theo việc, nghỉ việc còn giữ SIM phải nhắc bàn giao', () => {
    assert.deepEqual(describeImportItem({ action: 'create', name: 'An' }), { label: 'Thêm mới', tone: 'ok', detail: '', attention: false });
    assert.deepEqual(describeImportItem({ action: 'update', changes: ['Phòng ban: Kho → Kế toán', 'Email: a@x.vn'] }),
        { label: 'Cập nhật', tone: 'ok', detail: 'Phòng ban: Kho → Kế toán · Email: a@x.vn', attention: false });
    assert.deepEqual(describeImportItem({ action: 'leave', holding: 2, changes: ['Họ tên: Lan → Nguyễn Thị Lan'] }),
        { label: 'Nghỉ việc', tone: 'warn', detail: 'Họ tên: Lan → Nguyễn Thị Lan · Còn giữ 2 SIM — cần bàn giao sau khi nhập', attention: true });
    assert.equal(describeImportItem({ action: 'leave', holding: 0 }).attention, false);
    assert.deepEqual(describeImportItem({ action: 'skip', reason: 'Trùng với dòng 5' }), { label: 'Bỏ qua', tone: 'off', detail: 'Trùng với dòng 5', attention: false });
    assert.equal(describeImportItem({ action: 'la' }).label, 'la');
});

test('summarizeImport: xem trước nói "Sẽ nhập", áp dụng xong nói "Đã nhập", không có gì thì nói rõ', () => {
    assert.deepEqual(summarizeImport({ applied: false, created: 2, updated: 1, left: 1, unchanged: 5, skipped: 1 }),
        { text: 'Sẽ nhập: 2 thêm mới · 1 cập nhật · 1 đánh dấu nghỉ việc · 5 không đổi · 1 bỏ qua.', todo: 4 });
    assert.deepEqual(summarizeImport({ applied: true, created: 2, returned: 1 }), { text: 'Đã nhập: 2 thêm mới · 1 làm lại.', todo: 3 });
    assert.deepEqual(summarizeImport({ applied: false, unchanged: 7 }), { text: 'Không có gì thay đổi: 7 không đổi.', todo: 0 });
    assert.deepEqual(summarizeImport({}), { text: 'File không có nhân sự nào.', todo: 0 });
});
