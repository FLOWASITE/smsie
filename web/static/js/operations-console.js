const opsState = {
    modems: [],
    messages: [],
    bays: [],
    slotEvents: [],
    slotTab: 'tray',
    phoneNumbers: {},
    balanceChecks: {},
    phoneLookups: {},
    phoneHistory: [],
    balanceStatus: [],
    simHealth: [],
    keepalive: { config: {}, items: [] },
    keepaliveRuns: {},
    loadedAt: null
};

function groupOpsMessages(messages) {
    const byPhone = new Map();
    (messages || []).forEach(message => {
        const phone = message.phone || 'Không rõ số';
        if (!byPhone.has(phone)) byPhone.set(phone, []);
        byPhone.get(phone).push(message);
    });
    return Array.from(byPhone, ([phone, items]) => {
        const sorted = items.slice().sort((left, right) => new Date(left.timestamp) - new Date(right.timestamp));
        return { phone, messages: sorted, latest: sorted[sorted.length - 1] };
    }).sort((left, right) => new Date(right.latest.timestamp) - new Date(left.latest.timestamp));
}

function describeOpsMessageRoute(message, modems, mappings, phoneNumbers) {
    const modem = (modems || []).find(item => item.iccid === message.iccid) || {};
    const slot = mappings && mappings[message.iccid];
    const phone = phoneNumbers && phoneNumbers[message.iccid];
    const identity = [];
    if (slot) identity.push(`Khe ${slot}`);
    if (phone) identity.push(phone);
    if (modem.port_name) identity.push(modem.port_name);
    if (!identity.length && message.iccid) identity.push(message.iccid);
    return `${message.type === 'sent' ? 'Gửi từ' : 'Nhận tại'} ${identity.join(' · ')}`;
}

function summarizeOpsData(modems, messages) {
    const modemList = modems || [];
    const messageList = messages || [];
    return {
        modemTotal: modemList.length,
        modemOnline: modemList.filter(opsIsOnline).length,
        weakSignal: modemList.filter(modem => Number(modem.signal_strength || 0) < 40).length,
        messageTotal: messageList.length,
        received: messageList.filter(message => message.type === 'received').length,
        sent: messageList.filter(message => message.type === 'sent').length,
        delivered: messageList.filter(message => String(message.status || '').toLowerCase() === 'delivered').length,
        failed: messageList.filter(message => ['failed', 'undelivered'].includes(String(message.status || '').toLowerCase())).length,
        unread: messageList.filter(message => message.type === 'received' && !message.is_read).length
    };
}

function buildOpsCsv(messages) {
    const quote = value => `"${String(value === undefined || value === null ? '' : value).replaceAll('"', '""')}"`;
    const rows = [['timestamp', 'direction', 'phone', 'iccid', 'status', 'content']];
    (messages || []).forEach(message => {
        rows.push([
            message.timestamp,
            message.type,
            message.phone,
            message.iccid,
            message.status || (message.type === 'sent' ? 'sent' : 'received'),
            message.content
        ]);
    });
    return rows.map(row => row.map(quote).join(',')).join('\r\n');
}

function opsElement(tag, className, text) {
    const element = $(`<${tag}>`);
    if (className) element.addClass(className);
    if (text !== undefined) element.text(text);
    return element;
}

function opsIsOnline(modem) {
    return modem && String(modem.status || '').toLowerCase() === 'online';
}

function opsMoney(value) {
    return `${Number(value || 0).toLocaleString('vi-VN')} đ`;
}

function bayBalanceLabel(bay) {
    return bay && bay.balance_updated_at ? opsMoney(bay.balance_vnd) : 'Chưa kiểm tra';
}

function opsBalanceLabel(modem) {
    return modem && modem.balance_updated_at ? opsMoney(modem.balance_vnd) : 'Chưa kiểm tra';
}

function describeBalanceLevel(item) {
    const it = item || {};
    const days = it.days_left;
    if (it.level === 'low') return { tone: 'danger', label: `Số dư ${opsMoney(it.balance_vnd)} dưới ngưỡng ${opsMoney(it.threshold_vnd)}` };
    if (it.level === 'forecast') return { tone: 'warning', label: `Dự kiến hết tiền sau ≈${Math.round(days)} ngày` };
    if (it.level === 'ok') return { tone: 'ok', label: days !== null && days !== undefined ? `Còn ≈${Math.round(days)} ngày` : 'Số dư ổn' };
    return { tone: 'muted', label: 'Chưa đọc số dư' };
}

// Finding từ /sim-health: no_sms/unregistered = nguy (danger), absent = nhắc (warning). Nhãn = detail server.
function describeHealthFinding(finding) {
    const f = finding || {};
    return { tone: f.kind === 'absent' ? 'warning' : 'danger', label: f.detail || '' };
}

const KEEPALIVE_RECENT_MS = 7 * 24 * 60 * 60 * 1000;
const KEEPALIVE_SMS_VND = 300; // ponytail: giá SMS nội mạng ước tính; thành cột config nếu cần chính xác

function opsShortDate(value) {
    const d = new Date(value);
    return `${String(d.getDate()).padStart(2, '0')}/${String(d.getMonth() + 1).padStart(2, '0')}`;
}

// Lần chạy cuối còn "mới" (≤7 ngày) và không phải sent → đáng cảnh báo.
function keepaliveRecentProblem(item, now) {
    const run = item && item.last_run;
    if (!run || run.status === 'sent') return null;
    if (now - new Date(run.ran_at).getTime() > KEEPALIVE_RECENT_MS) return null;
    return run;
}

function describeKeepalive(item, now) {
    const it = item || {};
    const at = now === undefined ? Date.now() : now;
    if (!it.enabled) return { tone: 'muted', label: 'Tắt' };
    const run = keepaliveRecentProblem(it, at);
    if (run) return { tone: run.status === 'failed' ? 'danger' : 'warning', label: `Nuôi SIM ${run.status}: ${run.reason || ''}` };
    if (it.next_due_at) return { tone: 'ok', label: `Lần kế tiếp ${opsShortDate(it.next_due_at)}` };
    return { tone: 'ok', label: 'Đang bật' };
}

// Mốc chạy kế tiếp: run_hour:00 hôm nay nếu chưa qua, không thì ngày mai.
function keepaliveNextRun(runHour, now) {
    const next = new Date(now === undefined ? Date.now() : now);
    next.setHours(Number(runHour) || 0, 0, 0, 0);
    if (next.getTime() <= (now === undefined ? Date.now() : now)) next.setDate(next.getDate() + 1);
    return next;
}

function opsKeepaliveByICCID() {
    const map = {};
    (opsState.keepalive.items || []).forEach(item => { map[item.iccid] = item; });
    return map;
}

function patchModemProfile(iccid, body) {
    return $.ajax({ url: `/api/v1/modems/${encodeURIComponent(iccid)}/profile`, method: 'PATCH', contentType: 'application/json', data: JSON.stringify(body) });
}

function opsHealthByICCID() {
    const map = {};
    (opsState.simHealth || []).forEach(item => { if ((item.findings || []).length) map[item.iccid] = item; });
    return map;
}

function opsDate(value) { return value ? new Date(value).toLocaleDateString('vi-VN') : '—'; }
function opsDateTime(value) { return value ? new Date(value).toLocaleString('vi-VN') : '—'; }

function balanceSparkline(snapshots) {
    return (snapshots || []).slice(-7).map(s => `${Math.round(Number(s.balance_vnd || 0) / 1000)}k`).join(' → ');
}

function opsBalanceByICCID() {
    const map = {};
    opsState.balanceStatus.forEach(item => { map[item.iccid] = item; });
    return map;
}

function opsSlotByICCID() {
    const map = {};
    opsState.bays.forEach(bay => { if (bay.current_iccid && bay.slot_number) map[bay.current_iccid] = bay.slot_number; });
    return map;
}

function assignBaySlot(imei, slot) {
    return $.ajax({ url: `/api/v1/bays/${encodeURIComponent(imei)}`, method: 'PATCH', contentType: 'application/json', data: JSON.stringify({ slot_number: slot }) });
}

