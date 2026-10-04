import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import ts from 'typescript';
import { renderToStaticMarkup } from 'react-dom/server';
import { createTranslator } from 'next-intl';

const require = createRequire(import.meta.url);
const readSource = (relativePath) => fs.readFileSync(new URL(relativePath, import.meta.url), 'utf8');
const parseSource = (relativePath) => ts.createSourceFile(relativePath, readSource(relativePath), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);

function collectNodes(sourceFile, predicate) {
    const matches = [];
    const visit = (node) => {
        if (predicate(node)) matches.push(node);
        ts.forEachChild(node, visit);
    };
    visit(sourceFile);
    return matches;
}

function evaluateSource(source, dependencies = {}) {
    const evaluatedModule = { exports: {} };
    const compiled = ts.transpileModule(source, {
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX },
    });
    vm.runInNewContext(compiled.outputText, {
        module: evaluatedModule,
        exports: evaluatedModule.exports,
        require: (specifier) => dependencies[specifier] ?? require(specifier),
        ...dependencies,
    });
    return evaluatedModule.exports;
}

const groupSource = parseSource('../src/components/modules/group/GroupListItem.tsx');
const healthVariableNames = new Set([
    'previousHealthSnapshotId', 'healthQuery', 'healthSnapshot',
    'awaitingHealthSnapshot', 'isHealthProbeRunning', 'healthProbeLabel',
]);
const healthDeclarations = collectNodes(groupSource, (node) => ts.isVariableStatement(node)
    && node.declarationList.declarations.some((declaration) => healthVariableNames.has(declaration.name.getText(groupSource))));
const healthButton = collectNodes(groupSource, (node) => ts.isJsxElement(node)
    && node.openingElement.tagName.getText(groupSource) === 'button'
    && node.openingElement.attributes.properties.some((attribute) => attribute.name?.getText(groupSource) === 'aria-busy'))[0];
const healthResult = collectNodes(groupSource, (node) => ts.isJsxExpression(node)
    && node.expression?.getText(groupSource).startsWith('healthSnapshot &&'))[0];

function renderHealth(view, mutation = {}) {
    const messages = JSON.parse(readSource('../public/locale/en.json'));
    const translate = createTranslator({ locale: 'en', messages, namespace: 'group', onError: (error) => { throw error; } });
    const { render } = evaluateSource(`
        import { jsx } from 'react/jsx-runtime';
        const Loader2 = () => jsx('span', { 'data-icon': 'loading' });
        const HeartPulse = () => jsx('span', { 'data-icon': 'health' });
        export function render() {
            ${healthDeclarations.map((node) => node.getText(groupSource)).join('\n')}
            return { button: ${healthButton.getText(groupSource)}, result: ${healthResult.expression.getText(groupSource)} };
        }
    `, {
        group: { id: 7 },
        expanded: false,
        runGroupHealth: { isPending: false, isSuccess: false, ...mutation },
        useGroupHealthLatest: () => ({ data: view }),
        t: translate,
        cn: (...classes) => classes.filter(Boolean).join(' '),
    });
    const rendered = render();
    return { button: renderToStaticMarkup(rendered.button), result: renderToStaticMarkup(rendered.result) };
}

const viewWithSnapshot = (status, snapshotId = 20) => ({
    group_id: 7,
    group_name: 'Example',
    group_mode: 3,
    latest: { id: snapshotId, group_id: 7, status, duration_ms: 25, message: '' },
});

