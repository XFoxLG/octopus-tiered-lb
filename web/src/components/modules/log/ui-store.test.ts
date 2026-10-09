import test from 'node:test';
import assert from 'node:assert/strict';

import {
    COMPACT_LOG_FIELD_VISIBILITY,
    DEFAULT_LOG_FIELD_VISIBILITY,
    LOG_FIELD_VISIBILITY_STORAGE_VERSION,
    migrateLogFieldVisibility,
} from './ui-store.ts';

test('log field visibility defaults to the full view and keeps compact opt-in', () => {
    assert.equal(DEFAULT_LOG_FIELD_VISIBILITY.clientIP, true);
    assert.equal(DEFAULT_LOG_FIELD_VISIBILITY.tps, true);
    assert.equal(DEFAULT_LOG_FIELD_VISIBILITY.reasoningTokens, true);
    assert.equal(COMPACT_LOG_FIELD_VISIBILITY.clientIP, false);
    assert.equal(LOG_FIELD_VISIBILITY_STORAGE_VERSION, 1);
});

test('migrating a version-0 record turns the old compact default off once', () => {
    const migrated = migrateLogFieldVisibility(
        { compact: true, visibility: { ...DEFAULT_LOG_FIELD_VISIBILITY, tps: false } },
        0,
    );
    assert.equal(migrated.compact, false);
    // 用户自己在旧版里手动改过的字段保留，只把 compact 默认值关掉。
    assert.equal(migrated.visibility?.tps, false);
    assert.equal(migrated.visibility?.clientIP, true);
});

test('migrating an empty version-0 record yields the full default view', () => {
    const migrated = migrateLogFieldVisibility(undefined, 0);
    assert.equal(migrated.compact, false);
    assert.equal(migrated.visibility?.endpointType, true);
});

test('records already written by the current version are left untouched', () => {
    const stored = { compact: true, visibility: COMPACT_LOG_FIELD_VISIBILITY };
    const kept = migrateLogFieldVisibility(stored, LOG_FIELD_VISIBILITY_STORAGE_VERSION);
    assert.equal(kept.compact, true);
    assert.equal(kept.visibility?.clientIP, false);
});
