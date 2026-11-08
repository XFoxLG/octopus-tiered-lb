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


function editorFixture(relativePath) {
    const sourceFile = parseSource(relativePath);
    const inline = sourceFile.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === 'EditDialogContent');
    const sharedSource = readSource('../src/components/modules/group/EditDialogContent.tsx');
    const states = [], refs = [], dependencies = [], effects = [];
    let hook = 0, isOpen = true;
    const react = {
        useState(initial) {
            const index = hook++;
            if (!(index in states)) states[index] = typeof initial === 'function' ? initial() : initial;
            return [states[index], value => { states[index] = typeof value === 'function' ? value(states[index]) : value; }];
        },
        useRef(initial) { const index = hook++; return refs[index] ??= { current: initial }; },
        useId() { return 'test-editor-view'; },
        useCallback(callback) { return callback; },
        useEffect(effect, deps) {
            const index = hook++;
            if (!dependencies[index] || deps.some((value, i) => !Object.is(value, dependencies[index][i]))) {
                dependencies[index] = deps; effects.push(effect);
            }
        },
    };
    const globals = {
        ...react,
        useMorphingDialog: () => ({ isOpen, setIsOpen: next => { isOpen = next; } }),
        useTranslations: () => key => key,
        cn: (...values) => values.filter(Boolean).join(' '),
        GroupEditor: 'form', AIRouteButton: 'button', AvailabilityResultsPanel: 'section', Button: 'button',
        MorphingDialogTitle: 'header', MorphingDialogClose: 'button', MorphingDialogDescription: 'main',
        AlertDialog: 'aside', AlertDialogContent: 'div', AlertDialogHeader: 'div', AlertDialogTitle: 'h3',
        AlertDialogDescription: 'p', AlertDialogFooter: 'footer', AlertDialogCancel: 'button', AlertDialogAction: 'button',
        Loader2: 'i', Pencil: 'i', TestTubeDiagonal: 'i', Trash2: 'i', Waves: 'i',
        normalizeEndpointType: value => value, supportsGroupTest: value => ['chat', 'embeddings', 'rerank'].includes(value),
    };
    const loaded = evaluateSource(inline ? 'export ' + inline.getText(sourceFile) : sharedSource, {
        ...globals,
        react,
        'next-intl': { useTranslations: globals.useTranslations },
        'lucide-react': globals,
        '@/components/ui/button': { Button: globals.Button },
        '@/components/ui/alert-dialog': globals,
        '@/components/ui/morphing-dialog': globals,
        '@/lib/utils': { cn: globals.cn },
        './AIRouteButton': { AIRouteButton: globals.AIRouteButton },
        './AvailabilityResultsPanel': { AvailabilityResultsPanel: globals.AvailabilityResultsPanel },
        './Editor': { GroupEditor: globals.GroupEditor },
        './utils': globals,
    });
    return {
        render(props) { hook = 0; effects.length = 0; let tree = loaded.EditDialogContent(props); for (const effect of effects.splice(0)) effect(); hook = 0; tree = loaded.EditDialogContent(props); return tree; },
        setOpen(value) { isOpen = value; },
        reset() { states.length = 0; refs.length = 0; dependencies.length = 0; },
    };
}