test('health group view without latest renders no undefined result chip', () => {
    const rendered = renderHealth({ group_id: 7, group_name: 'Example', group_mode: 3 });
    assert.equal(rendered.result, '');
    assert.doesNotMatch(rendered.button, /disabled=""|undefined/);
    assert.match(rendered.button, /aria-label="Run health probe/);
});

test('pending and accepted health requests are not reported as completed', () => {
    const pending = renderHealth(viewWithSnapshot('success'), { isPending: true });
    assert.match(pending.button, /Starting health probe/);
    assert.match(pending.button, /disabled=""/);
    assert.equal(pending.result, '');
    const accepted = renderHealth(viewWithSnapshot('success'), {
        isSuccess: true, data: { accepted: true, group_id: 7 }, context: { previousSnapshotId: 20 },
    });
    assert.match(accepted.button, /accepted; waiting for results/);
    assert.match(accepted.button, /disabled=""/);
    assert.equal(accepted.result, '');
});

test('running health stays disabled and only terminal snapshots display duration', () => {
    const mutation = { isSuccess: true, context: { previousSnapshotId: 19 } };
    const running = renderHealth(viewWithSnapshot('running'), mutation);
    assert.match(running.button, /aria-label="Probing"/);
    assert.match(running.button, /disabled=""/);
    assert.doesNotMatch(running.result, /Took|undefined/);
    for (const [status, label] of [['success', 'Probe passed'], ['partial', 'Partially passed'], ['failed', 'Probe failed']]) {
        const completed = renderHealth(viewWithSnapshot(status), mutation);
        assert.doesNotMatch(completed.button, /disabled=""/);
        assert.ok(completed.result.includes(label));
        assert.match(completed.result, /Took 25 ms/);
    }
});

function createHealthApiFixture() {
    const requests = [];
    const invalidations = [];
    let view = viewWithSnapshot('success');
    const apiClient = {
        get: async (url) => { requests.push({ method: 'GET', url }); return view; },
        post: async (url, body) => { requests.push({ method: 'POST', url, body }); return { accepted: true, group_id: 7 }; },
    };
    const source = parseSource('../src/api/endpoints/group.ts');
    const declarations = source.statements.filter((node) => !ts.isImportDeclaration(node)
        && node.getStart(source) < source.text.indexOf('export type ToolsProbeVerdict'));
    const hooks = evaluateSource(declarations.map((node) => node.getText(source)).join('\n'), {
        apiClient,
        REFETCH_INTERVAL_DEFAULT: 30_000,
        useQuery: (options) => options,
        useMutation: (options) => options,
        useQueryClient: () => ({
            fetchQuery: (options) => options.queryFn(),
            invalidateQueries: (options) => { invalidations.push(options.queryKey); return Promise.resolve(); },
        }),
    });
    return { hooks, requests, invalidations, setView: (nextView) => { view = nextView; } };
}

test('health API separates acknowledgements from views and invalidates the latest group key', async () => {
    const fixture = createHealthApiFixture();
    const mutation = fixture.hooks.useRunGroupHealth();
    const context = await mutation.onMutate({ groupId: 7 });
    assert.equal(context.previousSnapshotId, 20);
    const accepted = await mutation.mutationFn({ groupId: 7 });
    assert.equal(accepted.accepted, true);
    assert.equal(accepted.status, undefined);
    mutation.onSettled(accepted, null, { groupId: 7 });
    assert.equal(JSON.stringify(fixture.invalidations), JSON.stringify([['group-health-latest', 7], ['analytics', 'group-health']]));
    assert.equal(JSON.stringify(fixture.requests), JSON.stringify([
        { method: 'GET', url: '/api/v1/group/health/latest/7' },
        { method: 'POST', url: '/api/v1/group/health/run/7', body: { probe_mode: 'standard' } },
    ]));
    fixture.setView({ group_id: 7, group_name: 'Example', group_mode: 3 });
    const query = fixture.hooks.useGroupHealthLatest(7);
    assert.equal((await query.queryFn()).latest, undefined);
    assert.equal(JSON.stringify(query.queryKey), JSON.stringify(fixture.invalidations[0]));
});

test('health polling follows accepted and running snapshots even when collapsed, then stops', () => {
    const { hooks } = createHealthApiFixture();
    const collapsed = hooks.useGroupHealthLatest(7, false, 20);
    const state = (view) => ({ state: { data: view } });
    for (const view of [undefined, { group_id: 7 }, viewWithSnapshot('success'), viewWithSnapshot('running', 21)]) {
        assert.equal(collapsed.enabled(state(view)), true);
        assert.equal(collapsed.refetchInterval(state(view)), 1_000);
    }
    assert.equal(collapsed.enabled(state(viewWithSnapshot('success', 21))), false);
    assert.equal(collapsed.refetchInterval(state(viewWithSnapshot('success', 21))), 30_000);
    assert.equal(hooks.useGroupHealthLatest(undefined, true).enabled(state(undefined)), false);
});

test('channel protocol selection is multi-select, order-preserving, and gates conflicting combinations', () => {
    const source = readSource('../src/components/modules/channel/Form.tsx');
    // 提取真正的实现（而不是在这里重写一份逻辑），保证测试跟着产品代码走。
    const helpers = source.slice(
        source.indexOf('export function toggleUpstreamProtocol('),
        source.indexOf('export interface ChannelFormProps {'),
    );
    const { toggleUpstreamProtocol, upstreamProtocolConflict, normalizeTimeoutInputValue } = evaluateSource(
        `${helpers}
        export { toggleUpstreamProtocol, upstreamProtocolConflict, normalizeTimeoutInputValue };`,
    );
    // 返回值来自 vm 沙箱，数组原型与宿主不同，用 JSON 比较结构。
    const asJson = (value) => JSON.stringify(value);

    // 勾选顺序 = 尝试优先级：新勾选追加到末尾，不插队。
    let protocols = [];
    protocols = toggleUpstreamProtocol(protocols, 'chat', true);
    protocols = toggleUpstreamProtocol(protocols, 'responses', true);
    assert.equal(asJson(protocols), asJson(['chat', 'responses']));
    // 重排（先取消再勾选）必须反映到顺序上。
    protocols = toggleUpstreamProtocol(protocols, 'chat', false);
    protocols = toggleUpstreamProtocol(protocols, 'chat', true);
    assert.equal(asJson(protocols), asJson(['responses', 'chat']));
    // 重复勾选不产生重复项。
    assert.equal(asJson(toggleUpstreamProtocol(protocols, 'responses', true)), asJson(['responses', 'chat']));

    // 互斥组合必须被识别（前端禁用 + 后端拒绝，两处规则一致）。
    assert.equal(upstreamProtocolConflict(['passthrough', 'chat']), true);
    assert.equal(upstreamProtocolConflict(['raw', 'responses']), true);
    assert.equal(upstreamProtocolConflict(['chat_only', 'chat']), true);
    assert.equal(upstreamProtocolConflict(['responses_only', 'responses']), true);
    assert.equal(upstreamProtocolConflict(['messages_only', 'messages']), true);
    assert.equal(upstreamProtocolConflict(['chat', 'responses']), false);
    assert.equal(upstreamProtocolConflict(['responses_only', 'chat_only']), false);
    assert.equal(upstreamProtocolConflict(['chat']), false);
    assert.equal(upstreamProtocolConflict([]), false);

    // 超时输入：空串与非法输入都映射成 0（跟随分组），负一保留为"关闭"。
    assert.equal(normalizeTimeoutInputValue(''), 0);
    assert.equal(normalizeTimeoutInputValue('   '), 0);
    assert.equal(normalizeTimeoutInputValue('abc'), 0);
    assert.equal(normalizeTimeoutInputValue('-1'), -1);
    assert.equal(normalizeTimeoutInputValue('0'), 0);
    assert.equal(normalizeTimeoutInputValue('30'), 30);
    assert.equal(normalizeTimeoutInputValue(' 12.9 '), 12);
});

test('member-only group edits preserve advanced values while explicit clearing remains possible', () => {
    const source = readSource('../src/components/modules/group/GroupListItem.tsx');
    const advancedUpdates = source.slice(source.indexOf('const nextDefaultReasoningEffort ='), source.indexOf('if (items_to_add.length)'));
    const { buildPayload } = evaluateSource(`
        export function buildPayload(group, values) {
            const payload = {};
            ${advancedUpdates}
            return payload;
        }
    `);
    const group = {
        default_reasoning_effort: 'high', reasoning_force_override: true,
        param_override: '{"temperature":0.7}', custom_header: [{ header_key: 'X-Test', header_value: 'value' }],
    };
    assert.equal(JSON.stringify(buildPayload(group, { members: [] })), '{}');
    const cleared = buildPayload(group, {
        default_reasoning_effort: '', reasoning_force_override: false, param_override: '', custom_header: [],
    });
    assert.equal(cleared.default_reasoning_effort, '');
    assert.equal(cleared.reasoning_force_override, false);
    assert.equal(cleared.param_override, '');
    assert.equal(cleared.custom_header.length, 0);
});

test('group advanced fields retain shrinkable native selects and a labeled override switch', () => {
    const source = readSource('../src/components/modules/group/Editor.tsx');
    assert.match(source, /@container\/group-settings/);
    assert.match(source, /@\[28rem\]\/group-settings:grid-cols-2/);
    assert.match(source, /\[&_select\]:min-w-0/);
    assert.match(source, /\[&_select\]:max-w-full/);
    assert.match(source, /<Switch\s+id="group-reasoning-force-override"/);
    assert.match(source, /placeholder=\{t\.raw\('form\.paramOverride\.placeholder'\)\}/);
    assert.doesNotMatch(source, /md:col-span-2/);
});