function opsAlerts() {
    const alerts = [];
    const unassigned = buildTrayCells(opsState.bays, Date.now()).unassigned;
    if (unassigned.length) {
        alerts.push({
            level: 'warning',
            title: `${unassigned.length} modem chưa gán khe`,
            note: 'Gán khe vật lý trước khi tạo lịch duy trì.'
        });
    }
    opsState.modems.forEach(modem => {
        if (!opsIsOnline(modem)) {
            alerts.push({
                level: 'danger',
                title: `${modem.port_name || modem.iccid} đang ngoại tuyến`,
                note: 'Kiểm tra nguồn, cáp USB và trạng thái worker.'
            });
        } else if (Number(modem.signal_strength || 0) < 40) {
            alerts.push({
                level: 'warning',
                title: `${modem.port_name || modem.iccid} có sóng yếu`,
                note: `Mức hiện tại ${Number(modem.signal_strength || 0)}%.`
            });
        }
    });
    const slots = opsSlotByICCID();
    opsState.balanceStatus.forEach(item => {
        if (item.level !== 'low' && item.level !== 'forecast') return;
        const d = describeBalanceLevel(item);
        alerts.push({ level: d.tone, title: `Khe ${item.slot_number ?? slots[item.iccid] ?? '—'} · ${item.phone_number || item.iccid}`, note: d.label });
    });
    (opsState.simHealth || []).forEach(item => {
        (item.findings || []).forEach(f => {
            const d = describeHealthFinding(f);
            alerts.push({ level: d.tone, title: `Khe ${item.slot_number ?? slots[item.iccid] ?? '—'} · ${item.phone_number || item.iccid}`, note: d.label });
        });
    });
    (opsState.keepalive.items || []).forEach(item => {
        const run = keepaliveRecentProblem(item, Date.now());
        if (!run) return;
        alerts.push({ level: 'warning', title: `Khe ${item.slot_number ?? slots[item.iccid] ?? '—'} · ${item.phone_number || item.iccid}`, note: `Nuôi SIM ${run.status}: ${run.reason || ''}` });
    });
    if (!alerts.length) {
        alerts.push({ level: 'ok', title: 'Không có cảnh báo', note: 'Các modem đã gán đang hoạt động bình thường.' });
    }
    return alerts;
}

function renderOpsKPIs() {
    const online = opsState.modems.filter(opsIsOnline).length;
    const mapped = Object.keys(opsSlotByICCID()).length;
    const unread = opsState.messages.filter(message => message.type === 'received' && !message.is_read).length;
    const cards = [
        { label: 'Modem trực tuyến', value: `${online}/32`, note: `${32 - mapped} khe chưa có mapping`, icon: 'bi-router', tone: 'mint' },
        { label: 'SIM đã gán', value: `${mapped}/32`, note: mapped ? 'Đã lưu hồ sơ vật lý' : 'Chưa có mapping vật lý', icon: 'bi-sim', tone: 'blue' },
        { label: 'Tin chưa đọc', value: unread, note: `${opsState.messages.length} tin trong lịch sử`, icon: 'bi-chat-left-text', tone: 'green' },
        { label: 'Cảnh báo', value: opsAlerts().filter(alert => alert.level !== 'ok').length, note: `${opsState.balanceStatus.filter(item => item.level === 'low' || item.level === 'forecast').length} SIM sắp hết tiền · ${Object.keys(opsHealthByICCID()).length} SIM có dấu hiệu chết`, icon: 'bi-exclamation-triangle', tone: 'coral' }
    ];
    const container = $('#ops-kpis').empty();
    cards.forEach(card => {
        const item = opsElement('article', 'ops-kpi');
        item.append(opsElement('div', `ops-kpi-icon tone-${card.tone}`).append($('<i>').addClass(`bi ${card.icon}`)));
        const body = opsElement('div', 'ops-kpi-body');
        body.append(opsElement('div', 'ops-kpi-label', card.label));
        body.append(opsElement('div', 'ops-kpi-value', String(card.value)));
        body.append(opsElement('div', 'ops-kpi-note', card.note));
        item.append(body);
        container.append(item);
    });
}

function renderSimRails() {
    const sorted = opsState.modems.slice().sort((left, right) => Number(left.slot_number || 99) - Number(right.slot_number || 99));
    ['#ops-sim-rail-overview', '#ops-sim-rail-sms'].forEach(selector => {
        const targetView = selector.endsWith('sms') ? 'sms' : 'overview';
        const selectedICCID = window.currentAppRoute ? window.currentAppRoute().iccid : '';
        const rail = $(selector).empty();
        for (let index = 0; index < 8; index += 1) {
            const modem = sorted[index];
            const card = opsElement('button', `sim-rail-card${modem && (!selectedICCID || modem.iccid === selectedICCID) ? ' is-active' : ''}${modem ? '' : ' is-empty'}`);
            card.attr('type', 'button');
            card.append(opsElement('div', 'sim-rail-icon').append($('<i>').addClass('bi bi-sim')));
            const body = opsElement('div', 'sim-rail-body');
            body.append(opsElement('strong', '', `SIM ${modem && modem.slot_number ? modem.slot_number : index + 1}`));
            body.append(opsElement('div', 'sim-rail-number', modem ? (modem.phone_number || 'Chưa biết số') : 'Chưa gán'));
            body.append(opsElement('div', 'sim-rail-meta', modem ? `Khe ${modem.slot_number || '—'} · ${modem.port_name || '—'}` : 'Không có SIM'));
            card.append(body, opsElement('span', `sim-rail-dot${modem && opsIsOnline(modem) ? ' online' : ''}`));
            if (modem) card.click(() => window.navigateApp(targetView, modem.iccid));
            rail.append(card);
        }
        const more = opsElement('button', 'sim-rail-next').attr('type', 'button').attr('aria-label', 'Xem thêm SIM').append($('<i>').addClass('bi bi-chevron-right'));
        more.click(() => window.navigateApp('slots'));
        rail.append(more);
    });
}

function renderMiniSlots() {
    const { cells } = buildTrayCells(opsState.bays, Date.now());
    const grid = $('#ops-mini-slots').empty();
    cells.forEach(cell => {
        const item = opsElement('div', `mini-slot tone-${cell.tone}${cell.swapped ? ' is-swapped' : ''}`, String(cell.slot).padStart(2, '0'));
        item.attr('title', cell.bay ? `Khe ${cell.slot}: ${cell.bay.phone_number || cell.bay.current_iccid || 'không có SIM'}` : `Khe ${cell.slot}: chưa gán modem`);
        grid.append(item);
    });
}

function renderOpsAlertSummary() {
    const alerts = opsAlerts();
    $('#nav-alert-count').text(alerts.filter(alert => alert.level !== 'ok').length);
    const container = $('#ops-alert-summary').empty();
    alerts.slice(0, 4).forEach(alert => {
        const row = opsElement('div', `ops-list-row alert-${alert.level}`);
        row.append(opsElement('div', 'ops-list-icon').append($('<i>').addClass(alert.level === 'danger' ? 'bi bi-x-octagon' : alert.level === 'ok' ? 'bi bi-check2' : 'bi bi-exclamation-triangle')));
        const body = opsElement('div');
        body.append(opsElement('div', 'ops-list-title', alert.title));
        body.append(opsElement('div', 'ops-list-note', alert.note));
        row.append(body);
        container.append(row);
    });
}

function renderRecentMessages() {
    const container = $('#ops-recent-messages').empty();
    const messages = opsState.messages.slice(0, 5);
    if (!messages.length) {
        container.append(opsElement('div', 'ops-empty', 'Chưa có hoạt động SMS.'));
        return;
    }
    messages.forEach(message => {
        const outgoing = message.type === 'sent';
        const row = opsElement('div', 'ops-list-row');
        row.append(opsElement('div', 'ops-list-icon').append($('<i>').addClass(outgoing ? 'bi bi-arrow-up-right' : 'bi bi-arrow-down-left')));
        const body = opsElement('div');
        body.append(opsElement('div', 'ops-list-title', `${outgoing ? 'Gửi tới' : 'Nhận từ'} ${message.phone || 'Không rõ số'}`));
        body.append(opsElement('div', 'ops-list-note', message.content || 'Không có nội dung'));
        row.append(body);
        row.append(opsElement('time', 'ops-list-note', new Date(message.timestamp).toLocaleString('vi-VN')));
        container.append(row);
    });
}

function opsMessageStatus(message) {
    if (message.type === 'received') return { label: 'Tin nhận', className: 'received' };
    const status = String(message.status || 'sent').toLowerCase();
    const labels = {
        queued: 'Đang chờ',
        sending: 'Đang gửi',
        sent: 'Đã gửi',
        delivered: 'Đã giao',
        failed: 'Thất bại',
        undelivered: 'Không giao được'
    };
    return { label: labels[status] || status, className: status };
}

