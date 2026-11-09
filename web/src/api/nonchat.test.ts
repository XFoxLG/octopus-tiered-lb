import assert from 'node:assert/strict';
import test from 'node:test';
import { nonChatRates, totalAccountedTokens, type NonChatUsageSummary } from './nonchat.ts';
import { resolveEffectiveTokenCounts, resolveNonChatTokenDisplay, resolveLogInvestigationMarkers } from '../components/modules/log/display.ts';

test('total-only and attributed counters remain distinct without double-counting billed tokens', () => {
    assert.equal(totalAccountedTokens({ input_token: 700, output_token: 0, unattributed_token: 300 }), 1000);
    const summary: NonChatUsageSummary = { basis: 'processing', completeness: 'complete', total_tokens: 1000, counts: {} };
    const log = { endpoint_type: 'rerank', input_tokens: 0, output_tokens: 0, unattributed_token: 1000, non_chat_usage: summary };
    assert.equal(resolveEffectiveTokenCounts(log).totalTokens, 1000);
    assert.deepEqual(resolveNonChatTokenDisplay(log), { nonChat: true, inputUnspecified: true, outputUnspecified: true });
    summary.input_tokens = 1000;
    summary.output_tokens = 0;
    assert.equal(resolveNonChatTokenDisplay(log).outputUnspecified, false);
    assert.equal(resolveNonChatTokenDisplay(log).inputUnspecified, false);
});

test('non-chat rates use full request time and do not invent model output throughput', () => {
    const summary: NonChatUsageSummary = { basis: 'processing', completeness: 'complete', input_tokens: 451, total_tokens: 451, counts: { embedding_input_items: 8, embedding_vectors: 8, embedding_dimensions: 4096 } };
    assert.deepEqual(nonChatRates(summary, 2000), { itemsPerSecond: 4, inputTokensPerSecond: 225.5 });
    assert.deepEqual(nonChatRates({ ...summary, basis: 'billed' }, 2000), { itemsPerSecond: 4, inputTokensPerSecond: null });
    assert.deepEqual(nonChatRates(summary, 0), { itemsPerSecond: null, inputTokensPerSecond: null });
    assert.equal(nonChatRates({ ...summary, counts: { rerank_documents: 30, rerank_results: 5 } }, 2000).itemsPerSecond, 15);
});

test('usage anomalies are not relabeled as missing usage, empty output or interrupted generation', () => {
    const markers = resolveLogInvestigationMarkers({ endpoint_type: 'embeddings', ftut: 0, use_time: 1000, output_tokens: 0, usage_state: 'not_reported', non_chat_usage: { basis: 'processing', completeness: 'invalid', counts: { embedding_vectors: 8 } } });
    assert.deepEqual(markers, []);
});
