import assert from 'node:assert/strict';
import test from 'node:test';
import {
    formatFirstTokenMs,
    isSuspectFakeStreamSignature,
} from './fake-stream.ts';

test('flags the calibrated fake-streaming signature (real log samples)', () => {
    // GG 假流式-cache：首字 260.112s / 总 260.379s（实测内容在最后 ~0.3s 集中到达）
    assert.equal(isSuspectFakeStreamSignature(260112, 260379), true);
    // GG 假流式：首字 220.949s / 总 221.028s
    assert.equal(isSuspectFakeStreamSignature(220949, 221028), true);
    // 旧 GG 变体：首字 205.299s / 总 509.415s（占比 40.3% → 走次判定，仍标记）
    assert.equal(isSuspectFakeStreamSignature(205299, 509415), true);
});

test('does not flag real streaming samples', () => {
    // New Cross gemini-3.8-flash：首字 19.538s / 总 90.709s（真流式，占比 21.5%）
    assert.equal(isSuspectFakeStreamSignature(19538, 90709), false);
    // wududu gemini-3.8-flash-high：首字 21.404s / 总 155.423s
    assert.equal(isSuspectFakeStreamSignature(21404, 155423), false);
    // GG 真流式：首字 17.504s / 总 156.748s
    assert.equal(isSuspectFakeStreamSignature(17504, 156748), false);
});

test('short requests with high first-token share are not flagged', () => {
    // 小请求（总时长 < 60s）首字占比天然偏高：9.584s / 9.828s 不标记
    assert.equal(isSuspectFakeStreamSignature(9584, 9828), false);
    // 边界：59.9s 不进入判定
    assert.equal(isSuspectFakeStreamSignature(55000, 59900), false);
});

test('missing milestones never produce a marker', () => {
    assert.equal(isSuspectFakeStreamSignature(0, 120000), false);
    assert.equal(isSuspectFakeStreamSignature(90000, 0), false);
    assert.equal(isSuspectFakeStreamSignature(-1, -1), false);
});

test('formatFirstTokenMs renders compact values', () => {
    assert.equal(formatFirstTokenMs(0), '—');
    assert.equal(formatFirstTokenMs(850), '850ms');
    assert.equal(formatFirstTokenMs(260112), '260.11s');
});
