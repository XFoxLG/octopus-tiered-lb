import assert from 'node:assert/strict';
import test from 'node:test';

import type { KeyModelResult } from '@/api/endpoints/channel';

import {
    applyPerKeyModelFill,
    maskChannelKeySecret,
    matchPerKeyResultToFormKey,
    planPerKeyModelFill,
    type PerKeyFillKey,
} from './key-model-fill.ts';

function result(overrides: Partial<KeyModelResult> = {}): KeyModelResult {
    return {
        models: [],
        status_code: 200,
        passed: true,
        ...overrides,
    };
}

test('maskChannelKeySecret mirrors the backend maskSecret rule', () => {
    assert.equal(maskChannelKeySecret(''), '');
    assert.equal(maskChannelKeySecret('  '), '');
    assert.equal(maskChannelKeySecret('sk12345'), 'sk12345');
    assert.equal(maskChannelKeySecret('sk123456'), 'sk123456');
    assert.equal(maskChannelKeySecret('sk1234567'), 'sk12...4567');
    assert.equal(maskChannelKeySecret('sk-abcdefgh12345678'), 'sk-a...5678');
    assert.equal(maskChannelKeySecret('  sk-abcdefgh12345678  '), 'sk-a...5678');
});

test('matchPerKeyResultToFormKey prefers key_id over masked and remark', () => {
    const keys: PerKeyFillKey[] = [
        { id: 10, channel_key: 'sk-aaaa1111', remark: 'first' },
        { id: 20, channel_key: 'sk-bbbb2222', remark: 'second' },
    ];

    const hit = matchPerKeyResultToFormKey(
        result({ key_id: 20, key_masked: 'sk-a...1111', key_remark: 'first' }),
        keys,
    );

    assert.deepEqual(hit, { keyIndex: 1, matchedBy: 'id' });
});

test('matchPerKeyResultToFormKey falls back to masked key for unsaved keys without id', () => {
    const keys: PerKeyFillKey[] = [
        { channel_key: 'sk-aaaa1111', remark: 'first' },
        { channel_key: 'sk-bbbb2222', remark: 'second' },
    ];

    const hit = matchPerKeyResultToFormKey(
        result({ key_id: 0, key_masked: 'sk-b...2222' }),
        keys,
    );

    assert.deepEqual(hit, { keyIndex: 1, matchedBy: 'masked' });
});

test('matchPerKeyResultToFormKey disambiguates same-masked keys by remark', () => {
    // 前后 4 位相同的两个 key 脱敏后一致，只能靠备注区分
    const keys: PerKeyFillKey[] = [
        { id: 1, channel_key: 'sk-aaaaMIDDLE-ONE1111', remark: 'one' },
        { id: 2, channel_key: 'sk-aaaaMIDDLE-TWO1111', remark: 'two' },
    ];
    const shared = maskChannelKeySecret(keys[1].channel_key);
    assert.equal(shared, maskChannelKeySecret(keys[0].channel_key));

    const hit = matchPerKeyResultToFormKey(
        result({ key_masked: shared, key_remark: 'two' }),
        keys,
    );

    assert.deepEqual(hit, { keyIndex: 1, matchedBy: 'masked' });
});

test('matchPerKeyResultToFormKey falls back to remark when masked key is missing', () => {
    const keys: PerKeyFillKey[] = [
        { id: 1, channel_key: 'sk-aaaa1111', remark: 'first' },
        { id: 2, channel_key: 'sk-bbbb2222', remark: 'second' },
    ];

    const hit = matchPerKeyResultToFormKey(result({ key_remark: 'second' }), keys);

    assert.deepEqual(hit, { keyIndex: 1, matchedBy: 'remark' });
});

test('matchPerKeyResultToFormKey returns null when nothing matches', () => {
    const keys: PerKeyFillKey[] = [{ id: 1, channel_key: 'sk-aaaa1111', remark: 'first' }];

    assert.deepEqual(
        matchPerKeyResultToFormKey(result({ key_id: 99, key_masked: 'sk-z...9999' }), keys),
        { keyIndex: null, matchedBy: null },
    );
});

test('matchPerKeyResultToFormKey skips indexes already used by this batch', () => {
    const keys: PerKeyFillKey[] = [
        { id: 1, channel_key: 'sk-aaaa1111', remark: 'first' },
        { id: 2, channel_key: 'sk-bbbb2222', remark: 'second' },
    ];

    const hit = matchPerKeyResultToFormKey(result({ key_id: 1 }), keys, new Set([0]));

    assert.deepEqual(hit, { keyIndex: null, matchedBy: null });
});

