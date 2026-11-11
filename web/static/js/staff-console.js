// Nhân sự và SIM: ai đang giữ SIM nào, giao / đổi người giữ / thu hồi, bàn giao, lịch sử giao nhận.
// Phần đầu là hàm thuần (có test node); phần sau gắn DOM, dùng chung opsState / opsElement của operations-console.js.

const STAFF_DAY_MS = 24 * 60 * 60 * 1000;

// simLabel: tên ngắn gọn của một SIM để người đọc nhận ra — ưu tiên khe + số thuê bao, thiếu thì đuôi ICCID.
function simLabel(sim) {
    const s = sim || {};
    const parts = [];
    if (s.slot_number !== null && s.slot_number !== undefined && s.slot_number !== '') parts.push(`Khe ${s.slot_number}`);
    if (s.phone_number) parts.push(s.phone_number);
    if (!parts.length) {
        const iccid = String(s.iccid || '');
        parts.push(iccid.length > 8 ? `SIM …${iccid.slice(-6)}` : (iccid ? `SIM ${iccid}` : 'SIM chưa rõ'));
    }
    return parts.join(' · ');
}

// buildSimRows: gộp danh sách SIM (modems) với khe và người giữ hiện tại thành dòng hiển thị, xếp theo khe rồi số.
function buildSimRows(modems, holders, slotByICCID) {
    const holderOf = {};
    (holders || []).forEach(h => { holderOf[h.iccid] = h; });
    const slots = slotByICCID || {};
    const rows = (modems || []).map(m => {
        const h = holderOf[m.iccid] || null;
        const slot = slots[m.iccid] !== undefined ? slots[m.iccid] : (m.slot_number !== undefined ? m.slot_number : null);
        return {
            iccid: m.iccid,
            slot_number: slot === undefined ? null : slot,
            phone_number: m.phone_number || '',
            staff_id: h ? h.staff_id : null,
            staff_name: h ? h.staff_name : '',
            department: h ? (h.department || '') : '',
            assigned_at: h ? h.assigned_at : null,
            note: h ? (h.note || '') : ''
        };
    });
    const rank = v => (v === null || v === undefined ? Number.MAX_SAFE_INTEGER : Number(v));
    rows.sort((a, b) => rank(a.slot_number) - rank(b.slot_number) || String(a.phone_number).localeCompare(String(b.phone_number)) || String(a.iccid).localeCompare(String(b.iccid)));
    return rows;
}

// filterSimRows: lọc theo trạng thái giao (all | assigned | unassigned) và ô tìm kiếm (số, ICCID, tên người giữ).
function filterSimRows(rows, state, query) {
    const q = String(query || '').trim().toLowerCase();
    return (rows || []).filter(r => {
        if (state === 'assigned' && !r.staff_id) return false;
        if (state === 'unassigned' && r.staff_id) return false;
        if (!q) return true;
        return [r.phone_number, r.iccid, r.staff_name, r.department, r.slot_number === null ? '' : `khe ${r.slot_number}`]
            .some(v => String(v || '').toLowerCase().includes(q));
    });
}

// summarizeStaff: số liệu đầu trang.
function summarizeStaff(staff, simRows) {
    const list = staff || [];
    const rows = simRows || [];
    const active = list.filter(s => s.active !== false && !s.left_at).length;
    const assigned = rows.filter(r => r.staff_id).length;
    // Người đã nghỉ mà vẫn đứng tên SIM là việc cần xử lý ngay.
    const leftHolding = list.filter(s => (s.active === false || s.left_at) && (s.sims || []).length > 0).length;
    return { active, left: list.length - active, totalSims: rows.length, assigned, unassigned: rows.length - assigned, leftHolding };
}

// holdDuration: "3 ngày", "2 tháng", "hôm nay" — thời gian giữ SIM tính từ lúc giao tới lúc thu hồi (hoặc bây giờ).
function holdDuration(from, to) {
    const start = new Date(from).getTime();
    const end = to ? new Date(to).getTime() : Date.now();
    if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return '—';
    const days = Math.floor((end - start) / STAFF_DAY_MS);
    if (days < 1) return 'trong ngày';
    if (days < 60) return `${days} ngày`;
    const months = Math.floor(days / 30);
    if (months < 24) return `${months} tháng`;
    return `${Math.floor(days / 365)} năm`;
}