function renderConversationThread(thread, container) {
    container.empty();
    const header = opsElement('header', 'conversation-detail-head');
    const identity = opsElement('div');
    const avatar = opsElement('div', 'contact-avatar tone-mint', String(thread.phone).replace(/[^A-Za-z0-9]/g, '').slice(0, 2).toUpperCase() || 'SMS');
    identity.append(opsElement('div', 'eyebrow', 'Hội thoại SMS'));
    identity.append(opsElement('h2', '', thread.phone));
    identity.append(opsElement('div', 'ops-list-note', `${thread.messages.length} tin nhắn`));
    header.append(avatar, identity);

    const latest = thread.latest || {};
    const compose = opsElement('button', 'btn btn-dark btn-sm', 'Soạn tin');
    compose.attr('type', 'button').click(function () {
        const iccid = latest.iccid || (opsState.modems[0] && opsState.modems[0].iccid);
        if (!iccid || typeof showSMSModal !== 'function') return;
        showSMSModal(iccid);
        $('#sms-phone').val(thread.phone);
    });
    header.append(compose);
    container.append(header);

    const timeline = opsElement('div', 'message-timeline');
    thread.messages.forEach(message => {
        const outgoing = message.type === 'sent';
        const bubble = opsElement('article', `message-bubble ${outgoing ? 'is-outgoing' : 'is-incoming'}`);
        bubble.append(opsElement('div', 'message-copy', message.content || 'Không có nội dung'));
        bubble.append(opsElement('div', 'message-route', describeOpsMessageRoute(message, opsState.modems, opsSlotByICCID(), opsState.phoneNumbers)));
        const meta = opsElement('div', 'message-meta');
        const status = opsMessageStatus(message);
        meta.append(opsElement('span', `message-status status-${status.className}`, status.label));
        meta.append(opsElement('time', '', new Date(message.timestamp).toLocaleString('vi-VN')));
        bubble.append(meta);
        timeline.append(bubble);
    });
    container.append(timeline);

    const composer = opsElement('div', 'conversation-composer');
    composer.append(opsElement('button', 'composer-icon').attr({ type: 'button', disabled: true, 'aria-label': 'Đính kèm tệp chưa hỗ trợ' }).append($('<i>').addClass('bi bi-paperclip')));
    const input = $('<textarea>').addClass('form-control').attr({ rows: 1, placeholder: 'Nhập nội dung tin nhắn…', 'aria-label': 'Nội dung SMS' });
    const count = opsElement('span', 'composer-count', '0/160');
    input.on('input', function () { count.text(`${String(input.val() || '').length}/160`); });
    const send = opsElement('button', 'btn btn-send-draft', 'Tiếp tục gửi');
    send.attr('type', 'button').click(function () {
        const text = String(input.val() || '').trim();
        if (!text) return;
        const iccid = latest.iccid || (opsState.modems[0] && opsState.modems[0].iccid);
        if (!iccid || typeof showSMSModal !== 'function') return;
        showSMSModal(iccid);
        $('#sms-phone').val(thread.phone);
        $('#sms-content').val(text);
    });
    composer.append(opsElement('div', 'composer-input').append(input, count), send);
    container.append(composer);
}

function renderConversationInbox(messages) {
    opsState.currentMessages = messages || [];
    const query = String($('#sms-search').val() || '').trim().toLowerCase();
    const filtered = opsState.currentMessages.filter(message => {
        if (!query) return true;
        return String(message.phone || '').toLowerCase().includes(query) || String(message.content || '').toLowerCase().includes(query);
    });
    const threads = groupOpsMessages(filtered);
    const root = $('#sms-list').empty().addClass('conversation-grid');
    if (!threads.length) {
        root.removeClass('conversation-grid').append(opsElement('div', 'ops-empty', 'Không tìm thấy hội thoại phù hợp.'));
        return;
    }

    const list = opsElement('aside', 'conversation-list');
    const detail = opsElement('section', 'conversation-detail');
    threads.forEach((thread, index) => {
        const item = opsElement('button', `conversation-contact${index === 0 ? ' is-active' : ''}`);
        item.attr('type', 'button');
        item.append(opsElement('div', `contact-avatar tone-${['mint', 'coral', 'blue', 'amber'][index % 4]}`, String(thread.phone).replace(/[^A-Za-z0-9]/g, '').slice(0, 2).toUpperCase() || 'SMS'));
        const itemBody = opsElement('div', 'conversation-contact-body');
        const top = opsElement('div', 'conversation-contact-top');
        top.append(opsElement('strong', '', thread.phone));
        top.append(opsElement('time', '', new Date(thread.latest.timestamp).toLocaleDateString('vi-VN')));
        itemBody.append(top);
        itemBody.append(opsElement('div', 'conversation-preview', thread.latest.content || 'Không có nội dung'));
        item.append(itemBody);
        item.click(function () {
            list.find('.conversation-contact').removeClass('is-active');
            item.addClass('is-active');
            renderConversationThread(thread, detail);
        });
        list.append(item);
    });
    root.append(list, detail);
    renderConversationThread(threads[0], detail);
}

if (typeof window !== 'undefined') {
    window.renderOpsMessages = renderConversationInbox;
}

function renderLiveUnassigned() {
    const container = $('#ops-live-unassigned').empty();
    const { cells, unassigned } = buildTrayCells(opsState.bays, Date.now());
    const header = opsElement('div', 'ops-panel-head');
    const heading = opsElement('div');
    heading.append(opsElement('div', 'eyebrow', 'Modem phát hiện qua USB'));
    heading.append(opsElement('h2', '', 'Chưa gán khe'));
    header.append(heading);
    container.append(header);

    if (!unassigned.length) {
        container.append(opsElement('div', 'ops-empty', 'Không còn modem chờ gán khe.'));
        return;
    }
    const freeSlots = cells.filter(cell => !cell.bay);
    unassigned.forEach(bay => {
        const row = opsElement('div', 'unassigned-device');
        const identity = opsElement('div');
        identity.append(opsElement('strong', '', bay.port_name || 'COM chưa rõ'));
        identity.append(opsElement('div', 'ops-list-note', [`IMEI …${String(bay.imei).slice(-4)}`, bay.phone_number || bay.current_iccid || 'không có SIM'].join(' · ')));
        row.append(identity);

        const select = $('<select>').addClass('form-select form-select-sm').attr('aria-label', `Chọn khe cho ${bay.port_name || bay.imei}`);
        select.append($('<option>').val('').text('Chọn khe…'));
        freeSlots.forEach(cell => select.append($('<option>').val(cell.slot).text(`Khe ${String(cell.slot).padStart(2, '0')}`)));
        const button = opsElement('button', 'btn btn-sm btn-dark', 'Gán khe');
        button.attr('type', 'button').click(function () {
            const slot = Number(select.val());
            if (!slot) return;
            button.prop('disabled', true).text('Đang lưu…');
            assignBaySlot(bay.imei, slot)
                .done(loadOperationsData)
                .fail(function (xhr) {
                    button.prop('disabled', false).text('Gán khe');
                    const message = xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không gán được khe';
                    select.addClass('is-invalid').attr('title', message);
                });
        });
        row.append(opsElement('div', 'unassigned-actions').append(select, button));
        container.append(row);
    });
}

function bayCard(cell) {
    const bay = cell.bay;
    const status = bay && bay.current_iccid ? opsBalanceByICCID()[bay.current_iccid] : null;
    const level = status ? status.level : '';
    const sick = bay && bay.current_iccid && opsHealthByICCID()[bay.current_iccid];
    const card = opsElement('button', `bay ${cell.tone}${cell.swapped ? ' swapped' : ''}${level === 'low' || level === 'forecast' ? ` ${level}` : ''}${sick ? ' sick' : ''}`);
    card.attr('type', 'button').prop('disabled', cell.tone === 'missing');
    const body = opsElement('div');
    body.append(opsElement('div', 'num', `Khe ${String(cell.slot).padStart(2, '0')}`));
    if (!bay) {
        body.append(opsElement('div', 'phone', 'Chưa gán modem'));
    } else if (!bay.current_iccid) {
        body.append(opsElement('div', 'phone', 'Không có SIM'));
        body.append(opsElement('div', 'op', bay.port_name || `IMEI …${String(bay.imei || '').slice(-4)}`));
    } else {
        body.append(opsElement('div', 'phone', bay.phone_number || bay.current_iccid));
        body.append(opsElement('div', 'op', [bay.operator, bay.port_name].filter(Boolean).join(' · ') || '—'));
        body.append(opsElement('div', `bal${level === 'low' ? ' low' : ''}`, bayBalanceLabel(bay)));
    }
    card.append(opsElement('span', 'chip'), body, opsElement('span', 'dot'));
    if (cell.swapped) card.append(opsElement('span', 'swap', '↔ 24h'));
    if (bay) card.click(() => showTrayPop(cell, card));
    return card;
}

