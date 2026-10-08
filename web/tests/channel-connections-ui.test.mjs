import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import ts from 'typescript';
import { renderToStaticMarkup } from 'react-dom/server';
import { createTranslator } from 'next-intl';

const require = createRequire(import.meta.url);
const read = (path) => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
let sequence = 0;
function load(path, dependencies = {}) {
    const evaluatedModule = { exports: {} };
    const code = ts.transpileModule(read(path), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX } }).outputText;
    vm.runInNewContext(code, { module: evaluatedModule, exports: evaluatedModule.exports, URL, crypto: { randomUUID: () => `endpoint-${++sequence}` }, require: (id) => dependencies[id] ?? require(id) });
    return evaluatedModule.exports;
}
const helpers = load('../src/components/modules/channel/connection-config.ts');
function all(node, predicate) {
    if (Array.isArray(node)) return node.flatMap((child) => all(child, predicate));
    if (!node?.props) return [];
    return [...(predicate(node) ? [node] : []), ...all(node.props.children, predicate)];
}
function fixture({ locale = 'en', value = helpers.newConnectionConfig(), preview, legacyMode = false } = {}) {
    let config = legacyMode ? undefined : value;
    const messages = JSON.parse(read(`../public/locale/${locale}.json`));
    const translate = createTranslator({ locale: locale.replace('_', '-'), messages, namespace: 'channel.connection', onError: (error) => { throw error; } });
    const { ConnectionEditor } = load('../src/components/modules/channel/ConnectionEditor.tsx', {
        react: { useState: (initial) => [initial, () => {}] },
        'next-intl': { useTranslations: () => translate },
        '@/api/endpoints/channel': { useChannelConnectionPreview: () => ({ data: preview }) },
        '@/components/ui/button': { Button: 'button' },
        '@/components/ui/input': { Input: 'input' },
        './connection-config': helpers,
    });
    const render = () => ConnectionEditor({ value: config, onChange: (next) => { config = next; }, legacy: { type: 0, base_urls: [{ url: 'https://legacy.test/v1' }] }, channelId: legacyMode ? 9 : undefined, idPrefix: 'fixture' });
    return { render, get value() { return config; } };
}
test('new interfaces share URLs without sharing identity; reordering and deletion preserve catalog ownership', () => {
    const ui = fixture();
    const first = ui.value.endpoints[0].id;
    all(ui.render(), (e) => e.type === 'button' && e.props.children?.[1] === 'Add interface')[0].props.onClick();
    const second = ui.value.endpoints[1].id;
    assert.notEqual(first, second);
    all(ui.render(), (e) => e.type === 'input' && e.props.type === 'url').slice(0, 2).forEach((entry) => entry.props.onChange({ target: { value: 'https://example.com/v1' } }));
    all(ui.render(), (e) => e.props['aria-label'] === 'Move interface 2 up')[0].props.onClick();
    assert.equal(ui.value.endpoints[0].id, second);
    assert.equal(ui.value.catalog.endpoint_id, first);
    all(ui.render(), (e) => e.props['aria-label'] === 'Remove interface 2')[0].props.onClick();
    assert.equal(ui.value.catalog.format, 'manual');
    assert.equal(ui.value.endpoints.length, 1);
});
test('legacy configuration is not applied by rendering; explicit confirmation only changes the draft', () => {
    const proposed = helpers.newConnectionConfig();
    const ui = fixture({ legacyMode: true, preview: { automatic: true, config: proposed, groups: [] } });
    ui.render(); assert.equal(ui.value, undefined);
    all(ui.render(), (e) => e.type === 'button')[0].props.onClick();
    assert.equal(ui.value, proposed);
});
test('brand choices are retired while Chat retains an optional MiMo profile', () => {
    const ui = fixture();
    const options = all(ui.render(), (e) => e.type === 'option').map((e) => e.props.value);
    assert.ok(options.includes('mimo'));
    assert.ok(!options.includes('cloudflare'));
    assert.ok(!options.includes('volcengine'));
});
for (const locale of ['en', 'zh_hans', 'zh_hant']) test(`${locale}: interface fields render real labels and keyboard controls`, () => {
    const ui = fixture({ locale });
    const markup = renderToStaticMarkup(ui.render());
    assert.doesNotMatch(markup, /channel\.connection\./);
    assert.match(markup, /aria-live="polite"/);
    assert.match(markup, /<label[^>]*for="fixture-endpoint-/);
    assert.match(markup, /<summary/);
    assert.ok(all(ui.render(), (e) => e.type === 'button').every((e) => e.props.type === 'button'));
});
test('URL previews preserve custom prefixes and do not show query credentials', () => {
    const endpoint = { ...helpers.newEndpoint(), url: 'https://example.com/proxy%2Ftenant?token=secret&x=1' };
    const preview = helpers.connectionURLPreview(endpoint);
    assert.match(preview, /proxy%2Ftenant\/chat\/completions/);
    assert.doesNotMatch(preview, /secret/);
    endpoint.url_mode = 'full'; endpoint.url = 'https://example.com/custom%2Fpath?z=2&a=1';
    assert.equal(helpers.connectionURLPreview(endpoint), endpoint.url);
    assert.equal(helpers.canFetchConnectionModels({ ...helpers.newConnectionConfig(), catalog: { format: 'manual' } }), false);
});
