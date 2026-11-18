import test from 'node:test';
import assert from 'node:assert/strict';
import fileSystem from 'node:fs';
import virtualMachine from 'node:vm';
import { createRequire } from 'node:module';
import typescript from 'typescript';
import { renderToStaticMarkup } from 'react-dom/server';
import { createTranslator } from 'next-intl';

const require = createRequire(import.meta.url);
const readSource = (relativePath) => fileSystem.readFileSync(new URL(relativePath, import.meta.url), 'utf8');
const plainValue = (value) => JSON.parse(JSON.stringify(value));

function evaluateSource(source, dependencies = {}) {
    const evaluatedModule = { exports: {} };
    const compiled = typescript.transpileModule(source, {
        compilerOptions: {
            module: typescript.ModuleKind.CommonJS,
            target: typescript.ScriptTarget.ES2022,
            jsx: typescript.JsxEmit.ReactJSX,
        },
    });
    virtualMachine.runInNewContext(compiled.outputText, {
        ...globalThis,
        Blob,
        FormData,
        Headers,
        Response,
        URL,
        URLSearchParams,
        console,
        fetch: dependencies.fetch ?? globalThis.fetch,
        module: evaluatedModule,
        exports: evaluatedModule.exports,
        require: (specifier) => dependencies[specifier] ?? require(specifier),
    });
    return evaluatedModule.exports;
}

function collectElements(element, predicate) {
    if (Array.isArray(element)) return element.flatMap((child) => collectElements(child, predicate));
    if (!element || typeof element !== 'object' || !element.props) return [];
    return [
        ...(predicate(element) ? [element] : []),
        ...collectElements(element.props.children, predicate),
    ];
}

function createBackupFixture({ locale = 'en', previewResult = null } = {}) {
    const calls = { preview: [], import: [] };
    const toasts = [];
    let previewData = null;
    let nextImport = Promise.resolve({ rows_affected: {}, progress: [] });

    const previewDB = {
        isPending: false,
        get data() { return previewData; },
        reset() { previewData = null; },
        async mutateAsync(input) {
            calls.preview.push(plainValue(input));
            previewData = previewResult;
            return previewResult;
        },
    };
    const importDB = {
        isPending: false,
        data: undefined,
        async mutateAsync(input) {
            calls.import.push(plainValue(input));
            return nextImport;
        },
    };
    const exportDB = { isPending: false, mutateAsync: async () => ({}) };

    const hookValues = [];
    let hookIndex = 0;
    const useState = (initialValue) => {
        const stateIndex = hookIndex++;
        if (!(stateIndex in hookValues)) {
            hookValues[stateIndex] = typeof initialValue === 'function' ? initialValue() : initialValue;
        }
        return [hookValues[stateIndex], (nextValue) => {
            hookValues[stateIndex] = typeof nextValue === 'function'
                ? nextValue(hookValues[stateIndex])
                : nextValue;
        }];
    };
    const useRef = (initialValue) => {
        const refIndex = hookIndex++;
        if (!(refIndex in hookValues)) hookValues[refIndex] = { current: initialValue };
        return hookValues[refIndex];
    };

    const messages = JSON.parse(readSource(`../public/locale/${locale}.json`));
    const translate = createTranslator({
        locale: locale.replace('_', '-'),
        messages,
        namespace: 'setting',
        onError: (error) => { throw error; },
    });

    const componentModule = evaluateSource(readSource('../src/components/modules/setting/Backup.tsx'), {
        react: {
            useState,
            useRef,
            useMemo: (factory) => {
                hookIndex++;
                return factory();
            },
        },
        'next-intl': { useTranslations: () => translate },
        'lucide-react': new Proxy({}, { get: () => 'span' }),
        '@/components/ui/button': { Button: 'button' },
        '@/components/ui/hint': { Hint: 'span' },
        '@/components/ui/input': { Input: 'input' },
        '@/components/ui/switch': { Switch: 'button' },
        '@/components/common/Toast': {
            toast: {
                error: (message) => toasts.push({ kind: 'error', message }),
                success: (message) => toasts.push({ kind: 'success', message }),
            },
        },
        '@/api/endpoints/setting': {
            useExportDB: () => exportDB,
            useImportDB: () => importDB,
            usePreviewImportDB: () => previewDB,
        },
        '@/components/ui/progress': { Progress: 'div' },
        '@/lib/utils': { cn: (...values) => values.filter(Boolean).join(' ') },
    });

    const render = () => {
        hookIndex = 0;
        return componentModule.SettingBackup();
    };
    const elements = (predicate) => collectElements(render(), predicate);
    const control = (id) => {
        const element = elements((candidate) => candidate.props.id === id)[0];
        assert.ok(element, `missing control ${id}`);
        return element;
    };

    return {
        ...componentModule,
        calls,
        toasts,
        render,
        control,
        elements,
        markup: () => renderToStaticMarkup(render()),
        pickFile() {
            const input = elements((element) => element.type === 'input' && element.props.type === 'file')[0];
            assert.ok(input, 'missing backup file input');
            input.props.onChange({ target: { files: [{ name: 'backup.json' }] } });
        },
        changeMapping(value) {
            control('backup-channel-mapping').props.onChange({ target: { value } });
        },
        setPreviewResult(result) {
            previewResult = result;
        },
        failNextImport(error) {
            nextImport = Promise.reject(error);
            nextImport.catch(() => {});
        },
    };
}