function showTrayPop(cell, card) {
    const bay = cell.bay;
    const pop = $('#ops-tray-pop').empty();
    const panel = card.closest('.ops-panel');
    const r = card[0].getBoundingClientRect();
    const p = panel[0].getBoundingClientRect();
    pop.css({ left: Math.min(r.left - p.left, p.width - 280) + 'px', top: (r.bottom - p.top + 6) + 'px' });
    pop.append(opsElement('b', '', `Khe ${cell.slot}${bay.phone_number ? ' · ' + bay.phone_number : ''}`));
    const dl = $('<dl>');
    const status = bay.current_iccid ? opsBalanceByICCID()[bay.current_iccid] : null;
    const rows = bay.current_iccid
        ? [['ICCID', bay.current_iccid], ['IMEI', bay.imei], ['Nhà mạng', bay.operator || '—'], ['Số dư', bayBalanceLabel(bay)], ['Ở khe từ', bay.last_event_at ? new Date(bay.last_event_at).toLocaleString('vi-VN') : '—']]
        : [['IMEI', bay.imei], ['Cổng', bay.port_name || '—']];
    if (status && (status.level === 'low' || status.level === 'forecast' || (status.level === 'ok' && status.days_left != null))) rows.push(['Dự kiến hết', describeBalanceLevel(status).label]);
    const health = bay.current_iccid ? opsHealthByICCID()[bay.current_iccid] : null;
    if (health) rows.push(['Sức khoẻ', health.findings.map(f => f.detail).join(' · ')]);
    rows.forEach(([k, v]) => dl.append($('<dt>').text(k), $('<dd>').text(v)));
    pop.append(dl);
    if (bay.current_iccid) {
        const link = opsElement('a', 'text-action', 'Xem lịch sử khe →').attr('href', '#');
        link.click(event => { event.preventDefault(); openSlotHistory(bay.current_iccid); });
        pop.append(opsElement('div', 'mt-2').append(link));
    }
    pop.prop('hidden', false);
}

function renderTray() {
    const { cells } = buildTrayCells(opsState.bays, Date.now());
    const tray = $('#ops-tray').empty();
    cells.forEach(cell => tray.append(bayCard(cell)));
}

function showSlotTab(tab) {
    opsState.slotTab = tab;
    $('[data-slot-tab]').each(function () { $(this).toggleClass('is-active', $(this).data('slot-tab') === tab); });
    ['tray', 'history', 'calibration'].forEach(name => $(`#slot-tab-${name}`).toggleClass('d-none', name !== tab));
}

function openSlotHistory(iccid) {
    showSlotTab('history');
    window.navigateApp('slots', iccid);
}

function loadSlotEvents(iccid) {
    const params = { iccid, page_size: 500 };
    const from = $('#slot-events-from').val();
    const to = $('#slot-events-to').val();
    if (from) params.from = from;
    if (to) params.to = to;
    $.get(`/api/v1/modems/${encodeURIComponent(iccid)}/phone-history`).done(function (response) {
        opsState.phoneHistory = response.data || [];
        renderSlotHistory();
    });
    return $.get('/api/v1/slot-events', params).done(function (response) {
        opsState.slotEvents = response.data || [];
        renderSlotHistory();
        renderAuditPreview();
    });
}

function renderSlotHistory() {
    const route = window.currentAppRoute ? window.currentAppRoute() : { iccid: '' };
    const card = $('#ops-slot-sim-card').empty();
    const timeline = $('#ops-slot-timeline').empty();
    if (!route.iccid) {
        card.append(opsElement('div', 'ops-empty', 'Chọn một khe trên bản đồ khay rồi bấm "Xem lịch sử khe".'));
        return;
    }
    const bay = opsState.bays.find(b => b.current_iccid === route.iccid);
    const modem = opsState.modems.find(m => m.iccid === route.iccid) || {};
    card.append(opsElement('div', 'eyebrow', 'SIM · ICCID'));
    card.append(opsElement('h3', 'mono', route.iccid));
    const dl = $('<dl>').addClass('kv');
    const rows = [['Số thuê bao', modem.phone_number || '—'], ['Số dư', opsBalanceLabel(modem)], ['Lần đảo', `${opsState.slotEvents.filter(e => e.event === 'moved').length} lần`]];
    const previous = (opsState.phoneHistory || []).find(h => h.iccid === route.iccid && h.old_phone);
    if (previous) rows.push(['Số trước', `${previous.old_phone} (${new Date(previous.at).toLocaleDateString('vi-VN', { day: '2-digit', month: '2-digit' })})`]);
    rows.forEach(([k, v]) => dl.append($('<dt>').text(k), $('<dd>').text(v)));
    card.append(dl);
    card.append(opsElement('div', 'now', bay
        ? [`Đang ở khe ${bay.slot_number || '— (chưa gán)'}`, bay.port_name, bay.status === 'online' ? 'trực tuyến' : 'ngoại tuyến'].filter(Boolean).join(' · ')
        : 'Hiện không nằm trong khay'));

    if (!opsState.slotEvents.length) {
        timeline.append(opsElement('div', 'ops-empty', 'Chưa có sự kiện đổi khe cho SIM này.'));
        return;
    }
    const icons = { moved: 'bi-arrow-left-right', removed: 'bi-eject', inserted: 'bi-box-arrow-in-down' };
    groupSlotEventsByDay(opsState.slotEvents).forEach(group => {
        timeline.append(opsElement('div', 'day', new Date(group.day).toLocaleDateString('vi-VN')));
        const ul = $('<ul>').addClass('timeline');
        group.events.forEach(event => {
            const d = describeSlotEvent(event);
            const li = $('<li>');
            const when = opsElement('div', 'when');
            when.append(opsElement('b', '', new Date(event.detected_at).toLocaleTimeString('vi-VN')), document.createTextNode(event.port_name || ''));
            const ico = opsElement('span', `ico ${d.tone}`).append($('<i>').addClass(`bi ${icons[event.event] || 'bi-dot'}`));
            const what = opsElement('div', 'what');
            what.append(opsElement('span', 'path', d.path));
            if (event.operator) what.append(opsElement('small', '', event.operator));
            const bal = opsElement('div', `bal${d.balance === '—' ? ' na' : ''}`, d.balance).append(opsElement('small', '', d.balanceNote));
            li.append(when, ico, what, bal);
            ul.append(li);
        });
        timeline.append(ul);
    });
}

