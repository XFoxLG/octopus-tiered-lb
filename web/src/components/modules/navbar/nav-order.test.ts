import assert from 'node:assert/strict';
import test from 'node:test';
import { DEFAULT_NAV_ORDER, normalizeNavOrder, parseNavOrder, parseNavVisible } from './nav-order.ts';

test('normalizeNavOrder drops unknown ids and appends missing defaults', () => {
    const got = normalizeNavOrder(['group', 'group', 'unknown', 'setting'], DEFAULT_NAV_ORDER);
    assert.deepEqual(got, ['group', 'setting', 'home', 'channel', 'model', 'analytics', 'log', 'notification', 'ops', 'apikey']);
});

test('normalizeNavOrder preserves default order when input is empty', () => {
    assert.deepEqual(normalizeNavOrder([], DEFAULT_NAV_ORDER), DEFAULT_NAV_ORDER);
});

test('normalizeNavOrder trims items before filtering and de-duplicating', () => {
    const got = normalizeNavOrder([' group ', ' ', 'setting', ' setting '], DEFAULT_NAV_ORDER);
    assert.deepEqual(got, ['group', 'setting', 'home', 'channel', 'model', 'analytics', 'log', 'notification', 'ops', 'apikey']);
});

test('persisted navigation drops the retired user page without losing other preferences', () => {
    const savedNavigation = JSON.stringify(['user', 'group', 'setting', 'user']);
    assert.deepEqual(parseNavOrder(savedNavigation), [
        'group', 'setting', 'home', 'channel', 'model', 'analytics', 'log', 'notification', 'ops', 'apikey',
    ]);
    assert.deepEqual(parseNavVisible(savedNavigation), ['group', 'setting']);
    assert.deepEqual(parseNavVisible('["user"]'), DEFAULT_NAV_ORDER);
});
