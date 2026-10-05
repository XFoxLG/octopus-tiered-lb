import assert from 'node:assert/strict';
import test from 'node:test';
import { filterMarketItems, MODEL_CAPABILITY_OPTIONS } from './filters.ts';

const items = [
  { name: 'openai/chat', input: 1, output: 2, cache_read: 0, cache_write: 0 },
  { name: 'chat', input: 0, output: 0, cache_read: 0, cache_write: 0 },
  { name: 'moderator', input: 0, output: 0, cache_read: 0.1, cache_write: 0 },
];
const base = {
  items, capabilities: [
    { name: 'openai/chat', endpoints: ['responses'], conversation: true },
    { name: 'chat', endpoints: [], conversation: false },
    { name: 'moderator', endpoints: ['moderations'], conversation: false },
  ],
  searchTerm: '', capability: 'all' as const, provider: '', dedupe: false, pricing: 'all' as const,
  inferProvider: (name: string) => name === 'moderator' ? 'Other' : 'OpenAI',
  normalizeName: (name: string) => name.replace('openai/', ''),
};

test('capability options include moderation and conversation matches endpoint grouping', () => {
  assert.ok(MODEL_CAPABILITY_OPTIONS.includes('moderations'));
  assert.deepEqual(filterMarketItems({ ...base, capability: 'chat' }).map((item) => item.name), ['openai/chat', 'chat']);
  assert.deepEqual(filterMarketItems({ ...base, capability: 'moderations' }).map((item) => item.name), ['moderator']);
});

test('filters combine and deduplication preserves the first matching sorted item', () => {
  assert.deepEqual(filterMarketItems({ ...base, dedupe: true }).map((item) => item.name), ['openai/chat', 'moderator']);
  assert.deepEqual(filterMarketItems({ ...base, searchTerm: ' CHAT ', provider: 'OpenAI', pricing: 'free', dedupe: true }).map((item) => item.name), ['chat']);
});

test('cache-only pricing counts as priced and unavailable capability data does not match', () => {
  assert.deepEqual(filterMarketItems({ ...base, pricing: 'priced' }).map((item) => item.name), ['openai/chat', 'moderator']);
  assert.deepEqual(filterMarketItems({ ...base, capability: 'chat', capabilities: [] }), []);
});
