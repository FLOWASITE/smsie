const test = require('node:test');
const assert = require('node:assert/strict');

const { groupOpsMessages } = require('./operations-console.js');

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
