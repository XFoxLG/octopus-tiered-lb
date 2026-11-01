import assert from 'node:assert/strict';
import test from 'node:test';
import { buildAvailabilityRows } from './availability-results.ts';

test('availability rows keep pending members while a full check is running', () => {
    const members = [
        { id: '1:alpha', channel_id: 1, channel_name: 'Primary', name: 'alpha', enabled: true },
        { id: '2:beta', channel_id: 2, channel_name: 'Backup', name: 'beta', enabled: true },
    ];
    const rows = buildAvailabilityRows(members, [{
        client_id: '1:alpha', item_id: 1, channel_id: 1, channel_name: 'Primary', model_name: 'alpha',
        passed: true, attempts: 1, status_code: 200,
    }], true);
    assert.deepEqual(rows.map((row) => row.status), ['passed', 'testing']);
});

test('availability rows match results by client id before model and channel fallback', () => {
    const members = [{ id: 'draft-id', channel_id: 3, channel_name: 'Shared', name: 'gamma', enabled: true }];
    const result = { client_id: 'draft-id', item_id: 9, channel_id: 99, channel_name: 'Other', model_name: 'other', passed: false, attempts: 3, status_code: 502 };
    const rows = buildAvailabilityRows(members, [result], false);
    assert.equal(rows[0].status, 'failed');
    assert.equal(rows[0].result?.status_code, 502);
});


test('completed results remain visible when the tested members have been removed', () => {
    const results = [
        { client_id: '1:alpha', item_id: 1, channel_id: 1, channel_name: 'Primary', model_name: 'alpha', passed: false, attempts: 1, status_code: 400 },
        { client_id: '2:beta', item_id: 2, channel_id: 2, channel_name: 'Backup', model_name: 'beta', passed: false, attempts: 1, status_code: 503 },
    ];
    const members: Parameters<typeof buildAvailabilityRows>[0] = [];
    const rows = buildAvailabilityRows(members, results, false);
    assert.equal(rows.length, 2);
    assert.deepEqual(rows.map(row => row.modelName), ['alpha', 'beta']);
    assert.deepEqual(rows.map(row => row.status), ['failed', 'failed']);
    assert.deepEqual(rows.map(row => row.result?.status_code), [400, 503]);
    assert.equal(members.length, 0, 'displaying historical results must not restore group members');
});

test('current members and historical results do not duplicate matched test rows', () => {
    const members = [{ id: 'draft-id', channel_id: 3, channel_name: 'Current', name: 'gamma', enabled: true }];
    const results = [
        { client_id: 'draft-id', item_id: 3, channel_id: 3, channel_name: 'Current', model_name: 'gamma', passed: true, attempts: 1, status_code: 200 },
        { client_id: 'removed-id', item_id: 4, channel_id: 4, channel_name: 'Removed', model_name: 'old', passed: false, attempts: 1, status_code: 400 },
    ];
    const rows = buildAvailabilityRows(members, results, false);
    assert.deepEqual(rows.map(row => row.modelName), ['gamma', 'old']);
    assert.equal(rows.filter(row => row.result === results[0]).length, 1);
    assert.equal(members.length, 1);
});
