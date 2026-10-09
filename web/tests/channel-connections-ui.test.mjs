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
function fixture({ locale = 'en', value = helpers.newConnectionConfig(), preview, previewError = false, previewLoading = false, legacyMode = false } = {}) {
    let config = legacyMode ? undefined : value;
    const messages = JSON.parse(read(`../public/locale/${locale}.json`));
    const translate = createTranslator({ locale: locale.replace('_', '-'), messages, namespace: 'channel.connection', onError: (error) => { throw error; } });
    const { ConnectionEditor } = load('../src/components/modules/channel/ConnectionEditor.tsx', {
        react: {
            useState: (initial) => [initial, () => {}],
            // 直接调用组件函数时没有真正的 commit 阶段：同步执行 effect，模拟首帧后的预填回写。
            useEffect: (effect) => { effect(); },
            useRef: (initial) => ({ current: initial }),
        },
        'next-intl': { useTranslations: () => translate },
        '@/api/endpoints/channel': { useChannelConnectionPreview: () => ({ data: preview, isError: previewError, isLoading: previewLoading, refetch: () => {} }) },
        '@/components/ui/button': { Button: 'button' },
        '@/components/ui/input': { Input: 'input' },
        './connection-config': helpers,
    });
    const render = () => ConnectionEditor({ value: config, onChange: (next) => { config = next; }, channelId: legacyMode ? 9 : undefined, idPrefix: 'fixture' });
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
test('legacy configuration is prefilled automatically as the draft without a confirmation step', () => {
    const proposed = helpers.newConnectionConfig();
    const ui = fixture({ legacyMode: true, preview: { automatic: true, config: proposed, groups: [] } });
    const markup = renderToStaticMarkup(ui.render());
    assert.equal(ui.value, proposed);
    assert.match(markup, /Interfaces and addresses/);
    assert.doesNotMatch(markup, /confirm migration/i);
});
test('non-equivalent legacy preview is still applied silently as the draft', () => {
    const proposed = helpers.newConnectionConfig();
    const ui = fixture({ legacyMode: true, preview: { automatic: false, config: proposed, reason: 'groups' } });
    ui.render();
    assert.equal(ui.value, proposed);
});
test('a failed preview never sticks on loading and offers manual configuration', () => {
    const ui = fixture({ legacyMode: true, preview: { automatic: false, reason: 'multiple_or_missing_addresses' }, previewLoading: false });
    const buttons = all(ui.render(), (node) => node.type === 'button');
    assert.ok(buttons.some((button) => button.props.children === 'Configure manually'));
    assert.doesNotMatch(renderToStaticMarkup(buttons[0]), /Loading legacy configuration/);
});
test('preview request errors provide the same manual escape hatch', () => {
    const ui = fixture({ legacyMode: true, previewError: true });
    const manual = all(ui.render(), (node) => node.type === 'button' && node.props.children === 'Configure manually')[0];
    manual.props.onClick();
    assert.equal(ui.value.selection, 'same_protocol');
    assert.equal(ui.value.endpoints.length, 1);
});
test('catalog format follows a source change only when it had followed the old protocol', () => {
    assert.equal(helpers.catalogFormatForEndpointProtocol('messages'), 'anthropic');
    assert.equal(helpers.autoCatalogFormatOnEndpointChange('chat', 'messages', 'openai'), 'anthropic');
    assert.equal(helpers.autoCatalogFormatOnEndpointChange('chat', 'responses', 'anthropic'), 'anthropic');
    assert.equal(helpers.autoCatalogFormatOnEndpointChange('messages', 'gemini', 'anthropic'), 'gemini');
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

test('native disclosure arrows match the app chevron and stay keyboard visible', () => {
    for (const path of ['ConnectionEditor.tsx', 'Form.tsx']) {
        const source = read(`../src/components/modules/channel/${path}`);
        assert.match(source, /\[&::-webkit-details-marker\]:hidden/);
        assert.match(source, /group-open:rotate-180/);
        assert.match(source, /focus-visible:ring-2 focus-visible:ring-ring/);
    }
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

const CHANNEL_TYPES = { OpenAIEmbedding: 3 };

function testDialogFixture({ locale = 'en', overrides = {} } = {}) {
    const messages = JSON.parse(read(`../public/locale/${locale}.json`));
    const translateFor = (namespace) => createTranslator({
        locale: locale.replace('_', '-'),
        messages,
        namespace,
        // 缺键会抛错：这样「界面显示原始 key」会直接变成测试失败。
        onError: (error) => { throw error; },
    });
    const noopMutation = { isPending: false, mutate: () => {}, mutateAsync: async () => ({}) };
    const { TestDialog } = load('../src/components/modules/channel/TestDialog.tsx', {
        react: {
            useState: (initial) => [initial, () => {}],
            useEffect: (effect) => { effect(); },
            useMemo: (factory) => factory(),
            useRef: (initial) => ({ current: initial }),
        },
        'next-intl': { useTranslations: (namespace) => translateFor(namespace) },
        'lucide-react': new Proxy({}, { get: () => 'span' }),
        '@/api/endpoints/channel': {
            ChannelType: CHANNEL_TYPES,
            useTestChannelModel: () => noopMutation,
            useChannelProbe: () => noopMutation,
            useCheckChannelKeys: () => noopMutation,
            useApplyChannelProbe: () => noopMutation,
        },
        '@/api/endpoints/group': { useGroupTestProgress: () => ({ data: undefined }) },
        '@/components/ui/button': { Button: 'button' },
        '@/components/ui/input': { Input: 'input' },
        '@/components/common/Toast': { toast: { error: () => {}, success: () => {}, warning: () => {} } },
        '@/lib/utils': { cn: (...values) => values.filter(Boolean).join(' ') },
    });
    const channel = {
        id: 7,
        type: 1,
        model: 'gpt-4o-mini',
        custom_model: '',
        skip_model_test: false,
        keys: [{ id: 1, enabled: true, channel_key: 'sk-test' }],
        connection_config: undefined,
        ...overrides,
    };
    const element = TestDialog({ channel, availableModels: ['gpt-4o-mini'], onClose: () => {}, onKeysUnavailable: () => {} });
    return { element, markup: renderToStaticMarkup(element) };
}

for (const locale of ['en', 'zh_hans', 'zh_hant']) test(`${locale}: the merged test dialog renders three labelled sections with no raw keys`, () => {
    const { element, markup } = testDialogFixture({ locale });
    const messages = JSON.parse(read(`../public/locale/${locale}.json`));
    for (const section of Object.values(messages.channel.test.sections)) {
        // 分段标题必须是独立的标题元素（markup 里 & 会被转义，所以查元素树而不是字符串）。
        const heading = all(element, (node) => node.type === 'p' && node.props.children === section);
        assert.equal(heading.length, 1, `分段标题 ${section} 应恰好出现一次`);
    }
    assert.match(markup, /role="region"/);
    assert.match(markup, /aria-busy="false"/);
    assert.equal(markup.includes('channel.test.'), false);
    assert.equal(markup.includes('channel.probe.'), false);
    assert.equal(markup.includes('channel.detail.'), false);
    const buttons = all(element, (node) => node.type === 'button');
    assert.ok(buttons.length >= 3);
    assert.ok(buttons.every((button) => button.props.type === 'button'), '每个按钮都必须是 type=button');
    // 模型应答 / 协议与能力 / 逐 Key 三段各有一个运行按钮，且都没有被禁用。
    for (const section of Object.values(messages.channel.test.sections)) {
        const run = buttons.find((button) => Array.isArray(button.props.children)
            && button.props.children.some((child) => typeof child === 'string' && child.includes(section)));
        assert.ok(run, `缺少「${section}」的运行按钮`);
        assert.equal(run.props.disabled, false);
    }
});

test('legacy channels offer to apply probe results; migrated channels are diagnostic only', () => {
    const messages = JSON.parse(read('../public/locale/en.json'));
    const legacy = testDialogFixture();
    assert.ok(legacy.markup.includes(messages.channel.probe.apply));
    assert.equal(legacy.markup.includes(messages.channel.test.diagnosticOnly), false);

    const migrated = testDialogFixture({ overrides: { connection_config: { selection: 'same_protocol', endpoints: [], catalog: { format: 'manual' } } } });
    assert.equal(migrated.markup.includes(messages.channel.probe.apply), false);
    assert.ok(migrated.markup.includes(messages.channel.test.diagnosticOnly));
});

test('a channel without keys explains the gap and disables the per-key run button', () => {
    const messages = JSON.parse(read('../public/locale/en.json'));
    const { element, markup } = testDialogFixture({ overrides: { keys: [] } });
    assert.ok(markup.includes(messages.channel.test.noKeys));
    const perKey = all(element, (node) => node.type === 'button'
        && Array.isArray(node.props.children)
        && node.props.children.some((child) => typeof child === 'string' && child.includes(messages.channel.test.sections.keys)));
    assert.equal(perKey.length, 1);
    assert.equal(perKey[0].props.disabled, true);
});

test('channel detail keeps a single test entry and drops the retired parallel buttons', () => {
    const card = read('../src/components/modules/channel/CardContent.tsx');
    assert.match(card, /<TestDialog/);
    assert.doesNotMatch(card, /ProbeDialog/);
    assert.doesNotMatch(card, /testModel\.probe|actions\.checkKeys/);
    assert.equal(fs.existsSync(new URL('../src/components/modules/channel/ProbeDialog.tsx', import.meta.url)), false);
});