function treeNodes(tree, predicate, ancestors = []) {
    if (!tree || typeof tree !== 'object') return [];
    if (Array.isArray(tree)) return tree.flatMap(child => treeNodes(child, predicate, ancestors));
    const matches = predicate(tree) ? [{ node: tree, ancestors }] : [];
    return [...matches, ...treeNodes(tree.props?.children, predicate, [...ancestors, tree])];
}
function visibleEditor(tree) {
    return treeNodes(tree, node => node.type === 'form').find(({ ancestors }) => ancestors.every(node => !String(node.props?.className ?? '').split(/\s+/).includes('hidden') && node.props?.['aria-hidden'] !== true && node.props?.inert !== true));
}
const completedEditorProps = {
    group: { id: 7, name: 'deepseek-v4-flash', endpoint_type: 'chat', mode: 5, items: [] },
    editMembers: [], isSubmitting: false, onSubmit() {}, onTestAvailability() {}, onRemoveFailedModels() {},
    isTestingAvailability: false, canRemoveFailedModels: false, testTaskId: 'completed-task',
    testProgressCompleted: 2, testProgressTotal: 2, testProgressValue: 100,
    testResults: [{ item_id: 1, channel_id: 1, channel_name: 'Primary', model_name: 'alpha', passed: false, attempts: 1, status_code: 400 }],
    availabilitySummary: { unavailableCount: 2, availableCount: 0, allAvailable: false, fullyMatched: false },
};
for (const [label, relativePath] of [['card', '../src/components/modules/group/Card.tsx'], ['list', '../src/components/modules/group/GroupListItem.tsx']]) {
    test(label + ' edit entry remains editable after completed failed tests on an empty group', () => {
        const fixture = editorFixture(relativePath);
        const tree = fixture.render(completedEditorProps);
        assert.ok(visibleEditor(tree), 'completed availability results must not hide the editor');
        assert.equal(treeNodes(tree, node => node.type === 'form')[0].node.props.initial.members.length, 0);
    });
}


test('testing and result navigation keep the editor mounted and preserve its draft', () => {
    const fixture = editorFixture('../src/components/modules/group/GroupListItem.tsx');
    let testsStarted = 0;
    const props = { ...completedEditorProps, editMembers: [{ id: '1:alpha', channel_id: 1, channel_name: 'Primary', name: 'alpha', enabled: true }], onTestAvailability() { testsStarted++; } };
    let tree = fixture.render(props);
    const form = treeNodes(tree, node => node.type === 'form')[0].node;
    assert.ok(visibleEditor(tree));
    const testButton = treeNodes(tree, node => node.type === 'button' && Array.isArray(node.props.children) && node.props.children.includes('detail.availability.testAll'))[0].node;
    testButton.props.onClick();
    tree = fixture.render({ ...props, isTestingAvailability: true, testTaskId: 'new-task' });
    assert.equal(testsStarted, 1);
    assert.equal(visibleEditor(tree), undefined);
    const hiddenForm = treeNodes(tree, node => node.type === 'form')[0].node;
    assert.equal(hiddenForm.key, form.key);
    assert.equal(treeNodes(tree, node => node.type === 'form').length, 1, 'switching views must not unmount or duplicate the editor');
    const editTab = treeNodes(tree, node => node.props?.role === 'tab' && node.props.id.endsWith('-edit-tab'))[0].node;
    editTab.props.onClick();
    tree = fixture.render({ ...props, isTestingAvailability: true, testTaskId: 'new-task' });
    assert.ok(visibleEditor(tree), 'ongoing test updates must not force the user back to results');
    tree = fixture.render({ ...props, isTestingAvailability: false, testTaskId: 'new-task', testProgressCompleted: 2 });
    assert.ok(visibleEditor(tree), 'completed test updates must not hide the editor');
    fixture.reset();
    assert.ok(visibleEditor(fixture.render(props)), 'reopening must begin in the editing view');
});

test('shared editor preserves explicit zero, false and empty advanced configuration', () => {
    const fixture = editorFixture('../src/components/modules/group/Card.tsx');
    const group = { ...completedEditorProps.group, hedge_delay_ms: 0, request_timeout_seconds: 0, relay_retry_count: -1, relay_route_retries: -1, stream_keepalive_enabled: false, reasoning_force_override: false, default_reasoning_effort: '', param_override: '', custom_header: [] };
    const initial = treeNodes(fixture.render({ ...completedEditorProps, group }), node => node.type === 'form')[0].node.props.initial;
    assert.equal(initial.hedge_delay_ms, 0);
    assert.equal(initial.request_timeout_seconds, 0);
    assert.equal(initial.relay_retry_count, -1);
    assert.equal(initial.relay_route_retries, -1);
    assert.equal(initial.stream_keepalive_enabled, false);
    assert.equal(initial.reasoning_force_override, false);
    assert.equal(initial.default_reasoning_effort, '');
    assert.equal(initial.param_override, '');
    assert.equal(initial.custom_header.length, 0);
});

