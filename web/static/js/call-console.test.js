const test = require('node:test');
const assert = require('node:assert/strict');

const { chooseCallRecordingMimeType, formatCallDuration, recordingDownloadName } = require('./call-console.js');

test('call duration uses a stable mm:ss clock', () => {
    assert.equal(formatCallDuration(0), '00:00');
    assert.equal(formatCallDuration(65), '01:05');
    assert.equal(formatCallDuration(3605), '60:05');
});

test('recorder prefers opus webm and falls back to a supported audio type', () => {
    const supported = new Set(['audio/ogg;codecs=opus', 'audio/webm']);
    assert.equal(chooseCallRecordingMimeType(type => supported.has(type)), 'audio/webm');
    assert.equal(chooseCallRecordingMimeType(type => type === 'audio/ogg;codecs=opus'), 'audio/ogg;codecs=opus');
    assert.equal(chooseCallRecordingMimeType(() => false), '');
});

test('recording download names include the call id and safe phone digits', () => {
    assert.equal(recordingDownloadName({ id: 18, phone: '+84 901-234-567', content_type: 'audio/webm' }), 'cuoc-goi-18-84901234567.webm');
});
