import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { MutationObserver, QueryClient } from '@tanstack/react-query';
import ts from 'typescript';

const require = createRequire(import.meta.url);
const readSource = (relativePath) => fs.readFileSync(new URL(relativePath, import.meta.url), 'utf8');

function evaluateSource(source, dependencies = {}) {
    const evaluatedModule = { exports: {} };
    const compiled = ts.transpileModule(source, {
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX },
    });
    vm.runInNewContext(compiled.outputText, {
        module: evaluatedModule,
        exports: evaluatedModule.exports,
        require: (specifier) => dependencies[specifier] ?? require(specifier),
        queueMicrotask,
    });
    return evaluatedModule.exports;
}

const settingsSource = ts.createSourceFile('setting.ts', readSource('../src/api/endpoints/setting.ts'), ts.ScriptTarget.Latest, true);
const settingsDeclaration = settingsSource.statements.find((statement) => ts.isVariableStatement(statement)
    && statement.declarationList.declarations.some((declaration) => declaration.name.getText(settingsSource) === 'SettingKey'));
const { SettingKey } = evaluateSource(settingsDeclaration.getText(settingsSource));

function collectElements(element, predicate) {
    if (Array.isArray(element)) return element.flatMap((child) => collectElements(child, predicate));
    if (!element || typeof element !== 'object' || !element.props) return [];
    return [
        ...(predicate(element) ? [element] : []),
        ...collectElements(element.props.children, predicate),
    ];
}

function createSettingsFixture(testContext, componentName = 'RequestFilter', initialValues = {}) {
    let settings = Object.entries({
        [SettingKey.RequestFilterEnabled]: 'false',
        [SettingKey.RequestFilterKeywords]: '[]',
        [SettingKey.RequestFilterErrorMessage]: 'Local rejection',
        ...initialValues,
    }).map(([key, value]) => ({ key, value }));
    const requests = [];
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false, gcTime: Infinity } } });
    const observer = new MutationObserver(queryClient, {
        mutationFn: (variables) => new Promise((resolve, reject) => {
            requests.push({
                key: variables.key,
                value: variables.value,
                succeed: () => resolve(variables),
                fail: () => reject(new Error('Fixture save failure')),
            });
        }),
    });
    const unsubscribe = observer.subscribe(() => {});
    testContext.after(() => { unsubscribe(); queryClient.clear(); });

    // Execute the actual component with persistent hooks and the real mutation
    // observer, without a browser, network, or a second copy of its save logic.
    const hookValues = [];
    let hookIndex = 0;
    const effects = [];
    const useState = (initialValue) => {
        const stateIndex = hookIndex++;
        if (!(stateIndex in hookValues)) hookValues[stateIndex] = initialValue;
        return [hookValues[stateIndex], (nextValue) => {
            hookValues[stateIndex] = typeof nextValue === 'function' ? nextValue(hookValues[stateIndex]) : nextValue;
        }];
    };
    const componentModule = evaluateSource(readSource(`../src/components/modules/setting/${componentName}.tsx`), {
        react: {
            useState,
            useRef: (initialValue) => useState({ current: initialValue })[0],
            useId: () => 'filter-fixture',
            useEffect: (callback, dependencies) => {
                const effectIndex = hookIndex++;
                const previousDependencies = hookValues[effectIndex];
                if (!previousDependencies || dependencies.some((value, index) => value !== previousDependencies[index])) {
                    effects.push(callback);
                }
                hookValues[effectIndex] = dependencies;
            },
        },
        'next-intl': { useTranslations: () => (key) => key },
        'lucide-react': { ShieldAlert: 'svg', ListFilter: 'svg', MessageSquareWarning: 'svg', X: 'svg', RotateCcw: 'svg' },
        '@/components/ui/input': { Input: 'input' },
        '@/components/ui/switch': { Switch: 'switch' },
        '@/components/ui/badge': { Badge: 'span' },
        '@/components/ui/button': { Button: 'button' },
        '@/components/ui/hint': { Hint: 'span' },
        '@/components/ui/select': { Select: 'select', SelectContent: 'div', SelectItem: 'option', SelectTrigger: 'button', SelectValue: 'span' },
        '@/components/common/Toast': { toast: { success: () => {}, error: () => {} } },
        './runtime-settings': { RETRY_FIELDS: [] },
        '@/api/endpoints/setting': {
            SettingKey,
            useSettingList: () => ({ data: settings }),
            useSetSetting: () => ({
                mutate: (variables, callbacks) => { observer.mutate(variables, callbacks).catch(() => {}); },
                mutateAsync: (variables) => observer.mutate(variables),
            }),
        },
    });
    const render = () => {
        hookIndex = 0;
        const rendered = componentModule[`Setting${componentName}`]();
        for (const effect of effects.splice(0)) effect();
        return rendered;
    };
    const findElements = (predicate) => collectElements(render(), predicate);
    const control = (suffix) => findElements((element) => element.props.id?.endsWith(`-${suffix}`))[0];
    return {
        requests,
        control,
        findElements,
        async settle() { render(); await nextTurn(); render(); await nextTurn(); },
        refresh(values = {}) { settings = settings.map((setting) => ({ ...setting, value: values[setting.key] ?? setting.value })); },
        toggle(checked) { control('enabled').props.onCheckedChange(checked); },
        addKeyword(keyword) {
            control('keyword').props.onChange({ target: { value: keyword } });
            control('keyword').props.onKeyDown({ key: 'Enter', nativeEvent: { isComposing: false }, preventDefault() {} });
        },
    };
}