test('channel mapping parser accepts only a JSON object of positive integer IDs', () => {
    const fixture = createBackupFixture();
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('')), { ok: true });
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('{"12": 34, "18": 41}')), {
        ok: true,
        mapping: { 12: 34, 18: 41 },
    });
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('not-json')), { ok: false, reason: 'invalidJson' });
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('[]')), { ok: false, reason: 'objectRequired' });
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('{"0": 2}')), { ok: false, reason: 'invalidSource' });
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('{"12": 0}')), { ok: false, reason: 'invalidTarget' });
    assert.deepEqual(plainValue(fixture.parseChannelMappingInput('{"01": 2}')), { ok: false, reason: 'invalidSource' });
});

test('preview and import receive the explicit source-to-target mapping without mutating the draft', async () => {
    const fixture = createBackupFixture({
        previewResult: {
            version: 6,
            mode: 'incremental',
            sections: ['channels', 'group_items'],
            channel_mappings: [{
                source_channel_id: 12,
                target_channel_id: 34,
                reason: 'explicit_mapping',
            }],
        },
    });
    fixture.pickFile();
    fixture.changeMapping('{"12": 34}');
    await fixture.control('backup-preview').props.onClick();

    assert.deepEqual(fixture.calls.preview[0].channelMapping, { 12: 34 });
    assert.equal(fixture.calls.preview[0].mode, 'incremental');
    assert.match(fixture.markup(), /12 → 34/);
    assert.equal(fixture.control('backup-channel-mapping').props.value, '{"12": 34}');

    fixture.failNextImport(new Error('legacy collision still unresolved'));
    await fixture.control('backup-import').props.onClick();
    assert.deepEqual(fixture.calls.import[0].channelMapping, { 12: 34 });
    assert.equal(fixture.control('backup-channel-mapping').props.value, '{"12": 34}');
    assert.ok(fixture.toasts.some((entry) => entry.kind === 'error'));
});

test('invalid mapping is rejected inline before either API call', async () => {
    const fixture = createBackupFixture();
    fixture.pickFile();
    fixture.changeMapping('{"12": 0}');
    await fixture.control('backup-preview').props.onClick();
    await fixture.control('backup-import').props.onClick();

    assert.equal(fixture.calls.preview.length, 0);
    assert.equal(fixture.calls.import.length, 0);
    assert.equal(fixture.control('backup-channel-mapping').props['aria-invalid'], true);
    assert.match(fixture.markup(), /positive integer target channel ID/);
});

test('full-restore preview names the manifest sections that will be replaced', async () => {
    const fixture = createBackupFixture({
        previewResult: {
            version: 6,
            mode: 'full',
            sections: ['channels', 'group_items'],
            channel_mappings: [],
        },
    });
    fixture.pickFile();
    fixture.control('backup-mode-full').props.onClick();
    await fixture.control('backup-preview').props.onClick();

    const markup = fixture.markup();
    assert.match(markup, /only the non-null sections explicitly listed/);
    assert.match(markup, /these manifest sections will be replaced or cleared/);
    assert.match(markup, /channels, group_items/);
});

test('all locales render the mapping editor and manifest warning without raw keys', () => {
    for (const locale of ['en', 'zh_hans', 'zh_hant']) {
        const fixture = createBackupFixture({ locale });
        fixture.control('backup-mode-full').props.onClick();
        const markup = fixture.markup();
        assert.doesNotMatch(markup, /setting\.backup\./);
        assert.match(markup, /backup-channel-mapping/);
        assert.match(markup, /Full Restore|完整恢复|完整復原/);
    }
});

test('both import API paths encode channel_mapping and existing calls remain valid', async () => {
    const requests = [];
    const hooks = evaluateSource(readSource('../src/api/endpoints/setting.ts'), {
        '@tanstack/react-query': {
            useQuery: (options) => options,
            useMutation: (options) => options,
            useQueryClient: () => ({ invalidateQueries: async () => {} }),
        },
        '../client': { apiClient: {}, API_BASE_URL: 'https://octopus.example' },
        '../constants': { REFETCH_INTERVAL_DEFAULT: 0 },
        '@/lib/logger': { logger: { error: () => {}, log: () => {} } },
        './user': { useAuthStore: { getState: () => ({ token: 'fixture-token' }) } },
        fetch: async (url) => {
            const request = { url: String(url) };
            requests.push(request);
            if (request.url.includes('preview=true')) {
                return new Response(JSON.stringify({ data: { version: 6, mode: 'incremental', sections: [] } }), {
                    status: 200,
                    headers: { 'content-type': 'application/json' },
                });
            }
            return new Response(JSON.stringify({ data: { rows_affected: {}, progress: [] } }), {
                status: 200,
                headers: { 'content-type': 'application/json' },
            });
        },
    });

    const file = new Blob(['{}'], { type: 'application/json' });
    await hooks.usePreviewImportDB().mutationFn({
        file,
        mode: 'incremental',
        channelMapping: { 12: 34 },
    });
    await hooks.useImportDB().mutationFn({
        file,
        mode: 'full',
        channelMapping: { 12: 34, 18: 41 },
    });
    await hooks.useImportDB().mutationFn({ file, mode: 'incremental' });

    const previewURL = new URL(requests[0].url);
    const importURL = new URL(requests[1].url);
    const legacyURL = new URL(requests[2].url);
    assert.equal(previewURL.searchParams.get('preview'), 'true');
    assert.equal(previewURL.searchParams.get('channel_mapping'), '{"12":34}');
    assert.equal(importURL.searchParams.get('mode'), 'full');
    assert.equal(importURL.searchParams.get('channel_mapping'), '{"12":34,"18":41}');
    assert.equal(legacyURL.searchParams.has('channel_mapping'), false);
    assert.throws(() => hooks.serializeChannelMapping({ 0: 2 }), /positive integer/);
});
