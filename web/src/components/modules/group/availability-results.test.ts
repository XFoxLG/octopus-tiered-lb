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