function renderBayCalibration() {
    const { cells, unassigned } = buildTrayCells(opsState.bays, Date.now());
    const body = $('#ops-cal-body').empty();
    const assigned = cells.filter(c => c.bay).length;
    $('#ops-cal-summary').text(`${assigned}/32 khe đã gán · ${unassigned.length} modem chưa gán`);
    const freeSlots = cells.filter(c => !c.bay).map(c => c.slot);
    const isAdmin = typeof auth !== 'undefined' && auth.role === 'admin';
    const pill = bay => !bay ? opsElement('span', 'pill off', 'Chưa gán')
        : !bay.current_iccid ? opsElement('span', 'pill warn', 'Không SIM')
        : bay.status === 'online' ? opsElement('span', 'pill ok', 'Trực tuyến') : opsElement('span', 'pill off', 'Ngoại tuyến');
    const failMark = (input, xhr, fallback) => input.addClass('is-invalid').attr('title', xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : fallback);
    const phoneCell = bay => {
        const td = $('<td>').addClass('mono');
        if (!bay || !bay.current_iccid || !isAdmin) return td.text(bay && bay.phone_number ? bay.phone_number : '—');
        const input = $('<input>').attr({ type: 'tel', 'aria-label': `Số thuê bao khe ${bay.slot_number || bay.imei}` }).addClass('form-control form-control-sm mono').val(bay.phone_number || '');
        input.change(function () {
            $.ajax({ url: `/api/v1/modems/${encodeURIComponent(bay.current_iccid)}/profile`, method: 'PATCH', contentType: 'application/json', data: JSON.stringify({ phone_number: input.val().trim() }) })
                .done(loadOperationsData)
                .fail(xhr => failMark(input, xhr, 'Không lưu được số thuê bao'));
        });
        const { button, note } = phoneLookupButton(bay.current_iccid, bay.status === 'online');
        td.append($('<div>').addClass('d-flex gap-1 align-items-center').append(input, button));
        if (note) td.append(note);
        return td;
    };
    const balances = opsBalanceByICCID();
    const thresholdCell = bay => {
        const td = $('<td>').addClass('mono');
        if (!bay || !bay.current_iccid) return td.text('—');
        const status = balances[bay.current_iccid] || {};
        const modem = opsState.modems.find(m => m.iccid === bay.current_iccid) || {};
        if (!isAdmin) return td.text(status.threshold_vnd == null ? '—' : `${opsMoney(status.threshold_vnd)} (${status.threshold_source === 'sim' ? 'riêng' : 'mặc định'})`);
        const input = $('<input>').attr({ type: 'number', min: 0, step: 1000, placeholder: 'mặc định', 'aria-label': `Ngưỡng số dư khe ${bay.slot_number || bay.imei}` }).addClass('form-control form-control-sm mono').val(modem.low_balance_vnd ?? '');
        input.change(function () {
            const value = input.val().trim();
            $.ajax({ url: `/api/v1/modems/${encodeURIComponent(bay.current_iccid)}/profile`, method: 'PATCH', contentType: 'application/json', data: JSON.stringify({ low_balance_vnd: value === '' ? null : Number(value) }) })
                .done(loadOperationsData)
                .fail(xhr => failMark(input, xhr, 'Không lưu được ngưỡng'));
        });
        return td.append(input);
    };
    const keepalive = opsKeepaliveByICCID();
    const keepaliveCell = bay => {
        const td = $('<td>');
        if (!bay || !bay.current_iccid) return td.text('—');
        const item = keepalive[bay.current_iccid] || {};
        if (!isAdmin) return td.text(item.enabled ? 'Bật' : 'Tắt');
        const input = $('<input>').attr({ type: 'checkbox', 'aria-label': `Nuôi SIM khe ${bay.slot_number || bay.imei}` }).addClass('form-check-input').prop('checked', !!item.enabled);
        input.change(function () {
            patchModemProfile(bay.current_iccid, { keepalive_enabled: input.prop('checked') })
                .done(loadOperationsData)
                .fail(xhr => failMark(input, xhr, 'Không lưu được công tắc nuôi SIM'));
        });
        return td.append(input);
    };
    const row = (slot, bay) => {
        const tr = $('<tr>');
        tr.append($('<td>').addClass('mono').text(slot ? String(slot).padStart(2, '0') : '—'));
        tr.append($('<td>').addClass('mono').text(bay ? bay.imei : '—'));
        tr.append($('<td>').addClass('mono').text(bay && bay.current_iccid ? bay.current_iccid : '—'));
        tr.append(phoneCell(bay));
        tr.append(thresholdCell(bay));
        tr.append(keepaliveCell(bay));
        tr.append($('<td>').append(pill(bay)));
        tr.append($('<td>').addClass('mono').text(bay && bay.last_seen_at ? new Date(bay.last_seen_at).toLocaleString('vi-VN') : '—'));
        const actions = $('<td>');
        if (bay && !isAdmin) {
            actions.text(slot ? `Khe ${String(slot).padStart(2, '0')}` : 'Chưa gán');
        } else if (bay) {
            const select = $('<select>').addClass('form-select form-select-sm').attr('aria-label', `Đổi khe cho ${bay.imei}`);
            select.append($('<option>').val('').text(slot ? 'Bỏ gán' : 'Chọn khe…'));
            freeSlots.forEach(s => select.append($('<option>').val(s).text(`Khe ${String(s).padStart(2, '0')}`)));
            select.change(function () {
                const value = $(this).val();
                if (!value && !slot) return;
                assignBaySlot(bay.imei, value ? Number(value) : null)
                    .done(loadOperationsData)
                    .fail(xhr => failMark(select, xhr, 'Không gán được khe'));
            });
            actions.append(select);
        }
        tr.append(actions);
        return tr;
    };
    cells.forEach(c => body.append(row(c.slot, c.bay)));
    unassigned.forEach(b => body.append(row(null, b)));
}

function balanceNeedsRefresh(modem) {
    if (!modem.balance_updated_at) return true;
    return Date.now() - new Date(modem.balance_updated_at).getTime() > 24 * 60 * 60 * 1000;
}

function pollBalanceResult(modem, previousUpdatedAt, attempt) {
    window.setTimeout(function () {
        $.get(`/api/v1/modems/${encodeURIComponent(modem.iccid)}`)
            .done(function (fresh) {
                if (fresh.balance_updated_at && fresh.balance_updated_at !== previousUpdatedAt) {
                    Object.assign(modem, fresh);
                    opsState.balanceChecks[modem.iccid] = { state: 'done' };
                    renderOperationsConsole();
                    return;
                }
                if (attempt < 5) {
                    pollBalanceResult(modem, previousUpdatedAt, attempt + 1);
                    return;
                }
                opsState.balanceChecks[modem.iccid] = { state: 'timeout' };
                renderOperationsConsole();
            })
            .fail(function () {
                opsState.balanceChecks[modem.iccid] = { state: 'error' };
                renderOperationsConsole();
            });
    }, 5000);
}

function requestBalanceCheck(modem) {
    const current = opsState.balanceChecks[modem.iccid];
    if (current && current.state === 'checking') return;
    const previousUpdatedAt = modem.balance_updated_at || '';
    opsState.balanceChecks[modem.iccid] = { state: 'checking' };
    renderOperationsConsole();
    $.ajax({
        url: `/api/v1/modems/${encodeURIComponent(modem.iccid)}/balance-check`,
        method: 'POST'
    }).done(function () {
        pollBalanceResult(modem, previousUpdatedAt, 0);
    }).fail(function (xhr) {
        if (xhr.status === 409) {
            pollBalanceResult(modem, previousUpdatedAt, 0);
            return;
        }
        opsState.balanceChecks[modem.iccid] = { state: 'error' };
        renderOperationsConsole();
    });
}

// describePhoneLookup: trạng thái nút "Đọc số" (thuần, có test node).
function describePhoneLookup(state) {
    if (!state || !state.state) return { label: 'Đọc số', tone: 'muted', busy: false };
    if (state.state === 'checking') return { label: 'Đang đọc số…', tone: 'muted', busy: true };
    if (state.state === 'done') return { label: `Đã đọc: ${state.phone}`, tone: 'ok', busy: false };
    if (state.state === 'timeout') return { label: 'Không nhận được số · thử lại', tone: 'warning', busy: false };
    return { label: state.message || 'Không gửi được yêu cầu đọc số', tone: 'danger', busy: false };
}

function pollPhoneLookup(iccid, previousPhone, attempt) {
    window.setTimeout(function () {
        $.get(`/api/v1/modems/${encodeURIComponent(iccid)}`)
            .done(function (fresh) {
                if (fresh.phone_number && fresh.phone_number !== previousPhone) {
                    opsState.phoneLookups[iccid] = { state: 'done', phone: fresh.phone_number };
                    loadOperationsData();
                    return;
                }
                if (attempt < 17) { pollPhoneLookup(iccid, previousPhone, attempt + 1); return; }
                opsState.phoneLookups[iccid] = { state: 'timeout' };
                renderOperationsConsole();
            })
            .fail(function () {
                opsState.phoneLookups[iccid] = { state: 'error' };
                renderOperationsConsole();
            });
    }, 5000);
}

