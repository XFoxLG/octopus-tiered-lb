import assert from 'node:assert/strict';
import test from 'node:test';
import { exactId, withExactId, compareExactIds } from './exact-id.ts';

test('wire IDs survive JSON parsing and remain distinct', () => {
    const a = withExactId(JSON.parse('{"id":117392002743437824,"id_str":"117392002743437824"}'));
    const b = withExactId(JSON.parse('{"id":117392002743437825,"id_str":"117392002743437825"}'));
    assert.equal(a.id, '117392002743437824');
    assert.equal(b.id, '117392002743437825');
    assert.equal(compareExactIds(a.id, b.id), -1);
    assert.equal(new Set([a.id, b.id]).size, 2);
});

test('legacy safe IDs work but rounded IDs are rejected', () => {
    assert.equal(exactId(1790776268595), '1790776268595');
    assert.throws(() => exactId(JSON.parse('117392002743437824')), /unsafe/);
    assert.throws(() => exactId('1e17'), /unsafe/);
});
