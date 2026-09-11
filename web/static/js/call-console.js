function formatCallDuration(seconds) {
    const total = Math.max(0, Math.floor(Number(seconds) || 0));
    const minutes = Math.floor(total / 60);
    return `${String(minutes).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`;
}

function chooseCallRecordingMimeType(isSupported) {
    const supports = typeof isSupported === 'function' ? isSupported : () => false;
    return [
        'audio/webm;codecs=opus',
        'audio/webm',
        'audio/ogg;codecs=opus',
        'audio/mp4'
    ].find(supports) || '';
}

function recordingDownloadName(recording) {
    const extensions = {
        'audio/webm': 'webm',
        'audio/ogg': 'ogg',
        'audio/mp4': 'm4a',
        'audio/mpeg': 'mp3',
        'audio/wav': 'wav',
        'audio/x-wav': 'wav'
    };
    const phone = String(recording && recording.phone || '').replace(/\D/g, '');
    const suffix = phone ? `-${phone}` : '';
    const extension = extensions[recording && recording.content_type] || 'webm';
    return `cuoc-goi-${recording && recording.id || 'recording'}${suffix}.${extension}`;
}

function installCallConsole() {
    const state = {
        activeICCID: '',
        activePhone: '',
        mediaRecorder: null,
        finishing: null,
        chunks: [],
        startedAt: 0,
        clockTimer: null,
        audioContext: null,
        destination: null,
        remoteSource: null,
        objectURL: '',
        modems: [],
        micEnabled: true,
        speakerEnabled: true
    };

    function setNotice(message, tone) {
        const notice = $('#call-recording-status');
        notice.removeClass('is-success is-danger is-live');
        if (tone) notice.addClass(`is-${tone}`);
        notice.text(message || '');
    }

    function setCallIdentity(modem) {
        const slot = modem && modem.slot_number ? String(modem.slot_number) : '—';
        const label = modem && (modem.phone_number || modem.name || modem.iccid) || 'Chưa chọn SIM';
        const ready = !!(modem && modem.status === 'online' && modem.call_supported);
        $('#call-selected-name').text(modem && modem.slot_number ? `SIM ${String(modem.slot_number).padStart(2, '0')}` : 'SIM');
        $('#call-selected-label').text(label);
        $('#call-selected-meta').text(modem ? `Khe ${slot} · ${modem.port_name || 'Chưa rõ cổng'} · ${modem.operator || 'Chưa rõ nhà mạng'}` : 'Chọn một modem có UAC để gọi');
        $('#call-selected-slot').text(slot);
        $('#call-selected-port').text(modem && modem.port_name || '—');
        $('#call-selected-type').text(modem && modem.call_supported ? 'UAC Modem' : 'Serial Modem');
        $('#call-selected-network').text(ready ? 'Sẵn sàng' : modem && modem.status === 'online' ? 'Chưa bật UAC' : 'Ngoại tuyến').toggleClass('is-ready', ready);
        $('#call-device-status').toggleClass('is-online', ready);
        $('#call-device-status span').text(ready ? 'Sẵn sàng' : modem && modem.status === 'online' ? 'Chưa UAC' : 'Ngoại tuyến');
    }

    function renderSimRail(modems, selectedICCID) {
        const rail = $('#call-sim-rail').empty();
        (modems || []).slice().sort((left, right) => Number(left.slot_number || 999) - Number(right.slot_number || 999)).forEach(function (modem) {
            const ready = modem.status === 'online' && modem.call_supported;
            const online = modem.status === 'online';
            const card = $('<button>').attr({ type: 'button', role: 'listitem', 'aria-pressed': modem.iccid === selectedICCID }).addClass(`call-sim-card ${ready ? 'is-ready' : online ? 'is-warning' : 'is-offline'}${modem.iccid === selectedICCID ? ' is-selected' : ''}`);
            const top = $('<span>').addClass('call-sim-card-top');
            top.append($('<strong>').text(modem.slot_number ? `SIM ${String(modem.slot_number).padStart(2, '0')}` : 'SIM'));
            top.append($('<span>').addClass(`call-sim-status ${ready ? 'is-ready' : online ? 'is-warning' : 'is-offline'}`).text(ready ? 'Sẵn sàng' : online ? 'Chưa UAC' : 'Ngoại tuyến'));
            card.append(top);
            card.append($('<span>').addClass('call-sim-phone').text(modem.phone_number || modem.name || modem.iccid));
            card.append($('<span>').addClass('call-sim-meta').text(`Khe ${modem.slot_number || '—'} · ${modem.port_name || '—'}`));
            card.click(() => window.navigateApp('calls', modem.iccid));
            rail.append(card);
        });
        if (!modems || !modems.length) rail.append($('<div>').addClass('call-sim-empty').text('Chưa có SIM nào trong modem pool.'));
    }

    function loadModems(preferredICCID) {
        return $.get('/api/v1/modems').then(function (modems) {
            const available = modems || [];
            state.modems = available;
            const select = $('#call-modem-select').empty();
            if (!available.length) {
                select.append($('<option>').val('').text('Chưa có modem'));
                select.prop('disabled', true);
                $('#call-iccid').val('');
                $('#call-panel').addClass('d-none');
                $('#call-not-ready').removeClass('d-none').text('Chưa có modem UAC sẵn sàng cho cuộc gọi WebRTC.');
                setCallIdentity(null);
                renderSimRail([], '');
                renderRecordings([]);
                return '';
            }

            available.forEach(function (modem) {
                const parts = [modem.phone_number || modem.name || modem.iccid];
                if (modem.slot_number) parts.push(`Khe ${modem.slot_number}`);
                if (modem.port_name) parts.push(modem.port_name);
                select.append($('<option>').val(modem.iccid).text(parts.join(' · ')));
            });
            select.prop('disabled', false);
            const selected = available.find(modem => modem.iccid === preferredICCID) || available[0];
            select.val(selected.iccid);
            $('#call-iccid').val(selected.iccid);
            $('#call-iccid-title').text(selected.iccid);
            setCallIdentity(selected);
            renderSimRail(available, selected.iccid);
            refreshCallStateUI(selected.iccid);
            loadRecordings(selected.iccid);
            return selected.iccid;
        }).fail(function () {
            $('#call-not-ready').removeClass('d-none').text('Không tải được danh sách modem.');
        });
    }

    function activate(preferredICCID) {
        stopCallStatePolling();
        loadModems(preferredICCID).then(function (iccid) {
            if (!iccid) return;
            callStatePollTimer = setInterval(function () {
                if (!$('#view-calls').hasClass('d-none')) refreshCallStateUI($('#call-iccid').val());
            }, 2000);
        });
    }

    function attachRemoteStream(stream) {
        if (!state.destination || !state.audioContext || !stream || state.remoteSource) return;
        try {
            state.remoteSource = state.audioContext.createMediaStreamSource(stream);
            state.remoteSource.connect(state.destination);
        } catch (_) {
            setNotice('Đang ghi micro; chưa ghép được âm thanh đầu dây bên kia.', 'danger');
        }
    }

    function startClock() {
        clearInterval(state.clockTimer);
        state.startedAt = Date.now();
        $('#call-duration').text('00:00');
        state.clockTimer = setInterval(function () {
            $('#call-duration').text(formatCallDuration((Date.now() - state.startedAt) / 1000));
        }, 1000);
    }

    function stopClock() {
        clearInterval(state.clockTimer);
        state.clockTimer = null;
    }

    async function beginRecording(iccid, phone) {
        startClock();
        state.activeICCID = iccid;
        state.activePhone = phone;
        if (window.setSmsieCallMedia) window.setSmsieCallMedia({ mic: state.micEnabled, speaker: state.speakerEnabled });
        if (!$('#call-record-enabled').is(':checked')) {
            setNotice('Cuộc gọi này không ghi âm.', '');
            return;
        }
        if (typeof MediaRecorder === 'undefined' || typeof AudioContext === 'undefined') {
            setNotice('Trình duyệt này không hỗ trợ ghi âm cuộc gọi.', 'danger');
            return;
        }

        const streams = window.getSmsieCallStreams ? window.getSmsieCallStreams() : {};
        if (!streams.localStream) {
            setNotice('Không tìm thấy luồng micro để ghi âm.', 'danger');
            return;
        }

        const mimeType = chooseCallRecordingMimeType(type => MediaRecorder.isTypeSupported(type));
        state.audioContext = new AudioContext();
        await state.audioContext.resume();
        state.destination = state.audioContext.createMediaStreamDestination();
        state.audioContext.createMediaStreamSource(streams.localStream).connect(state.destination);
        attachRemoteStream(streams.remoteStream);
        state.chunks = [];
        state.mediaRecorder = mimeType
            ? new MediaRecorder(state.destination.stream, { mimeType })
            : new MediaRecorder(state.destination.stream);
        state.mediaRecorder.ondataavailable = event => {
            if (event.data && event.data.size) state.chunks.push(event.data);
        };
        state.mediaRecorder.start(1000);
        $('#call-recording-live').removeClass('d-none');
        setNotice('Đang ghi âm hai chiều.', 'live');
    }

    function cleanupRecorder() {
        $('#call-recording-live').addClass('d-none');
        if (state.audioContext) state.audioContext.close().catch(() => {});
        state.mediaRecorder = null;
        state.audioContext = null;
        state.destination = null;
        state.remoteSource = null;
        state.chunks = [];
    }

    function uploadRecording(blob, duration) {
        const form = new FormData();
        form.append('recording', blob, `call-${Date.now()}.webm`);
        form.append('phone', state.activePhone);
        form.append('duration_seconds', String(duration));
        setNotice('Đang lưu bản ghi lên máy chủ…', '');
        return $.ajax({
            url: `/api/v1/modems/${encodeURIComponent(state.activeICCID)}/call/recordings`,
            method: 'POST',
            data: form,
            processData: false,
            contentType: false
        }).done(function () {
            setNotice('Đã lưu bản ghi trên máy chủ.', 'success');
            loadRecordings(state.activeICCID);
        }).fail(function (xhr) {
            const message = xhr.responseJSON && xhr.responseJSON.error || 'Không lưu được bản ghi.';
            setNotice(message, 'danger');
        });
    }

    function finishRecording(discard) {
        if (state.finishing) return state.finishing;
        stopClock();
        const duration = state.startedAt ? Math.max(0, Math.round((Date.now() - state.startedAt) / 1000)) : 0;
        state.startedAt = 0;
        const recorder = state.mediaRecorder;
        if (!recorder || recorder.state === 'inactive') {
            cleanupRecorder();
            return Promise.resolve();
        }
        state.finishing = new Promise(resolve => {
            recorder.onstop = function () {
                const blob = new Blob(state.chunks, { type: recorder.mimeType || 'audio/webm' });
                cleanupRecorder();
                if (discard || !blob.size) {
                    setNotice(discard ? 'Đã hủy bản ghi cuộc gọi lỗi.' : '', '');
                    state.finishing = null;
                    resolve();
                    return;
                }
                uploadRecording(blob, duration).always(function () {
                    state.finishing = null;
                    resolve();
                });
            };
            recorder.stop();
        });
        return state.finishing;
    }

    function onCallState(callState) {
        const active = callState === 'dialing' || callState === 'in_call';
        $('#call-state-badge').toggleClass('is-active', active);
        $('#call-state-badge span').text(active ? (callState === 'in_call' ? 'Đang kết nối' : 'Đang gọi') : 'Sẵn sàng');
        $('.call-info-panel').toggleClass('is-active', active);
        $('#call-status').text(active ? (callState === 'in_call' ? `Đang gọi ${state.activePhone || ''}`.trim() : 'Đang kết nối cuộc gọi') : 'Chưa có cuộc gọi nào');
        $('#call-info-helper').text(active ? 'Âm thanh đang truyền qua modem UAC.' : 'Chọn SIM, nhập số điện thoại và nhấn Gọi ngay để bắt đầu.');
        if (!active && state.startedAt) finishRecording(false);
    }

    function renderRecordings(recordings) {
        const list = $('#call-recording-list').empty();
        if (!recordings || !recordings.length) {
            list.append($('<tr>').append($('<td>').attr('colspan', 6).append($('<div>').addClass('call-recording-empty').html('<i class="bi bi-mic"></i><strong>Chưa có bản ghi</strong><span>Bản ghi cuộc gọi sẽ tự xuất hiện tại đây.</span>'))));
            return;
        }
        recordings.forEach(function (recording) {
            const modem = state.modems.find(item => item.iccid === recording.iccid) || {};
            const row = $('<tr>');
            const contact = $('<div>').addClass('call-recording-contact').append($('<span>').addClass('call-recording-avatar').html('<i class="bi bi-person-fill"></i>'));
            contact.append($('<div>').append($('<strong>').text(recording.phone || 'Không rõ số'), $('<small>').text(recording.phone ? recording.iccid : 'Không có số điện thoại')));
            const sim = $('<span>').addClass('call-recording-sim').text(modem.slot_number ? `SIM ${String(modem.slot_number).padStart(2, '0')}` : 'SIM');
            const status = $('<span>').addClass('call-recording-success').html('<i class="bi bi-circle-fill"></i> Thành công');
            const playerCell = $('<div>').addClass('call-recording-actions');
            const play = $('<button>').attr({ type: 'button', 'aria-label': `Phát bản ghi ${recording.id}` }).addClass('call-recording-play').html(`<i class="bi bi-play-fill"></i><span>0:00 / ${formatCallDuration(recording.duration_seconds)}</span>`);
            play.click(() => playRecording(recording, play));
            const download = $('<button>').attr({ type: 'button', 'aria-label': `Tải bản ghi ${recording.id}` }).addClass('call-recording-download').html('<i class="bi bi-download"></i>');
            download.click(() => downloadRecording(recording));
            playerCell.append(play, download);
            row.append($('<td>').append(contact));
            row.append($('<td>').append(sim));
            row.append($('<td>').text(new Date(recording.created_at).toLocaleString('vi-VN')));
            row.append($('<td>').text(formatCallDuration(recording.duration_seconds)));
            row.append($('<td>').append(status));
            row.append($('<td>').append(playerCell));
            list.append(row);
        });
    }

    function loadRecordings(iccid) {
        if (!iccid) return;
        $('#call-recording-list').html('<div class="call-recording-empty"><span>Đang tải bản ghi…</span></div>');
        $.get(`/api/v1/modems/${encodeURIComponent(iccid)}/call/recordings`, { page: 1, pageSize: 20 })
            .done(response => renderRecordings(response.data || []))
            .fail(() => $('#call-recording-list').html('<div class="call-recording-empty"><span>Không tải được bản ghi.</span></div>'));
    }

    async function fetchRecordingBlob(recording) {
        const response = await fetch(`/api/v1/modems/${encodeURIComponent(recording.iccid)}/call/recordings/${recording.id}/file`, {
            headers: { Authorization: `Bearer ${auth.token}` }
        });
        if (!response.ok) throw new Error('Không tải được bản ghi');
        return response.blob();
    }

    async function playRecording(recording, button) {
        try {
            button.prop('disabled', true).html('<span class="spinner-border spinner-border-sm"></span>');
            const blob = await fetchRecordingBlob(recording);
            if (state.objectURL) URL.revokeObjectURL(state.objectURL);
            state.objectURL = URL.createObjectURL(blob);
            const audio = document.getElementById('call-recording-player');
            audio.src = state.objectURL;
            audio.play();
        } catch (error) {
            setNotice(error.message, 'danger');
        } finally {
            button.prop('disabled', false).html('<i class="bi bi-play-fill"></i>');
        }
    }

    async function downloadRecording(recording) {
        try {
            const blob = await fetchRecordingBlob(recording);
            const url = URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = url;
            link.download = recordingDownloadName(recording);
            link.click();
            URL.revokeObjectURL(url);
        } catch (error) {
            setNotice(error.message, 'danger');
        }
    }

    $(document).on('change', '#call-modem-select', function () {
        const iccid = $(this).val();
        window.navigateApp('calls', iccid);
    });
    $(document).on('click', '#call-clear-number', function () { $('#call-phone').val('').trigger('focus'); });
    $(document).on('click', '#call-mic-toggle', function () {
        state.micEnabled = !state.micEnabled;
        $(this).toggleClass('is-on', state.micEnabled).attr('aria-pressed', state.micEnabled).find('small').text(state.micEnabled ? 'Bật' : 'Tắt');
        if (window.setSmsieCallMedia) window.setSmsieCallMedia({ mic: state.micEnabled });
    });
    $(document).on('click', '#call-speaker-toggle', function () {
        state.speakerEnabled = !state.speakerEnabled;
        $(this).toggleClass('is-on', state.speakerEnabled).attr('aria-pressed', state.speakerEnabled).find('small').text(state.speakerEnabled ? 'Bật' : 'Tắt');
        if (window.setSmsieCallMedia) window.setSmsieCallMedia({ speaker: state.speakerEnabled });
    });
    $(document).on('change', '#call-record-enabled', function () {
        $('#call-record-label').text(this.checked ? 'Bật' : 'Tắt');
        $('.call-record-button').toggleClass('is-on', this.checked);
    });

    return { activate, attachRemoteStream, beginRecording, finishRecording, loadRecordings, onCallState };
}

if (typeof window !== 'undefined') {
    const helpers = { chooseCallRecordingMimeType, formatCallDuration, recordingDownloadName };
    window.smsieCallConsole = window.jQuery ? Object.assign(helpers, installCallConsole()) : helpers;
}
if (typeof module !== 'undefined') {
    module.exports = { chooseCallRecordingMimeType, formatCallDuration, recordingDownloadName };
}
