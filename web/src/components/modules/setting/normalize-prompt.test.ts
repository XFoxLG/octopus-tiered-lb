import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createTranslator } from 'next-intl';

import {
  dotDashKey,
  normalizeModelName,
  normalizeToBase,
  setNormalizeRules,
  type ExplicitMapping,
} from '../model/normalize.ts';

interface RulesExample {
  router_prefixes: string[];
  functional_suffixes: string[];
  explicit_mappings: ExplicitMapping[];
}

const locales = [
  { file: 'en', locale: 'en', downloadable: /downloadable/ },
  { file: 'zh_hans', locale: 'zh-Hans', downloadable: /可下载/ },
  { file: 'zh_hant', locale: 'zh-Hant', downloadable: /可下載/ },
];

function parseExample(prompt: string): RulesExample {
  const start = prompt.lastIndexOf('\n{\n');
  assert.notEqual(start, -1, 'The prompt must end with a JSON rules example');
  return JSON.parse(prompt.slice(start + 1));
}

function applyRules(rules: RulesExample) {
  setNormalizeRules({
    routerPrefixes: rules.router_prefixes,
    functionalSuffixes: rules.functional_suffixes,
    explicitMappings: rules.explicit_mappings,
  });
}

test.afterEach(() => setNormalizeRules());

test('the copy action reads the prompt as literal text, not an ICU message', () => {
  const source = readFileSync(new URL('./Normalize.tsx', import.meta.url), 'utf8');
  assert.match(source, /writeClipboardText\(t\.raw\('normalize\.workflow\.prompt'\)\)/);
});

for (const { file, locale, downloadable } of locales) {
  const messages = JSON.parse(readFileSync(new URL(`../../../../public/locale/${file}.json`, import.meta.url), 'utf8'));
  const workflow = messages.setting.normalize.workflow;
  const t = createTranslator({
    locale,
    messages,
    namespace: 'setting.normalize.workflow',
    onError(error) {
      throw error;
    },
  });
  const prompt = t.raw('prompt') as string;

  test(`${locale}: the prompt requires an actual JSON file and documents the handoff`, () => {
    assert.equal(prompt, workflow.prompt);
    for (const text of [prompt, workflow.description, workflow.promptHint, messages.setting.normalize.import.hint]) {
      assert.ok(text.includes('normalize-rules.json'));
    }
    assert.match(prompt, downloadable);
    for (const text of ['UTF-8', 'application/json', 'JSON.parse', '-non-reasoning', '-reasoning', '-pro', '-mini', '-flash']) {
      assert.ok(prompt.includes(text), `Missing prompt requirement: ${text}`);
    }
    assert.ok(workflow.promptCopyFailed.trim());
    assert.ok(prompt.includes(String.raw`"-@\\w+"`));
    assert.ok(!prompt.includes(String.raw`\@`), '@ must not be escaped');
  });

  test(`${locale}: the JSON example has the import schema and valid regex escaping`, () => {
    const rules = parseExample(prompt);
    assert.deepEqual(Object.keys(rules).sort(), ['explicit_mappings', 'functional_suffixes', 'router_prefixes']);
    assert.ok(rules.router_prefixes.every((value) => typeof value === 'string' && value.endsWith('-') && !value.includes('/')));
    assert.ok(rules.functional_suffixes.every((value) => typeof value === 'string' && value.startsWith('-')));
    assert.ok(rules.explicit_mappings.every((value) => typeof value.variant === 'string' && typeof value.canonical === 'string'));

    for (const pattern of [String.raw`-\d{4}-\d{2}-\d{2}`, String.raw`-\d{8}`, String.raw`-\d{6}`, String.raw`-\d{4}`, String.raw`-@\w+`]) {
      assert.ok(rules.functional_suffixes.includes(pattern), `Missing regex example: ${pattern}`);
      assert.doesNotThrow(() => new RegExp(`${pattern}$`));
    }
    assert.ok(rules.functional_suffixes.indexOf(String.raw`-\d{4}-\d{2}-\d{2}`) < rules.functional_suffixes.indexOf(String.raw`-\d{4}`));

    applyRules(rules);
    const keys = rules.explicit_mappings.map(({ variant }) => dotDashKey(normalizeToBase(variant)));
    assert.equal(new Set(keys).size, keys.length, 'The example must not include equivalent or bidirectional mappings');
  });

  test(`${locale}: the example works with the normalization engine without merging model tiers`, () => {
    applyRules(parseExample(prompt));
    const variants = [
      ['[官B]CLAUDE-OPUS-4-6-thinking-ssvip', 'claude-opus-4.6'],
      ['[特价C]-claude-opus-4-6-guan-cc', 'claude-opus-4.6'],
      ['DMXAPI-clo-4-6-20251101-thinking', 'claude-opus-4.6'],
      ['anthropic/claude-opus-4.6', 'claude-opus-4.6'],
      ['gpt-4o-2024-05-13', 'gpt-4o'],
      ['doubao-seed-1-6-250615', 'doubao-seed-1-6'],
      ['deepseek-r1-0528', 'deepseek-r1'],
      ['claude-3-haiku@20240307', 'claude-3-haiku'],
      ['kimi-k2.5-@latest', 'kimi-k2.5'],
      ['deepseek/deepseek-r1:free', 'deepseek-r1'],
      ['deepseek-r1(free)', 'deepseek-r1'],
    ];
    for (const [variant, canonical] of variants) {
      assert.equal(normalizeModelName(variant), canonical, variant);
    }
    for (const model of ['gpt-4o-mini', 'gemini-2.5-flash-lite', 'deepseek-v4-pro', 'Baichuan4-Turbo', 'grok-4-20-non-reasoning', 'kimi-k2.5-256k']) {
      assert.equal(normalizeModelName(model), model.toLowerCase(), model);
    }
  });
}
