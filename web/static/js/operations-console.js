const opsState = {
    modems: [],
    messages: [],
    previewMappings: {},
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

function opsElement(tag, className, text) {
    const element = $(`<${tag}>`);
    if (className) element.addClass(className);
    if (text !== undefined) element.text(text);
    return element;
}

function opsIsOnline(modem) {
    return modem && String(modem.status || '').toLowerCase() === 'online';
}

function opsSignalLabel(signal) {
    const value = Number(signal || 0);
    if (value >= 70) return 'Tốt';
    if (value >= 40) return 'Trung bình';
    if (value > 0) return 'Yếu';
    return 'Không có';
}

function opsMappedModems() {
    return opsState.modems.filter(modem => opsState.previewMappings[modem.iccid]);
}

function opsUnassignedModems() {
    return opsState.modems.filter(modem => !opsState.previewMappings[modem.iccid]);
}

function opsAlerts() {
    const alerts = [];
    const unassigned = opsUnassignedModems();
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
    if (!alerts.length) {
        alerts.push({ level: 'ok', title: 'Không có cảnh báo', note: 'Các modem đã gán đang hoạt động bình thường.' });
    }
    return alerts;
}

function renderOpsKPIs() {
    const online = opsState.modems.filter(opsIsOnline).length;
    const mapped = opsMappedModems().length;
    const unread = opsState.messages.filter(message => message.type === 'received' && !message.is_read).length;
    const sent = opsState.messages.filter(message => message.type === 'sent').length;
    const cards = [
        { label: 'Modem trực tuyến', value: `${online}/${opsState.modems.length || 0}`, note: `${32 - mapped} khe chưa có mapping` },
        { label: 'Đã gán khe', value: `${mapped}/32`, note: mapped ? 'Mapping preview trong phiên này' : 'Chưa có mapping vật lý' },
        { label: 'Tin chưa đọc', value: unread, note: `${opsState.messages.length} tin trong lịch sử` },
        { label: 'Tin đã gửi', value: sent, note: 'Chỉ tính từ khi bật lưu lịch sử' }
    ];
    const container = $('#ops-kpis').empty();
    cards.forEach(card => {
        const item = opsElement('article', 'ops-kpi');
        item.append(opsElement('div', 'ops-kpi-label', card.label));
        item.append(opsElement('div', 'ops-kpi-value', String(card.value)));
        item.append(opsElement('div', 'ops-kpi-note', card.note));
        container.append(item);
    });
}

function renderMiniSlots() {
    const mappedSlots = new Set(Object.values(opsState.previewMappings).map(Number));
    const grid = $('#ops-mini-slots').empty();
    for (let slot = 1; slot <= 32; slot += 1) {
        const item = opsElement('div', `mini-slot${mappedSlots.has(slot) ? ' is-online' : ''}`, String(slot).padStart(2, '0'));
        item.attr('title', mappedSlots.has(slot) ? `Khe ${slot}: đã gán` : `Khe ${slot}: chưa gán`);
        grid.append(item);
    }

    const strip = $('#ops-unassigned').empty();
    const unassigned = opsUnassignedModems();
    if (!unassigned.length) {
        strip.append(opsElement('div', 'ops-list-note', 'Tất cả modem đã được gán khe trong bản preview.'));
        return;
    }
    strip.append(opsElement('div', 'ops-list-title', 'Thiết bị trực tuyến chưa gán'));
    unassigned.forEach(modem => {
        strip.append(opsElement('div', 'ops-list-note', `${modem.port_name || 'Chưa rõ COM'} · ${modem.iccid} · sóng ${modem.signal_strength || 0}%`));
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
    identity.append(opsElement('div', 'eyebrow', 'Hội thoại SMS'));
    identity.append(opsElement('h2', '', thread.phone));
    identity.append(opsElement('div', 'ops-list-note', `${thread.messages.length} tin nhắn`));
    header.append(identity);

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
        const meta = opsElement('div', 'message-meta');
        const status = opsMessageStatus(message);
        meta.append(opsElement('span', `message-status status-${status.className}`, status.label));
        meta.append(opsElement('time', '', new Date(message.timestamp).toLocaleString('vi-VN')));
        bubble.append(meta);
        timeline.append(bubble);
    });
    container.append(timeline);
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
        const top = opsElement('div', 'conversation-contact-top');
        top.append(opsElement('strong', '', thread.phone));
        top.append(opsElement('time', '', new Date(thread.latest.timestamp).toLocaleDateString('vi-VN')));
        item.append(top);
        item.append(opsElement('div', 'conversation-preview', thread.latest.content || 'Không có nội dung'));
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
    const header = opsElement('div', 'ops-panel-head');
    const heading = opsElement('div');
    heading.append(opsElement('div', 'eyebrow', 'Thiết bị phát hiện qua USB'));
    heading.append(opsElement('h2', '', 'Chưa gán khe vật lý'));
    header.append(heading);
    container.append(header);

    const unassigned = opsUnassignedModems();
    if (!unassigned.length) {
        container.append(opsElement('div', 'ops-empty', 'Không còn thiết bị chờ gán.'));
        return;
    }
    unassigned.forEach(modem => {
        const row = opsElement('div', 'unassigned-device');
        const identity = opsElement('div');
        identity.append(opsElement('strong', '', modem.port_name || 'COM chưa rõ'));
        identity.append(opsElement('div', 'ops-list-note', `${modem.iccid} · IMEI ${modem.imei || 'chưa đọc được'}`));
        row.append(identity);

        const select = $('<select>').addClass('form-select form-select-sm preview-slot-select').attr('aria-label', `Chọn khe cho ${modem.port_name || modem.iccid}`);
        select.append($('<option>').val('').text('Chọn khe…'));
        for (let slot = 1; slot <= 32; slot += 1) {
            select.append($('<option>').val(slot).text(`Khe ${String(slot).padStart(2, '0')}`));
        }
        const button = opsElement('button', 'btn btn-sm btn-dark', 'Gán thử');
        button.attr('type', 'button').click(function () {
            const slot = Number(select.val());
            if (!slot) return;
            opsState.previewMappings[modem.iccid] = slot;
            renderOperationsConsole();
        });
        row.append(opsElement('div', 'unassigned-actions').append(select, button));
        container.append(row);
    });
}

function renderSlotGrid() {
    const bySlot = {};
    opsState.modems.forEach(modem => {
        const slot = opsState.previewMappings[modem.iccid];
        if (slot) bySlot[slot] = modem;
    });
    const grid = $('#ops-slot-grid').empty();
    for (let slot = 1; slot <= 32; slot += 1) {
        const modem = bySlot[slot];
        const card = opsElement('article', `slot-card${modem ? ' has-modem' : ''}`);
        const top = opsElement('div', 'd-flex justify-content-between align-items-center');
        top.append(opsElement('span', 'slot-number', `KHE ${String(slot).padStart(2, '0')}`));
        if (modem) top.append(opsElement('span', 'health-dot is-online', 'Online'));
        card.append(top);
        card.append(opsElement('div', 'slot-state', modem ? (modem.name || modem.port_name || modem.iccid) : 'Chưa gán SIM'));
        card.append(opsElement('div', 'slot-meta', modem ? `${modem.port_name} · ${opsSignalLabel(modem.signal_strength)} ${modem.signal_strength || 0}%` : 'Sẵn sàng nhận mapping'));
        if (modem) {
            const reset = opsElement('button', 'text-action mt-2', 'Bỏ mapping preview');
            reset.attr('type', 'button').click(function () {
                delete opsState.previewMappings[modem.iccid];
                renderOperationsConsole();
            });
            card.append(reset);
        }
        grid.append(card);
    }
}

function renderOperationsConsole() {
    renderOpsKPIs();
    renderMiniSlots();
    renderOpsAlertSummary();
    renderRecentMessages();
    renderLiveUnassigned();
    renderSlotGrid();
}

function loadOperationsData() {
    $('#btn-refresh-ops').prop('disabled', true);
    $.when(
        $.get('/api/v1/modems'),
        $.get('/api/v1/sms', { page: 1, limit: 200 })
    ).done(function (modemResponse, smsResponse) {
        opsState.modems = modemResponse[0] || [];
        opsState.messages = (smsResponse[0] && smsResponse[0].data) || [];
        opsState.loadedAt = new Date();
        renderOperationsConsole();
    }).fail(function () {
        $('#ops-kpis').empty().append(opsElement('div', 'ops-empty', 'Không tải được dữ liệu vận hành. Hãy kiểm tra smsie service.'));
    }).always(function () {
        $('#btn-refresh-ops').prop('disabled', false);
    });
}

if (typeof module !== 'undefined') {
    module.exports = { groupOpsMessages };
}

if (typeof window !== 'undefined' && window.jQuery) $(document).ready(function () {
    $('[data-open-view]').click(function () {
        const view = $(this).data('open-view');
        $(`#nav-${view}`).trigger('click');
    });
    $('#btn-refresh-ops').click(loadOperationsData);
    $('#sms-search').on('input', function () {
        renderConversationInbox(opsState.currentMessages || []);
    });
    $('.nav-link').click(function () {
        const id = $(this).attr('id');
        if (id === 'nav-overview' || id === 'nav-slots' || id === 'nav-alerts' || id === 'nav-reports' || id === 'nav-audit' || id === 'nav-maintenance') {
            loadOperationsData();
        }
    });
    loadOperationsData();
});