test('planPerKeyModelFill joins models and skips failed keys', () => {
    const keys: PerKeyFillKey[] = [
        { id: 1, channel_key: 'sk-aaaa1111', remark: 'first', supported_models: 'old-model' },
        { id: 2, channel_key: 'sk-bbbb2222', remark: 'second', supported_models: 'keep-me' },
    ];

    const plan = planPerKeyModelFill(
        [
            result({ key_id: 1, models: ['gpt-4o', ' gpt-4.1 ', ''] }),
            result({ key_id: 2, passed: false, status_code: 401, message: 'unauthorized' }),
        ],
        keys,
    );

    assert.deepEqual(plan.fills, [{ keyIndex: 0, matchedBy: 'id', supportedModels: 'gpt-4o,gpt-4.1' }]);
    assert.equal(plan.skippedFailed.length, 1);
    assert.equal(plan.skippedFailed[0].result.key_id, 2);
    assert.equal(plan.skippedEmpty.length, 0);
    assert.equal(plan.unmatched.length, 0);
});

test('planPerKeyModelFill never clears an existing restriction with an empty model list', () => {
    const keys: PerKeyFillKey[] = [
        { id: 1, channel_key: 'sk-aaaa1111', remark: 'first', supported_models: 'old-model' },
    ];

    const plan = planPerKeyModelFill(
        [result({ key_id: 1, passed: true, models: ['', '   '] })],
        keys,
    );

    assert.deepEqual(plan.fills, []);
    assert.equal(plan.skippedEmpty.length, 1);
    assert.equal(plan.skippedEmpty[0].result.key_id, 1);
    // 不产生任何写入 → 已有的 supported_models 不会被清空
    assert.equal(applyPerKeyModelFill(keys, plan.fills), keys);
});

test('planPerKeyModelFill reports unmatched results separately', () => {
    const keys: PerKeyFillKey[] = [{ id: 1, channel_key: 'sk-aaaa1111', remark: 'first' }];

    const plan = planPerKeyModelFill(
        [
            result({ key_id: 1, models: ['gpt-4o'] }),
            result({ key_masked: 'sk-z...9999', models: ['gpt-4o'] }),
        ],
        keys,
    );

    assert.deepEqual(plan.fills, [{ keyIndex: 0, matchedBy: 'id', supportedModels: 'gpt-4o' }]);
    assert.equal(plan.unmatched.length, 1);
    assert.equal(plan.unmatched[0].resultIndex, 1);
});

test('planPerKeyModelFill never writes two results into the same form key', () => {
    const keys: PerKeyFillKey[] = [{ id: 1, channel_key: 'sk-aaaa1111', remark: 'first' }];

    const plan = planPerKeyModelFill(
        [
            result({ key_id: 1, models: ['gpt-4o'] }),
            result({ key_id: 1, key_masked: 'sk-a...1111', models: ['gpt-4.1'] }),
        ],
        keys,
    );

    assert.deepEqual(plan.fills, [{ keyIndex: 0, matchedBy: 'id', supportedModels: 'gpt-4o' }]);
    assert.equal(plan.unmatched.length, 1);
});

test('planPerKeyModelFill can include failed keys when explicitly asked', () => {
    const keys: PerKeyFillKey[] = [{ id: 1, channel_key: 'sk-aaaa1111', remark: 'first' }];

    const plan = planPerKeyModelFill(
        [result({ key_id: 1, passed: false, models: ['gpt-4o'] })],
        keys,
        { includeFailed: true },
    );

    assert.deepEqual(plan.fills, [{ keyIndex: 0, matchedBy: 'id', supportedModels: 'gpt-4o' }]);
    assert.equal(plan.skippedFailed.length, 0);
});

test('applyPerKeyModelFill returns new objects and leaves untouched keys alone', () => {
    const keys: PerKeyFillKey[] = [
        { id: 1, channel_key: 'sk-aaaa1111', supported_models: 'old' },
        { id: 2, channel_key: 'sk-bbbb2222', supported_models: 'keep' },
    ];

    const next = applyPerKeyModelFill(keys, [{ keyIndex: 0, supportedModels: 'a,b' }]);

    assert.notEqual(next, keys);
    assert.notEqual(next[0], keys[0]);
    assert.equal(next[0].supported_models, 'a,b');
    assert.equal(next[0].channel_key, 'sk-aaaa1111');
    assert.equal(next[1], keys[1]);
    assert.equal(keys[0].supported_models, 'old');
});

test('applyPerKeyModelFill ignores out-of-range indexes and empty plans', () => {
    const keys: PerKeyFillKey[] = [{ id: 1, channel_key: 'sk-aaaa1111' }];

    assert.equal(applyPerKeyModelFill(keys, []), keys);
    assert.equal(applyPerKeyModelFill(keys, [{ keyIndex: 5, supportedModels: 'a' }]), keys);
    assert.equal(applyPerKeyModelFill(keys, [{ keyIndex: -1, supportedModels: 'a' }]), keys);
});