// describeAssignment: một dòng lịch sử giao nhận → nhãn hiển thị.
function describeAssignment(row, now) {
    const r = row || {};
    const open = !r.released_at;
    return {
        open,
        status: open ? 'Đang giữ' : 'Đã trả',
        tone: open ? 'ok' : 'off',
        duration: holdDuration(r.assigned_at, open ? now : r.released_at),
        // Ghi chú lúc trả ("Chuyển cho Minh", "Nghỉ việc") nói rõ SIM đi đâu; không có thì dùng ghi chú lúc giao.
        note: [r.note, r.release_note].filter(Boolean).join(' → ')
    };
}

function assignmentCsv(rows) {
    const quote = value => `"${String(value === undefined || value === null ? '' : value).replaceAll('"', '""')}"`;
    const out = [['assigned_at', 'released_at', 'iccid', 'phone_number', 'staff_name', 'assigned_by', 'released_by', 'note', 'release_note']];
    (rows || []).forEach(r => out.push([r.assigned_at, r.released_at || '', r.iccid, r.phone_number || '', r.staff_name || '', r.assigned_by || '', r.released_by || '', r.note || '', r.release_note || '']));
    return out.map(r => r.map(quote).join(',')).join('\r\n');
}

const STAFF_ERRORS = {
    staff_not_found: 'Không tìm thấy nhân sự này (có thể vừa bị xoá). Tải lại trang.',
    sim_not_found: 'Không tìm thấy SIM này.',
    staff_left: 'Nhân sự này đã nghỉ việc. Bấm "Làm lại" trước khi giao SIM.',
    sim_not_assigned: 'SIM này hiện chưa giao cho ai.',
    staff_holds_sims: 'Nhân sự này còn đang giữ SIM. Hãy bàn giao hoặc thu hồi trước.',
    staff_has_history: 'Nhân sự này đã có lịch sử giao SIM nên không xoá được. Hãy đánh dấu nghỉ việc.',
    same_staff: 'Người giao và người nhận phải khác nhau.'
};

// describeStaffError: phản hồi lỗi của API → câu tiếng Việt nói rõ phải làm gì.
function describeStaffError(response, fallback) {
    const r = response || {};
    if (r.code && STAFF_ERRORS[r.code]) return STAFF_ERRORS[r.code];
    return r.error || fallback || 'Không thực hiện được. Thử lại.';
}

if (typeof module !== 'undefined') {
    module.exports = { simLabel, buildSimRows, filterSimRows, summarizeStaff, holdDuration, describeAssignment, assignmentCsv, describeStaffError };
}

