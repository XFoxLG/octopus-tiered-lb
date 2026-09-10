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

test('outbound inherit selection is visible and maps back to the persisted empty value', () => {
    const source = parseSource('../src/components/modules/channel/Form.tsx');
    const select = collectNodes(source, (node) => ts.isJsxElement(node)
        && node.openingElement.tagName.getText(source) === 'Select'
        && node.openingElement.getText(source).includes('formData.outbound_format_override'))[0];
    const changes = [];
    const { render } = evaluateSource(`export function render(formData) { return ${select.getText(source)}; }`, {
        Select: 'select', SelectTrigger: 'button', SelectValue: 'span', SelectContent: 'div', SelectItem: 'option',
        idPrefix: 'channel', t: (key) => key, onFormDataChange: (value) => changes.push(value),
    });
    for (const persistedValue of ['', 'chat_only', 'responses_only']) {
        const rendered = render({ outbound_format_override: persistedValue, name: 'Unchanged' });
        assert.equal(rendered.props.value, persistedValue || 'inherit');
        const items = rendered.props.children[1].props.children;
        assert.deepEqual(Array.from(items, (item) => item.props.value), ['inherit', 'chat_only', 'responses_only']);
        assert.equal(rendered.props.children[0].props.children.props.placeholder, 'outboundFormatOverrideFollowGroup');
        for (const selectedValue of ['inherit', 'chat_only', 'responses_only']) {
            rendered.props.onValueChange(selectedValue);
            assert.equal(changes.at(-1).outbound_format_override, selectedValue === 'inherit' ? '' : selectedValue);
            assert.equal(changes.at(-1).name, 'Unchanged');
        }
    }
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