function requestPhoneLookup(iccid) {
    const current = opsState.phoneLookups[iccid];
    if (current && current.state === 'checking') return;
    const previousPhone = opsState.phoneNumbers[iccid] || '';
    opsState.phoneLookups[iccid] = { state: 'checking' };
    renderOperationsConsole();
    $.ajax({ url: `/api/v1/modems/${encodeURIComponent(iccid)}/phone-lookup`, method: 'POST' })
        .done(function () { pollPhoneLookup(iccid, previousPhone, 0); })
        .fail(function (xhr) {
            opsState.phoneLookups[iccid] = { state: 'error', message: xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không gửi được yêu cầu đọc số' };
            renderOperationsConsole();
        });
}

// phoneLookupButton: nút "Đọc số" + dòng trạng thái inline (Hiệu chuẩn + Bảo trì).
function phoneLookupButton(iccid, online) {
    const d = describePhoneLookup(opsState.phoneLookups[iccid]);
    const button = opsElement('button', 'btn btn-sm btn-outline-secondary phone-lookup').attr({ type: 'button', title: 'Gửi USSD tra số thuê bao' })
        .html(`<i class="bi bi-telephone-plus"></i> ${d.busy ? 'Đang đọc…' : 'Đọc số'}`)
        .prop('disabled', d.busy || !online)
        .click(function (event) { event.stopPropagation(); requestPhoneLookup(iccid); });
    const note = d.tone === 'muted' && !d.busy ? null : opsElement('small', `ops-list-note keepalive-result tone-${d.tone}`, d.label);
    return { button, note };
}

function requestKeepaliveRun(item) {
    opsState.keepaliveRuns[item.iccid] = { state: 'running' };
    renderOperationsConsole();
    $.ajax({ url: '/api/v1/keepalive/run', method: 'POST', contentType: 'application/json', data: JSON.stringify({ iccid: item.iccid }) })
        .done(function (run) {
            opsState.keepaliveRuns[item.iccid] = { state: 'done', run };
            loadOperationsData();
        })
        .fail(function (xhr) {
            opsState.keepaliveRuns[item.iccid] = { state: 'error', message: xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không gửi được yêu cầu nuôi SIM' };
            renderOperationsConsole();
        });
}

function keepaliveRunLine(run) {
    if (!run) return 'Lần chạy cuối: chưa chạy';
    return `Lần chạy cuối: ${run.status}${run.reason ? ` · ${run.reason}` : ''} · ${opsDateTime(run.ran_at)}${run.target_phone ? ` → ${run.target_phone}` : ''}`;
}

function renderMaintenance() {
    const slots = opsSlotByICCID();
    const route = window.currentAppRoute ? window.currentAppRoute() : { view: 'maintenance', iccid: '' };
    const isAdmin = typeof auth !== 'undefined' && auth.role === 'admin';
    const cfg = opsState.keepalive.config || {};
    const items = opsState.keepalive.items || [];
    const keepalive = opsKeepaliveByICCID();
    const configOn = cfg.enabled === true;
    const enabled = items.filter(item => item.enabled);
    const eligible = enabled.filter(item => item.online);
    const sent = items.reduce((sum, item) => sum + Number(item.sent_this_month || 0), 0);
    const next = keepaliveNextRun(cfg.run_hour, Date.now());
    const summary = [
        { label: 'Lịch đang bật', value: enabled.length, note: `${items.length - enabled.length} SIM đang tắt`, icon: 'bi-calendar2-check', tone: 'mint' },
        { label: 'SIM đủ điều kiện', value: eligible.length, note: `${enabled.length - eligible.length} SIM bật nhưng ngoại tuyến`, icon: 'bi-sim', tone: 'blue' },
        { label: 'Ngân sách tháng', value: `${opsMoney(sent * KEEPALIVE_SMS_VND)} (ước tính)`, note: `${sent} SMS đã gửi · trần ${cfg.max_per_month ?? '—'} SMS/SIM/tháng`, icon: 'bi-wallet2', tone: 'amber' },
        { label: 'Lần chạy kế tiếp', value: cfg.run_hour === undefined ? '—' : `${String(next.getHours()).padStart(2, '0')}:00 ${opsShortDate(next)}`, note: configOn ? `Chu kỳ mặc định ${cfg.interval_days ?? '—'} ngày` : 'Công tắc tổng đang tắt', icon: 'bi-clock-history', tone: 'coral' }
    ];
    const kpis = $('#ops-maintenance-summary').empty();
    summary.forEach(item => {
        const card = opsElement('article', 'ops-kpi');
        card.append(opsElement('div', `ops-kpi-icon tone-${item.tone}`).append($('<i>').addClass(`bi ${item.icon}`)));
        const metric = opsElement('div', 'ops-kpi-body');
        metric.append(opsElement('div', 'ops-kpi-label', item.label));
        metric.append(opsElement('div', 'ops-kpi-value', String(item.value)));
        metric.append(opsElement('div', 'ops-kpi-note', item.note));
        card.append(metric);
        kpis.append(card);
    });
    $('#ops-keepalive-banner').toggleClass('d-none', cfg.enabled !== false);

    const list = $('#ops-schedule-list').empty();
    const balances = opsBalanceByICCID();
    if (!opsState.modems.length) {
        list.append(opsElement('div', 'ops-empty', 'Chưa có modem để tạo lịch duy trì.'));
        return;
    }
    opsState.modems.forEach(modem => {
        const slot = slots[modem.iccid];
        const selected = route.iccid === modem.iccid;
        const ka = keepalive[modem.iccid] || { iccid: modem.iccid, enabled: !!modem.keepalive_enabled };
        const card = opsElement('article', `schedule-card${selected ? ' is-selected' : ''}`);
        card.attr({ role: 'link', tabindex: '0' })
            .click(() => window.navigateApp('maintenance', modem.iccid))
            .on('keydown', event => { if (event.key === 'Enter' && event.target === card[0]) window.navigateApp('maintenance', modem.iccid); });
        const main = opsElement('div', 'schedule-main');
        main.append(opsElement('div', 'schedule-title', `${slot ? `Khe ${String(slot).padStart(2, '0')}` : 'Chưa gán khe'} · ${modem.port_name || modem.iccid}`));
        main.append(opsElement('div', 'ops-list-note', `${opsState.phoneNumbers[modem.iccid] || 'Chưa biết số'} · ${modem.iccid} · ${modem.operator || 'Chưa rõ nhà mạng'} · sóng ${modem.signal_strength || 0}%`));
        const status = balances[modem.iccid];
        main.append(opsElement('div', 'balance-line', `Số dư hiện tại: ${opsBalanceLabel(modem)}${modem.balance_updated_at ? ` · cập nhật ${new Date(modem.balance_updated_at).toLocaleString('vi-VN')}` : ''}${status ? ` · ${describeBalanceLevel(status).label}` : ''}`));
        if (status && (status.snapshots || []).length >= 2) main.append(opsElement('div', 'ops-list-note mono', balanceSparkline(status.snapshots)));
        const health = (opsState.simHealth || []).find(h => h.iccid === modem.iccid) || {};
        main.append(opsElement('div', 'ops-list-note', `SMS cuối: ${opsDate(health.last_sms_at)} · đăng ký mạng: ${opsDateTime(health.last_registered_at)}`));

        const rule = opsElement('div', 'schedule-rule keepalive-rule').click(event => event.stopPropagation());
        const toggleId = `ka-on-${modem.iccid}`;
        const toggle = $('<input>').attr({ type: 'checkbox', id: toggleId }).addClass('form-check-input').prop('checked', !!ka.enabled).prop('disabled', !isAdmin);
        toggle.change(function () {
            toggle.prop('disabled', true);
            patchModemProfile(modem.iccid, { keepalive_enabled: toggle.prop('checked') }).done(loadOperationsData).fail(function (xhr) {
                toggle.prop('disabled', false).prop('checked', !!ka.enabled);
                main.find('.keepalive-result').text(xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không lưu được công tắc');
            });
        });
        const interval = $('<input>').attr({ type: 'number', min: 1, step: 1, placeholder: String(cfg.interval_days ?? ''), 'aria-label': 'Chu kỳ nuôi (ngày)' }).addClass('form-control form-control-sm mono keepalive-interval').val(modem.keepalive_interval ?? '').prop('disabled', !isAdmin);
        interval.change(function () {
            const value = interval.val().trim();
            patchModemProfile(modem.iccid, { keepalive_interval: value === '' ? null : Number(value) }).done(loadOperationsData).fail(function (xhr) {
                interval.addClass('is-invalid').attr('title', xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không lưu được chu kỳ');
            });
        });
        rule.append(
            opsElement('label', 'form-check-label keepalive-toggle').attr('for', toggleId).append(toggle, document.createTextNode(' Nuôi SIM')),
            opsElement('span', '', ' · chu kỳ '), interval, opsElement('span', '', ` ngày (mặc định ${cfg.interval_days ?? '—'})`)
        );
        main.append(rule);
        main.append(opsElement('div', 'ops-list-note', `Hoạt động gần nhất: ${opsDateTime(ka.last_activity_at)} · Lần nuôi kế tiếp: ${ka.enabled && ka.next_due_at ? opsDateTime(ka.next_due_at) : '—'} · đã gửi tháng này: ${ka.sent_this_month || 0}`));
        main.append(opsElement('div', 'ops-list-note', keepaliveRunLine(ka.last_run)));
        const pending = opsState.keepaliveRuns[modem.iccid];
        let resultText = '';
        let resultTone = 'muted';
        if (pending && pending.state === 'running') resultText = 'Đang nuôi…';
        else if (pending && pending.state === 'error') { resultText = pending.message; resultTone = 'danger'; }
        else if (pending) {
            resultText = `Kết quả: ${pending.run.status}${pending.run.reason ? ` · ${pending.run.reason}` : ''}${pending.run.target_phone ? ` → ${pending.run.target_phone}` : ''}`;
            resultTone = pending.run.status === 'sent' ? 'ok' : 'danger';
        }
        main.append(opsElement('div', `ops-list-note keepalive-result tone-${resultTone}`, resultText));

        const controls = opsElement('div', 'schedule-controls');
        const d = describeKeepalive(ka, Date.now());
        controls.append(opsElement('span', `status-chip status-${d.tone === 'muted' ? 'blocked' : d.tone}`, d.label));
        const check = opsState.balanceChecks[modem.iccid] || {};
        const balanceButton = opsElement('button', 'btn btn-sm btn-outline-secondary');
        const buttonLabels = { checking: 'Đang kiểm tra…', timeout: 'Thử kiểm tra lại', error: 'Thử kiểm tra lại', done: 'Kiểm tra lại' };
        balanceButton.html(`<i class="bi bi-wallet2"></i> ${buttonLabels[check.state] || 'Kiểm tra số dư'}`);
        balanceButton.attr('type', 'button').prop('disabled', check.state === 'checking' || !opsIsOnline(modem)).click(function (event) {
            event.stopPropagation();
            window.navigateApp('maintenance', modem.iccid);
            requestBalanceCheck(modem);
        });
        controls.append(balanceButton);
        if (isAdmin) {
            const lookup = phoneLookupButton(modem.iccid, opsIsOnline(modem));
            controls.append(lookup.button);
            if (lookup.note) main.append(lookup.note);
            const runButton = opsElement('button', 'btn btn-sm btn-dark').attr('type', 'button').html('<i class="bi bi-send"></i> Nuôi ngay');
            runButton.prop('disabled', !configOn || !ka.enabled || !!(pending && pending.state === 'running'))
                .attr('title', !configOn ? 'Công tắc tổng đang tắt trong config.yaml' : !ka.enabled ? 'Bật "Nuôi SIM" trước' : '')
                .click(function (event) { event.stopPropagation(); requestKeepaliveRun(ka); });
            controls.append(runButton);
        }
        card.append(main, controls);
        list.append(card);

        const shouldAutoCheck = route.view === 'maintenance' && (selected || (opsState.modems.length === 1 && !route.iccid));
        if (shouldAutoCheck && balanceNeedsRefresh(modem) && !opsState.balanceChecks[modem.iccid] && opsIsOnline(modem)) {
            requestBalanceCheck(modem);
        }
    });
}

function renderAlertCenter() {
    $('#btn-balance-run').toggleClass('d-none', !(typeof auth !== 'undefined' && auth.role === 'admin'));
    const container = $('#ops-alert-list').empty();
    opsAlerts().forEach(alert => {
        const card = opsElement('article', `alert-card alert-${alert.level}`);
        const body = opsElement('div');
        body.append(opsElement('div', 'schedule-title', alert.title));
        body.append(opsElement('div', 'ops-list-note', alert.note));
        card.append(body);
        card.append(opsElement('span', `status-chip status-${alert.level}`, alert.level === 'danger' ? 'Khẩn cấp' : alert.level === 'ok' ? 'Ổn định' : 'Cần xử lý'));
        container.append(card);
    });
}

function renderReportPreview() {
    const summary = summarizeOpsData(opsState.modems, opsState.messages);
    const cards = [
        { label: 'Tổng SMS', value: summary.messageTotal, note: 'Trong dữ liệu hiện có', icon: 'bi-chat-square-dots', tone: 'blue' },
        { label: 'Tin nhận', value: summary.received, note: `${summary.unread} tin chưa đọc`, icon: 'bi-arrow-down-left-circle', tone: 'mint' },
        { label: 'Tin đã gửi', value: summary.sent, note: `${summary.delivered} đã giao`, icon: 'bi-send-check', tone: 'green' },
        { label: 'Gửi lỗi', value: summary.failed, note: 'Không tự động gửi lại', icon: 'bi-exclamation-octagon', tone: 'coral' }
    ];
    const kpis = $('#ops-report-kpis').empty();
    cards.forEach(item => {
        const card = opsElement('article', 'ops-kpi');
        card.append(opsElement('div', `ops-kpi-icon tone-${item.tone}`).append($('<i>').addClass(`bi ${item.icon}`)));
        const metric = opsElement('div', 'ops-kpi-body');
        metric.append(opsElement('div', 'ops-kpi-label', item.label));
        metric.append(opsElement('div', 'ops-kpi-value', String(item.value)));
        metric.append(opsElement('div', 'ops-kpi-note', item.note));
        card.append(metric);
        kpis.append(card);
    });

    const chart = $('#ops-message-chart').empty();
    chart.append(opsElement('div', 'eyebrow', 'Vòng đời tin nhắn'));
    chart.append(opsElement('h2', '', 'Trạng thái SMS'));
    const max = Math.max(summary.received, summary.sent, summary.delivered, summary.failed, 1);
    [
        ['Tin nhận', summary.received, 'received'],
        ['Đã gửi', summary.sent, 'sent'],
        ['Đã giao', summary.delivered, 'delivered'],
        ['Thất bại', summary.failed, 'failed']
    ].forEach(([label, value, status]) => {
        const row = opsElement('div', 'chart-row');
        row.append(opsElement('div', 'chart-label', label));
        const track = opsElement('div', 'chart-track');
        track.append(opsElement('div', `chart-bar bar-${status}`).css('width', `${Math.max((value / max) * 100, value ? 4 : 0)}%`));
        row.append(track, opsElement('div', 'chart-value', String(value)));
        chart.append(row);
    });

    const health = $('#ops-health-breakdown').empty();
    health.append(opsElement('div', 'eyebrow', 'Sức khỏe SIM'));
    health.append(opsElement('h2', '', 'Thiết bị đang theo dõi'));
    if (!opsState.modems.length) {
        health.append(opsElement('div', 'ops-empty mt-3', 'Chưa có modem.'));
        return;
    }
    opsState.modems.forEach(modem => {
        const row = opsElement('div', 'health-row');
        const body = opsElement('div');
        body.append(opsElement('div', 'ops-list-title', modem.port_name || modem.iccid));
        body.append(opsElement('div', 'ops-list-note', `${modem.registration || 'Chưa rõ đăng ký'} · ${modem.operator || 'Chưa rõ nhà mạng'}`));
        row.append(body, opsElement('strong', 'mono', `${modem.signal_strength || 0}%`));
        health.append(row);
    });
}

function renderAuditPreview() {
    const body = $('#ops-audit-body').empty();
    if (!opsState.messages.length && !opsState.slotEvents.length) {
        body.append($('<tr>').append($('<td>').attr('colspan', 5).append(opsElement('div', 'ops-empty', 'Chưa có sự kiện để hiển thị.'))));
        return;
    }
    opsState.messages.slice(0, 50).forEach(message => {
        const row = $('<tr>');
        row.append($('<td>').text(new Date(message.timestamp).toLocaleString('vi-VN')));
        row.append($('<td>').text(message.type === 'sent' ? 'admin' : 'system'));
        row.append($('<td>').text(message.type === 'sent' ? 'Gửi SMS' : 'Nhận SMS'));
        row.append($('<td>').text(`${message.phone || 'Không rõ số'} · ${message.iccid || 'Không rõ SIM'}`));
        row.append($('<td>').append(opsElement('span', `status-chip status-${message.type === 'sent' ? 'ready' : 'ok'}`, opsMessageStatus(message).label)));
        body.append(row);
    });
    opsState.slotEvents.slice(0, 20).forEach(event => {
        const row = $('<tr>');
        row.append($('<td>').text(new Date(event.detected_at).toLocaleString('vi-VN')));
        row.append($('<td>').text('system'));
        row.append($('<td>').text('Đổi khe'));
        row.append($('<td>').text(`${event.iccid} · ${describeSlotEvent(event).path}`));
        row.append($('<td>').append(opsElement('span', 'status-chip status-ok', event.event)));
        body.append(row);
    });
}

function renderOperationsConsole() {
    renderSimRails();
    renderOpsKPIs();
    renderMiniSlots();
    renderOpsAlertSummary();
    renderRecentMessages();
    renderLiveUnassigned();
    renderTray();
    renderBayCalibration();
    renderSlotHistory();
    renderMaintenance();
    renderAlertCenter();
    renderReportPreview();
    renderAuditPreview();
}

function loadOperationsData() {
    $('#btn-refresh-ops').prop('disabled', true);
    $.when(
        $.get('/api/v1/modems'),
        $.get('/api/v1/sms', { page: 1, limit: 200 }),
        $.get('/api/v1/bays'),
        $.get('/api/v1/balance/status'),
        $.get('/api/v1/sim-health'),
        $.get('/api/v1/keepalive/status')
    ).done(function (modemResponse, smsResponse, bayResponse, balanceResponse, healthResponse, keepaliveResponse) {
        opsState.modems = modemResponse[0] || [];
        opsState.messages = (smsResponse[0] && smsResponse[0].data) || [];
        opsState.bays = bayResponse[0] || [];
        opsState.balanceStatus = balanceResponse[0] || [];
        opsState.simHealth = healthResponse[0] || [];
        opsState.keepalive = keepaliveResponse[0] || { config: {}, items: [] };
        opsState.modems.forEach(modem => {
            if (modem.phone_number) opsState.phoneNumbers[modem.iccid] = modem.phone_number;
        });
        opsState.loadedAt = new Date();
        renderOperationsConsole();
    }).fail(function () {
        $('#ops-kpis').empty().append(opsElement('div', 'ops-empty', 'Không tải được dữ liệu vận hành. Hãy kiểm tra smsie service.'));
    }).always(function () {
        $('#btn-refresh-ops').prop('disabled', false);
    });
}

const TRAY_SLOTS = 32;
const SWAP_HIGHLIGHT_MS = 24 * 60 * 60 * 1000;

function trayTone(bay) {
    if (!bay) return 'missing';
    if (!bay.current_iccid) return 'empty';
    if (bay.status !== 'online') return 'offline';
    if ((bay.signal_strength || 0) < 20) return 'weak';
    return 'online';
}

function buildTrayCells(bays, now) {
    const bySlot = {};
    const unassigned = [];
    (bays || []).forEach(bay => {
        if (bay.slot_number) bySlot[bay.slot_number] = bay; else unassigned.push(bay);
    });
    const cells = [];
    for (let slot = 1; slot <= TRAY_SLOTS; slot += 1) {
        const bay = bySlot[slot];
        const swapped = !!(bay && bay.last_event_at && (now - new Date(bay.last_event_at).getTime()) < SWAP_HIGHLIGHT_MS);
        cells.push({ slot, bay: bay || null, tone: trayTone(bay), swapped });
    }
    return { cells, unassigned };
}

function slotEventDay(event) {
    const d = new Date(event.detected_at);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function groupSlotEventsByDay(events) {
    const sorted = (events || []).slice().sort((a, b) => new Date(b.detected_at) - new Date(a.detected_at));
    const groups = [];
    sorted.forEach(event => {
        const day = slotEventDay(event);
        let group = groups[groups.length - 1];
        if (!group || group.day !== day) {
            group = { day, events: [] };
            groups.push(group);
        }
        group.events.push(event);
    });
    return groups;
}

function slotLabel(slot) {
    return slot ? `Khe ${slot}` : 'Chưa gán khe';
}

function describeSlotEvent(event) {
    let path;
    let balanceNote;
    if (event.event === 'moved') {
        path = `${slotLabel(event.from_slot)} → ${slotLabel(event.to_slot)}`;
        balanceNote = 'số dư lúc vào khe';
    } else if (event.event === 'removed') {
        path = `${slotLabel(event.from_slot)} → rút ra`;
        balanceNote = 'số dư lúc rời khe';
    } else {
        path = slotLabel(event.to_slot);
        balanceNote = 'số dư lúc vào khe';
    }
    const hasBalance = event.balance_vnd !== null && event.balance_vnd !== undefined;
    return {
        path,
        tone: event.event,
        balance: hasBalance ? opsMoney(event.balance_vnd) : '—',
        balanceNote: hasBalance ? balanceNote : (event.event === 'removed' ? 'không đọc số dư' : 'USSD quá hạn')
    };
}

if (typeof module !== 'undefined') {
    module.exports = { balanceNeedsRefresh, buildOpsCsv, describeOpsMessageRoute, groupOpsMessages, summarizeOpsData, buildTrayCells, groupSlotEventsByDay, describeSlotEvent, bayBalanceLabel, describeBalanceLevel, balanceSparkline, describeHealthFinding, describeKeepalive, keepaliveNextRun, describePhoneLookup };
}

if (typeof window !== 'undefined' && window.jQuery) $(document).ready(function () {
    $(document).on('click', e => { if (!$(e.target).closest('.bay, #ops-tray-pop').length) $('#ops-tray-pop').prop('hidden', true); });
    $(document).on('keydown', e => { if (e.key === 'Escape') $('#ops-tray-pop').prop('hidden', true); });
    $('[data-slot-tab]').click(function () { showSlotTab($(this).data('slot-tab')); });
    $('#slot-events-from, #slot-events-to').change(function () {
        const route = window.currentAppRoute();
        if (route.view === 'slots' && route.iccid) loadSlotEvents(route.iccid);
    });
    $('#btn-export-slot-events').click(function () {
        const rows = [['detected_at', 'event', 'iccid', 'imei', 'phone_number', 'from_slot', 'to_slot', 'balance_vnd', 'port_name']];
        opsState.slotEvents.forEach(e => rows.push([e.detected_at, e.event, e.iccid, e.imei, e.phone_number || '', e.from_slot ?? '', e.to_slot ?? '', e.balance_vnd ?? '', e.port_name || '']));
        const csv = rows.map(r => r.map(v => `"${String(v).replaceAll('"', '""')}"`).join(',')).join('\r\n');
        const url = URL.createObjectURL(new Blob(['\ufeff', csv], { type: 'text/csv;charset=utf-8' }));
        const link = document.createElement('a');
        link.href = url;
        link.download = `smsie-slot-events-${new Date().toISOString().slice(0, 10)}.csv`;
        link.click();
        URL.revokeObjectURL(url);
    });
    $('[data-open-view]').click(function () {
        const view = $(this).data('open-view');
        window.navigateApp(view);
    });
    $('#btn-refresh-ops').click(loadOperationsData);
    $('#btn-balance-run').click(function () {
        const button = $(this);
        const status = $('#ops-balance-run-status');
        button.prop('disabled', true);
        status.text('Đang yêu cầu đọc số dư…');
        $.ajax({ url: '/api/v1/balance/run', method: 'POST' }).done(function (response) {
            const n = Number((response && response.requested) || 0);
            if (!n) {
                status.text('Không có SIM cần đọc (đã đọc trong 20 giờ qua hoặc ngoại tuyến).');
                return;
            }
            status.text(`Đã yêu cầu đọc ${n} SIM, đánh giá sau ~${Math.min(120, 3 * n + 25)} giây.`);
            window.setTimeout(loadOperationsData, Math.min(120000, 3000 * n + 25000));
        }).fail(function (xhr) {
            status.text(xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : 'Không gửi được yêu cầu đọc số dư.');
        }).always(function () { button.prop('disabled', false); });
    });
    $('#btn-export-preview').click(function () {
        const blob = new Blob(['\ufeff', buildOpsCsv(opsState.messages)], { type: 'text/csv;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = `smsie-report-${new Date().toISOString().slice(0, 10)}.csv`;
        link.click();
        URL.revokeObjectURL(url);
    });
    $('#btn-backup-database').click(async function () {
        const button = $(this);
        const status = $('#ops-backup-status');
        button.prop('disabled', true).text('Đang tạo backup…');
        status.text('Đang tạo snapshot SQLite nhất quán.');
        try {
            const response = await fetch('/api/v1/admin/backup', {
                headers: { Authorization: `Bearer ${auth.token}` }
            });
            if (!response.ok) throw new Error('Backup failed');
            const blob = await response.blob();
            const disposition = response.headers.get('content-disposition') || '';
            const match = disposition.match(/filename="?([^";]+)"?/i);
            const link = document.createElement('a');
            link.href = URL.createObjectURL(blob);
            link.download = match ? match[1] : `smsie-backup-${new Date().toISOString().slice(0, 10)}.db`;
            link.click();
            URL.revokeObjectURL(link.href);
            status.text('Đã tải backup về máy.');
        } catch (_) {
            status.text('Không tạo được backup. Kiểm tra quyền admin và service.');
        } finally {
            button.prop('disabled', false).html('<i class="bi bi-database-down"></i> Sao lưu dữ liệu');
        }
    });
    $('#sms-search').on('input', function () {
        renderConversationInbox(opsState.currentMessages || []);
    });
    $(document).on('smsie:route', function (_event, route) {
        renderSimRails();
        if (['overview', 'slots', 'alerts', 'reports', 'audit', 'maintenance'].includes(route.view)) {
            loadOperationsData();
        }
        if (route.view === 'slots' && route.iccid) {
            showSlotTab('history');
            loadSlotEvents(route.iccid);
        }
        if (route.view === 'audit') {
            $.get('/api/v1/slot-events', { page_size: 200 }).done(function (response) {
                opsState.slotEvents = response.data || [];
                renderAuditPreview();
            });
        }
    });
    if (auth.username && ['overview', 'slots', 'alerts', 'reports', 'audit', 'maintenance'].includes(window.currentAppRoute().view)) {
        loadOperationsData();
    }
});