// ---------------------------------------------------------------------------------------------------------------
// DOM
// ---------------------------------------------------------------------------------------------------------------
if (typeof window !== 'undefined' && window.jQuery) (function () {
    const state = { staff: [], holders: [], history: [], historyTotal: 0, historyPage: 1, tab: 'people', simFilter: 'all', simQuery: '', showLeft: false };
    const HISTORY_PAGE = 50;

    const el = (tag, cls, text) => opsElement(tag, cls || '', text);
    const errText = (xhr, fallback) => describeStaffError(xhr && xhr.responseJSON, fallback);
    const slotMap = () => {
        const map = {};
        (opsState.bays || []).forEach(b => { if (b.current_iccid && b.slot_number != null) map[b.current_iccid] = b.slot_number; });
        return map;
    };
    const simRows = () => buildSimRows(opsState.modems, state.holders, slotMap());
    const activeStaff = () => state.staff.filter(s => s.active);
    const api = (method, url, body) => $.ajax({ url, method, contentType: 'application/json', data: body === undefined ? undefined : JSON.stringify(body) });

    function showTab(tab) {
        state.tab = tab;
        $('[data-staff-tab]').each(function () { $(this).toggleClass('is-active', $(this).data('staff-tab') === tab); });
        ['people', 'sims', 'history'].forEach(name => $(`#staff-tab-${name}`).toggleClass('d-none', name !== tab));
        if (tab === 'history') loadHistory();
    }

    function load() {
        return $.when($.get('/api/v1/staff'), $.get('/api/v1/sim-holders'), $.get('/api/v1/modems'), $.get('/api/v1/bays'))
            .done(function (staffRes, holderRes, modemRes, bayRes) {
                state.staff = staffRes[0] || [];
                state.holders = holderRes[0] || [];
                opsState.modems = modemRes[0] || [];
                opsState.bays = bayRes[0] || [];
                opsState.holders = state.holders;
                render();
            })
            .fail(function () {
                $('#staff-people-body').empty().append($('<tr>').append($('<td>').attr('colspan', 5).append(el('div', 'ops-empty', 'Không tải được danh sách nhân sự. Thử tải lại trang.'))));
            });
    }

    function render() {
        renderSummary();
        renderPeople();
        renderSims();
        fillHistoryFilters();
    }

    function renderSummary() {
        const sum = summarizeStaff(state.staff, simRows());
        const cards = [
            { label: 'Nhân sự đang làm', value: sum.active, note: sum.left ? `${sum.left} người đã nghỉ` : 'Chưa ai nghỉ', icon: 'bi-person-badge', tone: 'mint' },
            { label: 'SIM đã giao', value: `${sum.assigned}/${sum.totalSims}`, note: 'SIM có người chịu trách nhiệm', icon: 'bi-sim', tone: 'blue' },
            { label: 'SIM chưa giao', value: sum.unassigned, note: sum.unassigned ? 'Chưa ai đứng tên' : 'Mọi SIM đều có người giữ', icon: 'bi-question-circle', tone: sum.unassigned ? 'amber' : 'green' },
            { label: 'Cần xử lý', value: sum.leftHolding, note: sum.leftHolding ? 'Người đã nghỉ còn đứng tên SIM' : 'Không có SIM đứng tên người đã nghỉ', icon: 'bi-exclamation-triangle', tone: sum.leftHolding ? 'coral' : 'green' }
        ];
        const box = $('#staff-kpis').empty();
        cards.forEach(card => {
            const item = el('article', 'ops-kpi');
            item.append(el('div', `ops-kpi-icon tone-${card.tone}`).append($('<i>').addClass(`bi ${card.icon}`)));
            const body = el('div', 'ops-kpi-body');
            body.append(el('div', 'ops-kpi-label', card.label), el('div', 'ops-kpi-value', String(card.value)), el('div', 'ops-kpi-note', card.note));
            box.append(item.append(body));
        });
    }

    function simChip(holding) {
        const chip = el('span', 'staff-sim-chip');
        chip.append(el('span', 'mono', simLabel(holding)));
        chip.attr('title', `ICCID ${holding.iccid} · giao ${opsDate(holding.assigned_at)} (${holdDuration(holding.assigned_at)})${holding.note ? ' · ' + holding.note : ''}`);
        const x = $('<button>').attr({ type: 'button', 'aria-label': `Thu hồi ${simLabel(holding)}` }).addClass('staff-chip-x').text('×');
        x.on('click', () => releaseSim(holding));
        return chip.append(x);
    }

    function renderPeople() {
        const body = $('#staff-people-body').empty();
        const list = state.staff.filter(s => state.showLeft || s.active || (s.sims || []).length > 0);
        const hiddenLeft = state.staff.length - list.length;
        $('#staff-left-toggle').toggleClass('d-none', state.staff.every(s => s.active)).text(state.showLeft ? 'Ẩn người đã nghỉ' : `Hiện ${hiddenLeft} người đã nghỉ`);
        if (!state.staff.length) {
            body.append($('<tr>').append($('<td>').attr('colspan', 5).append(el('div', 'ops-empty', 'Chưa có nhân sự nào. Bấm "Thêm nhân sự" để bắt đầu, rồi giao SIM cho từng người.'))));
            return;
        }
        list.forEach(s => {
            const row = $('<tr>').toggleClass('staff-row-left', !s.active);
            const who = $('<td>');
            who.append(el('div', 'staff-name', s.name));
            const meta = [s.code, s.active ? '' : `Nghỉ từ ${opsDate(s.left_at)}`].filter(Boolean).join(' · ');
            if (meta) who.append(el('div', 'ops-list-note', meta));
            row.append(who);
            row.append($('<td>').text(s.department || '—'));
            const contact = $('<td>');
            contact.append(el('div', 'mono', s.phone || '—'));
            if (s.email) contact.append(el('div', 'ops-list-note', s.email));
            row.append(contact);

            const sims = $('<td>');
            if ((s.sims || []).length) {
                const wrap = el('div', 'staff-sim-chips');
                s.sims.forEach(h => wrap.append(simChip(h)));
                sims.append(wrap);
                if (!s.active) sims.append(el('div', 'staff-warning', 'Đã nghỉ nhưng còn đứng tên SIM — hãy bàn giao.'));
            } else {
                sims.append(el('span', 'ops-list-note', 'Chưa giữ SIM nào'));
            }
            row.append(sims);

            const actions = el('div', 'staff-actions');
            const btn = (label, cls, fn) => $('<button>').attr('type', 'button').addClass(`btn btn-sm ${cls}`).text(label).on('click', fn);
            if (s.active) actions.append(btn('Giao SIM', 'btn-outline-dark', () => openAssign({ staffId: s.id })));
            if ((s.sims || []).length) actions.append(btn('Bàn giao', 'btn-outline-dark', () => openTransfer(s)));
            actions.append(btn('Sửa', 'btn-outline-secondary', () => openStaffForm(s)));
            actions.append(s.active ? btn('Nghỉ việc', 'btn-outline-secondary', () => markLeft(s)) : btn('Làm lại', 'btn-outline-secondary', () => markReturned(s)));
            actions.append(btn('Xoá', 'btn-outline-danger', () => removeStaff(s)));
            row.append($('<td>').append(actions));
            body.append(row);
        });
    }

    function renderSims() {
        const body = $('#staff-sims-body').empty();
        const all = simRows();
        const rows = filterSimRows(all, state.simFilter, state.simQuery);
        $('#staff-sims-count').text(`${rows.length}/${all.length} SIM`);
        if (!rows.length) {
            body.append($('<tr>').append($('<td>').attr('colspan', 5).append(el('div', 'ops-empty', all.length ? 'Không có SIM nào khớp bộ lọc.' : 'Chưa có SIM nào trong hệ thống.'))));
            return;
        }
        rows.forEach(r => {
            const row = $('<tr>');
            row.append($('<td>').addClass('mono').text(r.slot_number === null ? '—' : `#${r.slot_number}`));
            const sim = $('<td>');
            sim.append(el('div', 'mono', r.phone_number || 'Chưa biết số'));
            sim.append(el('div', 'ops-list-note mono', r.iccid));
            row.append(sim);
            const holder = $('<td>');
            if (r.staff_id) {
                holder.append(el('div', 'staff-name', r.staff_name));
                if (r.department) holder.append(el('div', 'ops-list-note', r.department));
            } else {
                holder.append(el('span', 'pill warn', 'Chưa giao'));
            }
            row.append(holder);
            row.append($('<td>').text(r.staff_id ? `${opsDate(r.assigned_at)} · ${holdDuration(r.assigned_at)}` : '—'));
            const actions = el('div', 'staff-actions');
            actions.append($('<button>').attr('type', 'button').addClass('btn btn-sm btn-outline-dark').text(r.staff_id ? 'Đổi người giữ' : 'Giao SIM').on('click', () => openAssign({ iccid: r.iccid })));
            if (r.staff_id) actions.append($('<button>').attr('type', 'button').addClass('btn btn-sm btn-outline-secondary').text('Thu hồi').on('click', () => releaseSim(r)));
            actions.append($('<button>').attr('type', 'button').addClass('btn btn-sm btn-link').text('Lịch sử').on('click', function () {
                $('#staff-history-sim').val(r.iccid);
                $('#staff-history-staff').val('');
                state.historyPage = 1;
                showTab('history');
            }));
            row.append($('<td>').append(actions));
            body.append(row);
        });
    }

    function fillHistoryFilters() {
        const staffSel = $('#staff-history-staff');
        const simSel = $('#staff-history-sim');
        const keepStaff = staffSel.val();
        const keepSim = simSel.val();
        staffSel.empty().append($('<option>').val('').text('Mọi nhân sự'));
        state.staff.forEach(s => staffSel.append($('<option>').val(s.id).text(s.active ? s.name : `${s.name} (đã nghỉ)`)));
        simSel.empty().append($('<option>').val('').text('Mọi SIM'));
        simRows().forEach(r => simSel.append($('<option>').val(r.iccid).text(simLabel(r))));
        staffSel.val(keepStaff || '');
        simSel.val(keepSim || '');
    }

    function historyParams(page, size) {
        const params = { page, page_size: size };
        if ($('#staff-history-staff').val()) params.staff_id = $('#staff-history-staff').val();
        if ($('#staff-history-sim').val()) params.iccid = $('#staff-history-sim').val();
        if ($('#staff-history-open').is(':checked')) params.open = 1;
        return params;
    }

    function loadHistory() {
        return $.get('/api/v1/sim-assignments', historyParams(state.historyPage, HISTORY_PAGE)).done(function (res) {
            state.history = res.data || [];
            state.historyTotal = res.total || 0;
            renderHistory();
        }).fail(function () {
            state.history = [];
            state.historyTotal = 0;
            renderHistory();
        });
    }

    function renderHistory() {
        const body = $('#staff-history-body').empty();
        const slots = slotMap();
        if (!state.history.length) {
            body.append($('<tr>').append($('<td>').attr('colspan', 6).append(el('div', 'ops-empty', 'Chưa có lần giao nhận nào khớp bộ lọc.'))));
        }
        state.history.forEach(r => {
            const d = describeAssignment(r);
            const row = $('<tr>');
            row.append($('<td>').text(opsDateTime(r.assigned_at)).attr('title', r.assigned_by ? `Người giao: ${r.assigned_by}` : ''));
            const sim = $('<td>');
            sim.append(el('div', 'mono', simLabel({ iccid: r.iccid, phone_number: r.phone_number, slot_number: slots[r.iccid] })));
            sim.append(el('div', 'ops-list-note mono', r.iccid));
            row.append(sim);
            row.append($('<td>').append(el('div', 'staff-name', r.staff_name || '(đã xoá)')));
            row.append($('<td>').text(d.open ? '—' : opsDateTime(r.released_at)).attr('title', r.released_by ? `Người thu hồi: ${r.released_by}` : ''));
            row.append($('<td>').append(el('span', `pill ${d.tone}`, d.status), el('div', 'ops-list-note', d.duration)));
            row.append($('<td>').text(d.note || '—'));
            body.append(row);
        });
        const pages = Math.max(1, Math.ceil(state.historyTotal / HISTORY_PAGE));
        $('#staff-history-page').text(`Trang ${state.historyPage}/${pages} · ${state.historyTotal} lần giao nhận`);
        $('#staff-history-prev').prop('disabled', state.historyPage <= 1);
        $('#staff-history-next').prop('disabled', state.historyPage >= pages);
    }

    const refresh = () => load().then(() => { if (state.tab === 'history') loadHistory(); });

    // ----- Thêm / sửa nhân sự -----
    function openStaffForm(staff) {
        const s = staff || {};
        $('#staff-form-title').text(s.id ? 'Sửa nhân sự' : 'Thêm nhân sự');
        $('#staff-form-id').val(s.id || '');
        $('#staff-form-name').val(s.name || '');
        $('#staff-form-code').val(s.code || '');
        $('#staff-form-department').val(s.department || '');
        $('#staff-form-phone').val(s.phone || '');
        $('#staff-form-email').val(s.email || '');
        $('#staff-form-note').val(s.note || '');
        $('#staff-form-error').addClass('d-none').text('');
        $('#staffFormModal').modal('show');
    }

    function saveStaffForm() {
        const id = $('#staff-form-id').val();
        const body = {
            name: $('#staff-form-name').val(), code: $('#staff-form-code').val(), department: $('#staff-form-department').val(),
            phone: $('#staff-form-phone').val(), email: $('#staff-form-email').val(), note: $('#staff-form-note').val()
        };
        if (!body.name.trim()) {
            $('#staff-form-error').removeClass('d-none').text('Cần nhập họ tên.');
            $('#staff-form-name').trigger('focus');
            return;
        }
        const button = $('#staff-form-save').prop('disabled', true);
        api(id ? 'PUT' : 'POST', id ? `/api/v1/staff/${id}` : '/api/v1/staff', body)
            .done(function () { $('#staffFormModal').modal('hide'); refresh(); })
            .fail(xhr => $('#staff-form-error').removeClass('d-none').text(errText(xhr, 'Không lưu được nhân sự.')))
            .always(() => button.prop('disabled', false));
    }

    // ----- Giao SIM / đổi người giữ -----
    function openAssign(preset) {
        const p = preset || {};
        const rows = simRows();
        const simSel = $('#assign-sim').empty();
        rows.forEach(r => simSel.append($('<option>').val(r.iccid).text(`${simLabel(r)}${r.staff_id ? ` — đang giữ: ${r.staff_name}` : ' — chưa giao'}`)));
        const staffSel = $('#assign-staff').empty();
        activeStaff().forEach(s => staffSel.append($('<option>').val(s.id).text(s.department ? `${s.name} · ${s.department}` : s.name)));
        if (p.iccid) simSel.val(p.iccid);
        else {
            const free = rows.find(r => !r.staff_id);
            if (free) simSel.val(free.iccid);
        }
        if (p.staffId) staffSel.val(String(p.staffId));
        simSel.prop('disabled', !!p.iccid);
        staffSel.prop('disabled', !!p.staffId);
        $('#assign-note').val('');
        $('#assign-error').addClass('d-none').text('');
        updateAssignHint();
        const blocked = !rows.length || !activeStaff().length;
        $('#assign-save').prop('disabled', blocked);
        if (blocked) $('#assign-error').removeClass('d-none').text(!rows.length ? 'Chưa có SIM nào trong hệ thống.' : 'Chưa có nhân sự đang làm. Thêm nhân sự trước.');
        $('#assignModal').modal('show');
    }

    function updateAssignHint() {
        const row = simRows().find(r => r.iccid === $('#assign-sim').val());
        const target = activeStaff().find(s => String(s.id) === String($('#assign-staff').val()));
        const hint = $('#assign-hint').removeClass('is-change is-same');
        if (!row || !target) { hint.text(''); return; }
        if (row.staff_id && row.staff_id === target.id) {
            hint.addClass('is-same').text(`${target.name} đang giữ SIM này rồi — không có gì thay đổi.`);
        } else if (row.staff_id) {
            hint.addClass('is-change').text(`Đổi người giữ: ${row.staff_name} → ${target.name}. Lần giao cho ${row.staff_name} sẽ được đóng lại và lưu trong lịch sử.`);
        } else {
            hint.text(`Giao ${simLabel(row)} cho ${target.name}.`);
        }
        $('#assign-save').text(row.staff_id && row.staff_id !== target.id ? 'Đổi người giữ' : 'Giao SIM');
    }

    function saveAssign() {
        const iccid = $('#assign-sim').val();
        const staffId = Number($('#assign-staff').val());
        if (!iccid || !staffId) return;
        const button = $('#assign-save').prop('disabled', true);
        api('POST', `/api/v1/modems/${encodeURIComponent(iccid)}/holder`, { staff_id: staffId, note: $('#assign-note').val() })
            .done(function () { $('#assignModal').modal('hide'); refresh(); })
            .fail(xhr => $('#assign-error').removeClass('d-none').text(errText(xhr, 'Không giao được SIM.')))
            .always(() => button.prop('disabled', false));
    }

    function releaseSim(sim) {
        const who = sim.staff_name ? ` từ ${sim.staff_name}` : '';
        const note = window.prompt(`Thu hồi ${simLabel(sim)}${who}?\nSIM sẽ không còn ai đứng tên. Ghi chú lý do (có thể bỏ trống):`, '');
        if (note === null) return;
        api('DELETE', `/api/v1/modems/${encodeURIComponent(sim.iccid)}/holder`, { note })
            .done(refresh)
            .fail(xhr => window.alert(errText(xhr, 'Không thu hồi được SIM.')));
    }

    // ----- Bàn giao toàn bộ SIM -----
    function openTransfer(staff) {
        $('#transfer-from-id').val(staff.id);
        $('#transfer-summary').text(`${staff.name} đang giữ ${staff.sims.length} SIM: ${staff.sims.map(simLabel).join(', ')}.`);
        const sel = $('#transfer-to').empty();
        activeStaff().filter(s => s.id !== staff.id).forEach(s => sel.append($('<option>').val(s.id).text(s.department ? `${s.name} · ${s.department}` : s.name)));
        $('#transfer-note').val('');
        const none = !sel.children().length;
        $('#transfer-error').toggleClass('d-none', !none).text(none ? 'Chưa có nhân sự đang làm nào khác để nhận. Thêm nhân sự trước.' : '');
        $('#transfer-save').prop('disabled', none);
        $('#transferModal').modal('show');
    }

    function saveTransfer() {
        const from = $('#transfer-from-id').val();
        const to = Number($('#transfer-to').val());
        if (!from || !to) return;
        const button = $('#transfer-save').prop('disabled', true);
        api('POST', `/api/v1/staff/${from}/transfer`, { to_staff_id: to, note: $('#transfer-note').val() })
            .done(function () { $('#transferModal').modal('hide'); refresh(); })
            .fail(xhr => $('#transfer-error').removeClass('d-none').text(errText(xhr, 'Không bàn giao được.')))
            .always(() => button.prop('disabled', false));
    }

    // ----- Nghỉ việc / làm lại / xoá -----
    function markLeft(staff) {
        const holding = (staff.sims || []).length;
        if (holding) {
            const ok = window.confirm(`${staff.name} đang giữ ${holding} SIM.\n\nBấm OK để đánh dấu nghỉ việc và THU HỒI cả ${holding} SIM (không còn ai đứng tên).\nMuốn chuyển cho người khác thì bấm Huỷ rồi dùng "Bàn giao" trước.`);
            if (!ok) return;
        } else if (!window.confirm(`Đánh dấu ${staff.name} đã nghỉ việc?\nLịch sử giao nhận vẫn được giữ.`)) {
            return;
        }
        api('POST', `/api/v1/staff/${staff.id}/leave`, { release_sims: holding > 0 })
            .done(refresh)
            .fail(xhr => window.alert(errText(xhr, 'Không cập nhật được.')));
    }

    function markReturned(staff) {
        api('POST', `/api/v1/staff/${staff.id}/return`, {}).done(refresh).fail(xhr => window.alert(errText(xhr, 'Không cập nhật được.')));
    }

    function removeStaff(staff) {
        if (!window.confirm(`Xoá hẳn ${staff.name}?\nChỉ xoá được người chưa từng được giao SIM; người đã có lịch sử thì dùng "Nghỉ việc".`)) return;
        api('DELETE', `/api/v1/staff/${staff.id}`).done(refresh).fail(xhr => window.alert(errText(xhr, 'Không xoá được.')));
    }

    $(document).ready(function () {
        $('[data-staff-tab]').on('click', function () { showTab($(this).data('staff-tab')); });
        $('#btn-add-staff').on('click', () => openStaffForm(null));
        $('#btn-assign-sim').on('click', () => openAssign({}));
        $('#staff-form-save').on('click', saveStaffForm);
        $('#assign-save').on('click', saveAssign);
        $('#assign-sim, #assign-staff').on('change', updateAssignHint);
        $('#transfer-save').on('click', saveTransfer);
        $('#staff-left-toggle').on('click', function () { state.showLeft = !state.showLeft; renderPeople(); });
        $('#staff-sim-filter button').on('click', function () {
            state.simFilter = $(this).data('filter');
            $('#staff-sim-filter button').removeClass('active');
            $(this).addClass('active');
            renderSims();
        });
        $('#staff-sim-search').on('input', function () { state.simQuery = $(this).val(); renderSims(); });
        $('#staff-history-staff, #staff-history-sim, #staff-history-open').on('change', function () { state.historyPage = 1; loadHistory(); });
        $('#staff-history-prev').on('click', function () { state.historyPage = Math.max(1, state.historyPage - 1); loadHistory(); });
        $('#staff-history-next').on('click', function () { state.historyPage += 1; loadHistory(); });
        $('#btn-export-assignments').on('click', function () {
            $.get('/api/v1/sim-assignments', historyParams(1, 500)).done(function (res) {
                const url = URL.createObjectURL(new Blob(['﻿', assignmentCsv(res.data)], { type: 'text/csv;charset=utf-8' }));
                const link = document.createElement('a');
                link.href = url;
                link.download = `smsie-giao-nhan-sim-${new Date().toISOString().slice(0, 10)}.csv`;
                link.click();
                URL.revokeObjectURL(url);
            });
        });
        $(document).on('smsie:route', function (_event, route) {
            if (route.view === 'staff') refresh();
        });
    });
})();