test('draft tests freeze the started member snapshot and failed removal keeps unsaved draft fields', () => {
    const shared = readSource('../src/components/modules/group/EditDialogContent.tsx');
    assert.match(shared, /const \[testedMembers, setTestedMembers\] = useState<SelectedMember\[\]>\(editMembers\)/);
    assert.match(shared, /setTestedMembers\(\(draft\?\.members \?\? editMembers\)\.map/);
    assert.match(shared, /members=\{testedMembers\}/);
    assert.match(shared, /onRemoveFailedModels\(draftRef\.current \?\? undefined\)/);
    for (const relativePath of ['../src/components/modules/group/Card.tsx', '../src/components/modules/group/GroupListItem.tsx']) {
        const source = readSource(relativePath);
        assert.match(source, /draft\s*\?\s*\{ \.\.\.draft, members: nextMembers \}/);
    }
    const panel = readSource('../src/components/modules/group/AvailabilityResultsPanel.tsx');
    assert.match(panel, /source: \{result\.source\}/);
    assert.match(panel, /protocol: \{result\.protocol\}/);
    assert.match(panel, /endpoint: \{result\.endpoint_id\}/);
    assert.match(panel, /snapshot: \{result\.snapshot_hash/);
});


const editorUtils = evaluateSource(readSource('../src/components/modules/group/utils.ts'), { './capabilities.ts': {} });
function groupSettingsFixture(initial) {
    const source = parseSource('../src/components/modules/group/Editor.tsx');
    const declaration = source.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === 'GroupEditor');
    const states = []; let index = 0, draft;
    const globals = {
        useState(value) { const key=index++; if (!(key in states)) states[key]=typeof value==='function'?value():value; return [states[key], next=>{states[key]=typeof next==='function'?next(states[key]):next;}]; },
        useMemo: callback=>callback(), useCallback: callback=>callback, useEffect: callback=>callback(),
        useTranslations: ()=>Object.assign(key=>key,{raw:key=>key}), useModelChannelList: ()=>({data:[]}),
        GroupMode:{Auto:5}, ...editorUtils,
        dedupeSelectedMembers: members=>members,
        cn:(...values)=>values.filter(Boolean).join(' '),
    };
    for (const name of ['Field','FieldLabel','FieldGroup','Input','Switch','Hint','Button','Accordion','AccordionItem','AccordionTrigger','AccordionContent','TooltipProvider','Tooltip','TooltipTrigger','TooltipContent','HelpCircle','SlidersHorizontal','Waves','Sparkles','FlaskConical','Plus','Trash2','ModelPickerSection','SortSection']) globals[name]=name;
    const loaded=evaluateSource('export '+declaration.getText(source),globals);
    return {
        render() { index=0;return loaded.GroupEditor({initial,submitText:'save',submittingText:'saving',isSubmitting:false,onSubmit:()=>{},onDraftChange:value=>{draft=value;}}); },
        draft:()=>draft,
    };
}
function byId(tree,id) { return treeNodes(tree,node=>node.props?.id===id)[0]?.node; }

test('category changes hide chat fields without clearing the complete draft',()=>{
    const fixture=groupSettingsFixture({name:'example',endpoint_type:'chat',members:[],default_reasoning_effort:'high',reasoning_force_override:true,stream_keepalive_enabled:true,assistant_prefill_compat:true,forward_mode:'raw',outbound_format:'messages_only',param_override:'{"temperature":0}',custom_header:[{header_key:'X-Test',header_value:'saved'}],hedge_delay_ms:0});
    let tree=fixture.render(); const before=fixture.draft();
    assert.ok(byId(tree,'group-default-reasoning-effort'));
    byId(tree,'group-endpoint-type').props.onChange({target:{value:'embeddings'}});
    tree=fixture.render();
    for(const id of ['group-default-reasoning-effort','group-reasoning-force-override','group-stream-keepalive','group-stream-idle-timeout','group-assistant-prefill','group-hedge-enabled','group-param-override']) assert.equal(byId(tree,id),undefined,id);
    assert.equal(byId(tree,'group-first-token-time-out').props.disabled,false);
    assert.equal(byId(tree,'group-attempt-time-out').props.disabled,false);
    const expected={...before,endpoint_type:'embeddings'};
    assert.deepEqual(JSON.parse(JSON.stringify(fixture.draft())),JSON.parse(JSON.stringify(expected)));
    assert.equal(treeNodes(tree,node=>node.props?.role==='alert').length,1,'legacy protocol conflict must remain visible');
    byId(tree,'group-endpoint-type').props.onChange({target:{value:'rerank'}});
    tree=fixture.render();
    assert.equal(byId(tree,'group-attempt-time-out').props.disabled,true);
    assert.equal(byId(tree,'group-first-token-time-out').props.disabled,true);
    assert.equal(byId(tree,'group-request-timeout').props.disabled,undefined);
    assert.equal(treeNodes(tree,node=>node.props?.value==='X-Test')[0].node.props.disabled,false,'rerank headers execute on the runtime path');
    assert.equal(fixture.draft().default_reasoning_effort,'high');
    byId(tree,'group-endpoint-type').props.onChange({target:{value:'chat'}});
    tree=fixture.render();
    assert.equal(byId(tree,'group-default-reasoning-effort').props.value,'high');
    assert.equal(byId(tree,'group-stream-keepalive').props.checked,true);
    assert.equal(byId(tree,'group-hedge-delay-ms').props.value,'0');
    assert.deepEqual(JSON.parse(JSON.stringify(fixture.draft())),JSON.parse(JSON.stringify(before)));
});

test('embedding protocol reset is an explicit draft-only action',()=>{
    const fixture=groupSettingsFixture({name:'example',endpoint_type:'embeddings',forward_mode:'passthrough',outbound_format:'chat_only',default_reasoning_effort:'high',members:[]});
    let tree=fixture.render();
    const button=treeNodes(tree,node=>node.type==='Button' && node.props.children==='form.categoryFields.resetEmbeddingProtocol')[0].node;
    assert.equal(fixture.draft().forward_mode,'passthrough');
    button.props.onClick();tree=fixture.render();
    assert.equal(fixture.draft().forward_mode,'convert');
    assert.equal(fixture.draft().outbound_format,'');
    assert.equal(fixture.draft().default_reasoning_effort,'high');
    assert.equal(treeNodes(tree,node=>node.props?.role==='alert').length,0);
});

test('draft test action follows current category and rejects unsupported media',()=>{
    const fixture=editorFixture('../src/components/modules/group/GroupListItem.tsx');let sent=0;
    const props={...completedEditorProps,onTestAvailability(){sent++;}};
    let tree=fixture.render(props);const oldButton=treeNodes(tree,node=>node.type==='button' && Array.isArray(node.props.children) && node.props.children.includes('detail.availability.testAll'))[0].node;
    treeNodes(tree,node=>node.type==='form')[0].node.props.onDraftChange({endpoint_type:'image_generation',members:[{id:'x'}]});
    tree=fixture.render(props);
    assert.equal(treeNodes(tree,node=>node.type==='button' && Array.isArray(node.props.children) && node.props.children.includes('detail.availability.testAll')).length,0);
    oldButton.props.onClick();assert.equal(sent,0,'stale click must not probe chat for a media draft');
    treeNodes(tree,node=>node.type==='form')[0].node.props.onDraftChange({endpoint_type:'rerank',members:[{id:'x'}]});
    tree=fixture.render(props);
    assert.equal(treeNodes(tree,node=>node.type==='button' && Array.isArray(node.props.children) && node.props.children.includes('detail.availability.testAll')).length,1);
});
