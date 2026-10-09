import assert from 'node:assert/strict';
import test from 'node:test';

import { parseServicesJSON, serializeServicesRows } from './ai-route-services.ts';

test('parseServicesJSON accepts empty and empty-array raw values', () => {
    assert.deepEqual(parseServicesJSON(''), { rows: [], invalid: false });
    assert.deepEqual(parseServicesJSON('[]'), { rows: [], invalid: false });
    assert.deepEqual(parseServicesJSON('  []  '), { rows: [], invalid: false });
});

test('parseServicesJSON rejects non-array and malformed JSON with invalid flag', () => {
    assert.equal(parseServicesJSON('{"base_url":"https://example.com"}').invalid, true);
    assert.equal(parseServicesJSON('[{"base_url":').invalid, true);
    assert.equal(parseServicesJSON('not json').invalid, true);
});

test('parseServicesJSON maps backend snake_case fields into rows and defaults enabled', () => {
    const { rows, invalid } = parseServicesJSON(
        '[{"name":"a","base_url":"https://a.com","api_key":"sk-1","model":"m","enabled":false},{"base_url":"https://b.com","api_key":"sk-2","model":"m2"}]',
    );

    assert.equal(invalid, false);
    assert.ok(rows);
    assert.equal(rows.length, 2);
    assert.equal(rows[0].name, 'a');
    assert.equal(rows[0].baseUrl, 'https://a.com');
    assert.equal(rows[0].apiKey, 'sk-1');
    assert.equal(rows[0].model, 'm');
    assert.equal(rows[0].enabled, false);
    // enabled 缺省时视为 true（对齐后端 IsEnabled 语义）。
    assert.equal(rows[1].enabled, true);
    assert.equal(rows[1].name, '');
});

test('parseServicesJSON tolerates non-object array entries with default row values', () => {
    const { rows, invalid } = parseServicesJSON('[1, null]');

    assert.equal(invalid, false);
    assert.ok(rows);
    assert.equal(rows.length, 2);
    assert.equal(rows[0].baseUrl, '');
    assert.equal(rows[1].enabled, true);
});

test('serializeServicesRows emits backend JSON shape and trims values', () => {
    const json = serializeServicesRows([
        { name: '  pool-a  ', baseUrl: ' https://a.com ', apiKey: ' sk-1 ', model: ' m ', enabled: false },
    ]);

    assert.deepEqual(JSON.parse(json), [
        { name: 'pool-a', base_url: 'https://a.com', api_key: 'sk-1', model: 'm', enabled: false },
    ]);
});

test('serializeServicesRows omits blank names and returns [] for empty pool', () => {
    const json = serializeServicesRows([
        { name: '   ', baseUrl: 'https://a.com', apiKey: 'sk-1', model: 'm', enabled: true },
    ]);
    assert.deepEqual(JSON.parse(json), [{ base_url: 'https://a.com', api_key: 'sk-1', model: 'm', enabled: true }]);

    assert.equal(serializeServicesRows([]), '[]');
});

test('parse and serialize round-trip keeps data intact', () => {
    const rows = [
        { name: 'a', baseUrl: 'https://a.com', apiKey: 'sk-1', model: 'm1', enabled: true },
        { name: '', baseUrl: 'https://b.com', apiKey: 'sk-2', model: 'm2', enabled: false },
    ];
    const parsed = parseServicesJSON(serializeServicesRows(rows));
    assert.equal(parsed.invalid, false);
    assert.deepEqual(parsed.rows, rows);
});
