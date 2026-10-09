import test from 'node:test';
import assert from 'node:assert/strict';

import { resolveReachEndTrigger } from './reach-end.ts';

test('reach-end triggers once for the current list tail', () => {
    const result = resolveReachEndTrigger({
        enabled: true,
        itemRowCount: 10,
        lastVirtualIndex: 9,
        reachEndOffset: 2,
        lastItemKey: 'log-10',
        triggeredKey: null,
    });
    assert.deepEqual(result, { shouldTrigger: true, nextTriggeredKey: 'log-10' });
});

test('reach-end does not fire repeatedly for the same tail', () => {
    const result = resolveReachEndTrigger({
        enabled: true,
        itemRowCount: 10,
        lastVirtualIndex: 9,
        reachEndOffset: 2,
        lastItemKey: 'log-10',
        triggeredKey: 'log-10',
    });
    assert.deepEqual(result, { shouldTrigger: false, nextTriggeredKey: 'log-10' });
});

test('reach-end re-arms after a new page appends a new tail', () => {
    const result = resolveReachEndTrigger({
        enabled: true,
        itemRowCount: 20,
        lastVirtualIndex: 18,
        reachEndOffset: 2,
        lastItemKey: 'log-20',
        triggeredKey: 'log-10',
    });
    assert.deepEqual(result, { shouldTrigger: true, nextTriggeredKey: 'log-20' });
});

test('reach-end resets when the viewport moves away from the tail', () => {
    const result = resolveReachEndTrigger({
        enabled: true,
        itemRowCount: 20,
        lastVirtualIndex: 10,
        reachEndOffset: 2,
        lastItemKey: 'log-20',
        triggeredKey: 'log-10',
    });
    assert.deepEqual(result, { shouldTrigger: false, nextTriggeredKey: null });
});

test('reach-end resets while disabled or empty', () => {
    assert.deepEqual(
        resolveReachEndTrigger({
            enabled: false,
            itemRowCount: 10,
            lastVirtualIndex: 9,
            reachEndOffset: 2,
            lastItemKey: 'log-10',
            triggeredKey: 'log-10',
        }),
        { shouldTrigger: false, nextTriggeredKey: null },
    );
    assert.deepEqual(
        resolveReachEndTrigger({
            enabled: true,
            itemRowCount: 0,
            lastVirtualIndex: -1,
            reachEndOffset: 2,
            lastItemKey: 'log-10',
            triggeredKey: 'log-10',
        }),
        { shouldTrigger: false, nextTriggeredKey: null },
    );
});
