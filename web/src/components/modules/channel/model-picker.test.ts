import assert from 'node:assert/strict';
import test from 'node:test';
import { normalizeFetchedModels, toggleModelSelection } from './model-picker.ts';

test('normalizes the multi-key union including models exclusive to later keys', () => {
    assert.deepEqual(
        normalizeFetchedModels(['gpt-4o', 'claude-sonnet', 'gpt-4o', ' gemini-pro ', '']),
        ['claude-sonnet', 'gemini-pro', 'gpt-4o'],
    );
});

test('model ordering is stable across unordered union responses', () => {
    assert.deepEqual(
        normalizeFetchedModels(['gemini-pro', 'gpt-4o', 'claude-sonnet']),
        normalizeFetchedModels(['claude-sonnet', 'gemini-pro', 'gpt-4o']),
    );
});

test('normalizes legacy model objects and ignores invalid values', () => {
    assert.deepEqual(normalizeFetchedModels([
        { id: ' gpt-4o ' }, { name: 'claude-sonnet' },
        { display_name: 'gemini-pro' }, { displayName: 'mimo' },
        null, 123, {}, { id: false }, ' ', 'gpt-4o',
    ]), ['claude-sonnet', 'gemini-pro', 'gpt-4o', 'mimo']);
    assert.deepEqual(normalizeFetchedModels(null), []);
    assert.deepEqual(normalizeFetchedModels(undefined), []);
});

test('selects every model for a key while retaining other selected models', () => {
    assert.deepEqual(
        toggleModelSelection(['existing', 'shared'], ['shared', 'key-only', 'key-only']),
        ['existing', 'shared', 'key-only'],
    );
});

test('deselecting a key removes only that key models', () => {
    assert.deepEqual(
        toggleModelSelection(['existing', 'shared', 'key-only'], ['shared', 'key-only']),
        ['existing'],
    );
});

test('empty or failed-key model lists leave selection unchanged', () => {
    const selected = ['existing'];
    assert.equal(toggleModelSelection(selected, []), selected);
    assert.equal(toggleModelSelection(selected, [' ', '']), selected);
});

test('per-key and all-model selection use the same draft without duplicates', () => {
    const firstKey = ['shared', 'first-only'];
    const secondKey = ['shared', 'second-only'];
    const union = normalizeFetchedModels([...firstKey, ...secondKey]);
    const perKeyDraft = toggleModelSelection(['custom'], secondKey);
    const allModelsDraft = toggleModelSelection(perKeyDraft, union);
    assert.deepEqual(new Set(allModelsDraft), new Set(['custom', ...union]));
    assert.deepEqual(toggleModelSelection(allModelsDraft, firstKey), ['custom', 'second-only']);
});