test('saving different filter fields does not strand queued keyword edits', async (testContext) => {
    const fixture = createSettingsFixture(testContext);
    await fixture.settle();
    fixture.addKeyword('health probe');
    fixture.toggle(true);
    await fixture.settle();
    fixture.addKeyword('another probe');
    assert.equal(fixture.requests.length, 2);
    fixture.requests[0].succeed();
    fixture.requests[1].succeed();
    await fixture.settle();
    assert.equal(fixture.requests.length, 3, 'the newer keyword list must be saved after the first list');
    assert.equal(fixture.requests[2].value, '["health probe","another probe"]');
    fixture.requests[2].succeed();
    await fixture.settle();
});

test('stale refresh cannot erase a queued toggle back to the original value', async (testContext) => {
    const fixture = createSettingsFixture(testContext);
    await fixture.settle();
    fixture.toggle(true);
    await fixture.settle();
    fixture.toggle(false);
    fixture.refresh();
    await fixture.settle();
    fixture.requests[0].succeed();
    await fixture.settle();
    assert.equal(fixture.requests.length, 2, 'turning back off must reach the server even when a refresh still says off');
    assert.equal(fixture.requests[1].value, 'false');
    fixture.requests[1].succeed();
    await fixture.settle();
    assert.equal(fixture.control('enabled').props.checked, false);
});

test('failed keyword save rolls back and can be retried while another field saves', async (testContext) => {
    const fixture = createSettingsFixture(testContext);
    await fixture.settle();
    fixture.addKeyword('health probe');
    fixture.toggle(true);
    await fixture.settle();
    fixture.requests[0].fail();
    fixture.requests[1].succeed();
    await fixture.settle();
    assert.equal(fixture.findElements((element) => element.type === 'li').length, 0);
    assert.equal(fixture.findElements((element) => element.props.role === 'alert').length, 1);
    fixture.addKeyword('health probe');
    await fixture.settle();
    assert.equal(fixture.requests.length, 3, 'a failed rule must remain retryable');
    fixture.requests[2].succeed();
    await fixture.settle();
});

test('background refresh preserves an error-message draft until blur saves it', async (testContext) => {
    const fixture = createSettingsFixture(testContext);
    await fixture.settle();
    fixture.control('error-message').props.onChange({ target: { value: 'Custom local rejection' } });
    fixture.refresh();
    await fixture.settle();
    assert.equal(fixture.control('error-message').props.value, 'Custom local rejection');
    assert.equal(fixture.requests.length, 0);
    fixture.control('error-message').props.onBlur();
    await fixture.settle();
    assert.equal(fixture.requests[0].value, 'Custom local rejection');
    fixture.requests[0].succeed();
    await fixture.settle();
});

test('truncation retry switch reads persisted values on mount and refresh', async (testContext) => {
    const fixture = createSettingsFixture(testContext, 'Retry', { [SettingKey.RetryTruncationEnabled]: 'true' });
    const truncationSwitch = () => fixture.findElements((element) => element.props['aria-label'] === 'retry.truncation.label')[0];
    await fixture.settle();
    assert.equal(truncationSwitch().props.checked, true);
    fixture.refresh();
    await fixture.settle();
    assert.equal(truncationSwitch().props.checked, true);
    truncationSwitch().props.onCheckedChange(false);
    await fixture.settle();
    assert.equal(fixture.requests[0].key, SettingKey.RetryTruncationEnabled);
    assert.equal(fixture.requests[0].value, 'false');
    fixture.requests[0].succeed();
    fixture.refresh({ [SettingKey.RetryTruncationEnabled]: 'false' });
    await fixture.settle();
    assert.equal(truncationSwitch().props.checked, false);
});
