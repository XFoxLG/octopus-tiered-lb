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
        compilerOptions: { module: typescript.ModuleKind.CommonJS, target: typescript.ScriptTarget.ES2022, jsx: typescript.JsxEmit.ReactJSX },
    });
    virtualMachine.runInNewContext(compiled.outputText, {
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

function savedConfig(overrides = {}) {
    return {
        type: 'redis',
        redis: { addr: 'stored.example:6380', db: 8, tls: true, pool_size: 5, dial_timeout: '3s', read_timeout: '3s' },
        has_saved_connection: true,
        has_password: true,
        config_source: 'database',
        runtime_backend: 'redis',
        runtime_healthy: true,
        runtime_tls: true,
        restart_needed: false,
        reconnecting: false,
        ...overrides,
    };
}

const configHelpers = evaluateSource(readSource('../src/components/modules/setting/cache-config.ts'));

function createCacheFixture({ config = savedConfig(), locale = 'en', queryState = {} } = {}) {
    let query = { data: config, isLoading: false, isError: false, ...queryState };
    const requests = [];
    const mutations = Object.fromEntries(['preview', 'test', 'save'].map((action) => [action, {
        mutateAsync: (body) => new Promise((resolve, reject) => {
            requests.push({ action, body: plainValue(body), succeed: resolve, fail: reject });
        }),
        reset() {},
    }]));

    // Run the complete component and its event handlers, retaining hook state between
    // renders. Only IO and UI primitives are substituted; request construction is real.
    const hookValues = [];
    let hookIndex = 0;
    let stateChanged = false;
    const effects = [];
    const useState = (initialValue) => {
        const stateIndex = hookIndex++;
        if (!(stateIndex in hookValues)) hookValues[stateIndex] = typeof initialValue === 'function' ? initialValue() : initialValue;
        return [hookValues[stateIndex], (nextValue) => {
            const resolvedValue = typeof nextValue === 'function' ? nextValue(hookValues[stateIndex]) : nextValue;
            stateChanged ||= !Object.is(resolvedValue, hookValues[stateIndex]);
            hookValues[stateIndex] = resolvedValue;
        }];
    };
    const messages = JSON.parse(readSource(`../public/locale/${locale}.json`));
    const translate = createTranslator({
        locale: locale.replace('_', '-'), messages, namespace: 'setting', onError: (error) => { throw error; },
    });
    const { SettingCache } = evaluateSource(readSource('../src/components/modules/setting/Cache.tsx'), {
        react: {
            useState,
            useRef: (initialValue) => useState({ current: initialValue })[0],
            useId: () => 'cache-fixture',
            useEffect: (callback, dependencies) => {
                const effectIndex = hookIndex++;
                const previousDependencies = hookValues[effectIndex];
                if (!previousDependencies || dependencies.some((value, index) => value !== previousDependencies[index])) effects.push(callback);
                hookValues[effectIndex] = dependencies;
            },
        },
        'next-intl': { useTranslations: () => translate },
        'lucide-react': { Database: 'svg', Loader2: 'svg' },
        '@/components/ui/input': { Input: 'input' },
        '@/components/ui/button': { Button: 'button' },
        './cache-config': configHelpers,
        '@/api/endpoints/setting': {
            useGetCacheConfig: () => query,
            usePreviewCacheConfig: () => mutations.preview,
            useTestCacheConnection: () => mutations.test,
            useSaveCacheConfig: () => mutations.save,
        },
    });
    const render = () => {
        let rendered;
        let renderCount = 0;
        do {
            assert.ok(renderCount++ < 10, 'component should settle without an effect loop');
            hookIndex = 0;
            stateChanged = false;
            rendered = SettingCache();
            for (const effect of effects.splice(0)) effect();
        } while (stateChanged);
        return rendered;
    };
    const elements = (predicate) => collectElements(render(), predicate);
    const control = (suffix) => {
        const element = elements((candidate) => candidate.props.id === `cache-fixture-${suffix}`)[0];
        assert.ok(element, `missing ${suffix} control`);
        return element;
    };
    return {
        requests,
        control,
        elements,
        render,
        markup: () => renderToStaticMarkup(render()),
        statuses: () => elements((element) => ['status', 'alert'].includes(element.props.role)).map(renderToStaticMarkup).join('\n'),
        change: (suffix, value) => control(suffix).props.onChange({ target: { value } }),
        check: (suffix, checked) => control(suffix).props.onChange({ target: { checked } }),
        click: (suffix) => control(suffix).props.onClick(),
        refresh: (nextQuery) => { query = { ...query, ...nextQuery }; },
    };
}

test('saved metadata is separate from blank replacement fields and legacy secrets never render', () => {
    const config = savedConfig();
    config.redis = { ...config.redis, password: 'legacy-password-secret', username: 'legacy-username-secret', ca_file: '/legacy-private/ca.pem' };
    const fixture = createCacheFixture({ config });
    for (const suffix of ['address', 'username', 'password', 'ca-file']) assert.equal(fixture.control(suffix).props.value, '');
    assert.equal(fixture.control('database').props.value, '0');
    assert.equal(fixture.control('tls').props.checked, false);
    assert.equal(fixture.control('address').props.type, 'password');
    assert.equal(fixture.control('password').props.type, 'password');
    assert.match(fixture.markup(), /stored\.example:6380; DB 8; TLS/);
    assert.doesNotMatch(fixture.markup(), /legacy-password-secret|legacy-username-secret|legacy-private/);
    assert.equal(fixture.requests.length, 0);

    for (const control of fixture.elements((element) => ['input', 'select'].includes(element.type))) {
        assert.ok(fixture.elements((element) => element.type === 'label' && element.props.htmlFor === control.props.id).length,
            `${control.props.id} must have an associated visible label`);
    }
});

test('saved connection is omitted for validation, PING and tuning-only save; edits never autosave', async () => {
    const fixture = createCacheFixture();
    fixture.change('poolSize', '12');
    fixture.change('dialTimeout', '500ms');
    fixture.change('readTimeout', '2s');
    assert.equal(fixture.requests.length, 0);
    for (const action of ['preview', 'test', 'save']) {
        const pending = fixture.click(action);
        const request = fixture.requests.at(-1);
        assert.deepEqual(request.body, { type: 'redis', tuning: { pool_size: 12, dial_timeout: '500ms', read_timeout: '2s' } });
        request.succeed(action === 'preview' ? savedConfig().redis : action === 'test' ? true : { type: 'redis', restart_needed: true });
        await pending;
    }
});

test('new redis URL disables and omits all separate identity, including an earlier manual draft', async () => {
    const fixture = createCacheFixture();
    fixture.change('address', 'manual.example:6380');
    fixture.change('username', 'manual-user');
    fixture.change('password', 'manual-secret');
    fixture.change('database', '6');
    fixture.check('tls', true);
    fixture.change('ca-file', '/manual/ca.pem');
    const address = 'redis://url-user:url-secret@new.example:6379/2';
    fixture.change('address', address);
    for (const suffix of ['username', 'password', 'database', 'tls', 'ca-file']) assert.equal(fixture.control(suffix).props.disabled, true);
    for (const suffix of ['username', 'password', 'ca-file']) assert.equal(fixture.control(suffix).props.value, '');
    assert.equal(fixture.control('tls').props.checked, false);
    const pending = fixture.click('test');
    assert.deepEqual(fixture.requests[0].body.redis, { addr: address });
    fixture.requests[0].succeed(true);
    await pending;
});

test('new manual address starts without saved credentials, DB, TLS or certificate', async () => {
    const fixture = createCacheFixture();
    fixture.change('address', 'manual.example:6379');
    const pending = fixture.click('test');
    assert.deepEqual(fixture.requests[0].body.redis, {
        addr: 'manual.example:6379', username: '', password: '', db: 0, tls: false, ca_file: '',
    });
    fixture.requests[0].succeed(true);
    await pending;
    fixture.change('username', 'new-user');
    fixture.change('password', 'new-password');
    fixture.change('database', '3');
    fixture.check('tls', true);
    fixture.change('ca-file', '/new/ca.pem');
    const nextPending = fixture.click('test');
    assert.deepEqual(fixture.requests[1].body.redis, {
        addr: 'manual.example:6379', username: 'new-user', password: 'new-password', db: 3, tls: true, ca_file: '/new/ca.pem',
    });
    fixture.requests[1].succeed(true);
    await nextPending;
});

test('rediss accepts only an explicit CA addition and clears replacement secrets after save', async () => {
    const fixture = createCacheFixture();
    const address = 'rediss://new-user:new-secret@secure.example:6380/4';
    fixture.change('address', address);
    assert.equal(fixture.control('ca-file').props.disabled, false);
    fixture.change('ca-file', '/new/ca.pem');
    const pending = fixture.click('save');
    assert.deepEqual(fixture.requests[0].body.redis, { addr: address, ca_file: '/new/ca.pem' });
    assert.equal(fixture.elements((element) => element.type === 'fieldset')[0].props.disabled, true);
    assert.equal(fixture.control('save').props.disabled, true);
    fixture.requests[0].succeed({ type: 'redis', restart_needed: true });
    await pending;
    assert.equal(fixture.control('address').props.value, '');
    assert.equal(fixture.control('ca-file').props.value, '');
    assert.doesNotMatch(fixture.markup(), /new-secret/);
    assert.match(fixture.statuses(), /saved|pending activation/);
    assert.match(fixture.statuses(), /No hot switch/);
});

test('environment and deployment sources cannot save but can identify a candidate', async () => {
    for (const source of ['environment', 'deployment']) {
        const fixture = createCacheFixture({ config: savedConfig({ config_source: source }) });
        assert.equal(fixture.control('save').props.disabled, true);
        assert.equal(fixture.control('save').props['aria-describedby'], 'cache-fixture-save-hint');
        assert.match(renderToStaticMarkup(fixture.control('save-hint')), /saving here is disabled/);
        await fixture.click('save');
        assert.equal(fixture.requests.length, 0);
        fixture.change('address', 'redis://candidate.example:6379');
        const pending = fixture.click('preview');
        assert.equal(fixture.requests[0].action, 'preview');
        fixture.requests[0].succeed({ ...savedConfig().redis, addr: 'candidate.example:6379', tls: false });
        await pending;
    }
});

test('unknown source, loading, and failed configuration reads never permit saving', async () => {
    for (const options of [
        { config: savedConfig({ config_source: undefined }) },
        { queryState: { data: undefined, isLoading: true } },
        { queryState: { isError: true } },
        { queryState: { data: undefined, isLoading: false } },
    ]) {
        const fixture = createCacheFixture(options);
        assert.equal(fixture.control('save').props.disabled, true);
        await fixture.click('save');
        assert.equal(fixture.requests.length, 0);
        assert.match(fixture.statuses(), /Loading cache|Cannot confirm/);
    }
    const fileFixture = createCacheFixture({ config: savedConfig({ config_source: 'file' }) });
    assert.equal(fileFixture.control('save').props.disabled, false);
});

test('memory is an explicit saved choice and never pretends the active Redis switched', async () => {
    const fixture = createCacheFixture();
    fixture.change('type', '');
    assert.equal(fixture.control('test').props.disabled, true);
    assert.equal(fixture.control('preview').props.disabled, true);
    const pending = fixture.click('save');
    assert.deepEqual(fixture.requests[0].body, { type: '' });
    fixture.refresh({ data: savedConfig({ type: '', restart_needed: true }) });
    fixture.requests[0].succeed({ type: '', restart_needed: true });
    await pending;
    assert.match(fixture.markup(), /Configured backend: Memory/);
    assert.match(fixture.statuses(), /Active backend: Redis \/ Valkey/);
    assert.match(fixture.markup(), /stored\.example:6380/);
});

test('a late preview cannot label a newer candidate and repeated submissions are blocked synchronously', async () => {
    const fixture = createCacheFixture();
    fixture.change('address', 'redis://first.example:6379');
    assert.equal(fixture.requests.length, 0, 'typing does not parse or save over the network');
    const previewHandler = fixture.control('preview').props.onClick;
    const pending = previewHandler();
    await previewHandler();
    assert.equal(fixture.requests.length, 1);
    for (const action of ['preview', 'test', 'save']) assert.equal(fixture.control(action).props.disabled, true);
    fixture.change('address', 'redis://second.example:6379');
    fixture.requests[0].succeed({ ...savedConfig().redis, addr: 'first.example:6379' });
    await pending;
    assert.doesNotMatch(fixture.statuses(), /first\.example|Candidate connection|Configuration parsed/);
    const nextPending = fixture.click('preview');
    fixture.requests[1].succeed({ ...savedConfig().redis, addr: 'second.example:6379', db: 11, tls: false });
    await nextPending;
    assert.match(fixture.statuses(), /second\.example:6379; DB 11; No TLS/);
    assert.match(fixture.statuses(), /No Redis connection, save, or backend switch/);
    fixture.change('poolSize', '9');
    assert.doesNotMatch(fixture.statuses(), /Candidate connection|Configuration parsed/);
});

test('PING feedback is inline, cleared by edits and does not leak exceptions or stale results', async () => {
    const fixture = createCacheFixture();
    const pending = fixture.click('test');
    fixture.requests[0].succeed(true);
    await pending;
    assert.match(fixture.statuses(), /PING succeeded/);
    fixture.change('readTimeout', '1s');
    assert.doesNotMatch(fixture.statuses(), /PING succeeded/);
    const nextPending = fixture.click('test');
    fixture.change('readTimeout', '2s');
    fixture.requests[1].fail(new Error('rediss://username:exception-secret@unsafe.example:6379'));
    await nextPending;
    assert.doesNotMatch(fixture.statuses(), /exception-secret|Validation or PING failed/);
    const failedPending = fixture.click('test');
    fixture.requests[2].fail(new Error('rediss://username:exception-secret@unsafe.example:6379'));
    await failedPending;
    assert.match(fixture.statuses(), /Validation or PING failed/);
    assert.ok(fixture.elements((element) => element.props.role === 'alert').length);
    assert.doesNotMatch(fixture.markup(), /exception-secret|unsafe\.example/);
});

test('invalid fields have linked errors and no request, including the Aiven password placeholder', async () => {
    const fixture = createCacheFixture({ config: savedConfig({ has_saved_connection: false }) });
    await fixture.click('save');
    assert.equal(fixture.requests.length, 0);
    assert.equal(fixture.control('address').props.required, true);
    assert.equal(fixture.control('address').props['aria-invalid'], true);
    assert.match(fixture.control('address').props['aria-describedby'], /address-error/);
    fixture.change('address', 'rediss://default:CLICK_TO:REVEAL_PASSWORD@aiven.example:6380');
    await fixture.click('preview');
    assert.match(fixture.statuses(), /is not a password/);
    assert.equal(fixture.requests.length, 0);
    fixture.change('address', 'manual.example:6379');
    fixture.change('database', '-1');
    fixture.change('poolSize', '1.5');
    const advancedDetails = { open: false };
    fixture.elements((element) => element.type === 'details')[0].props.ref.current = advancedDetails;
    await fixture.click('test');
    assert.equal(advancedDetails.open, true, 'invalid advanced fields must not stay hidden inside collapsed details');
    for (const suffix of ['database', 'poolSize']) {
        assert.equal(fixture.control(suffix).props['aria-invalid'], true);
        assert.equal(fixture.control(suffix).props['aria-describedby'], `cache-fixture-${suffix}-error`);
        assert.equal(fixture.control(`${suffix}-error`).props.role, 'alert');
    }
    assert.equal(fixture.requests.length, 0);
});

test('background config refresh updates diagnostics without overwriting unsaved replacement or tuning', () => {
    const fixture = createCacheFixture({ queryState: { data: undefined, isLoading: true } });
    fixture.refresh({ data: savedConfig(), isLoading: false });
    fixture.change('address', 'redis://draft.example:6379');
    fixture.change('poolSize', '17');
    fixture.refresh({ data: savedConfig({ runtime_backend: 'memory', runtime_healthy: false, reconnecting: true }) });
    assert.equal(fixture.control('address').props.value, 'redis://draft.example:6379');
    assert.equal(fixture.control('poolSize').props.value, '17');
    assert.match(fixture.statuses(), /Active backend: Memory|trying to reconnect/);
    assert.equal(fixture.requests.length, 0);
});

test('all three locales render real cache labels, server summaries and inline errors', async () => {
    for (const locale of ['en', 'zh_hans', 'zh_hant']) {
        const fixture = createCacheFixture({ locale, config: savedConfig({ has_password: false }) });
        fixture.change('address', 'manual.example:6379');
        fixture.change('password', 'CLICK_TO:REVEAL_PASSWORD');
        await fixture.click('save');
        assert.ok(fixture.control('password-error').props.children);
        fixture.change('address', 'rediss://candidate.example:6380/1');
        const pending = fixture.click('preview');
        fixture.requests[0].succeed({ ...savedConfig().redis, addr: 'candidate.example:6380', db: 1 });
        await pending;
        assert.doesNotMatch(fixture.markup(), /setting\.redis\.|redis\.validation\.|undefined/);
        assert.match(fixture.statuses(), /candidate\.example:6380/);
    }
});

test('API hooks keep parsing, PING, persistence and refresh on their distinct contract endpoints', async () => {
    const requests = [];
    const invalidations = [];
    const hooks = evaluateSource(readSource('../src/api/endpoints/setting.ts'), {
        '@tanstack/react-query': {
            useQuery: (options) => options,
            useMutation: (options) => options,
            useQueryClient: () => ({ invalidateQueries: async (options) => { invalidations.push(options.queryKey); } }),
        },
        '../client': { apiClient: {
            get: async (path) => { requests.push({ method: 'GET', path }); return savedConfig(); },
            post: async (path, body) => {
                requests.push({ method: 'POST', path, body: plainValue(body) });
                return path.endsWith('/preview') ? savedConfig().redis : path.endsWith('/test') ? true : { type: body.type, restart_needed: true };
            },
        } },
        '../constants': {},
        '@/lib/logger': {},
        './user': {},
    });
    const query = hooks.useGetCacheConfig();
    assert.equal((await query.queryFn()).has_saved_connection, true);
    const preview = hooks.usePreviewCacheConfig();
    assert.equal((await preview.mutationFn({ type: 'redis' })).addr, 'stored.example:6380');
    assert.equal(await hooks.useTestCacheConnection().mutationFn({ type: 'redis' }), true);
    assert.deepEqual(invalidations, [], 'parsing and PING must not invalidate or activate saved configuration');
    const save = hooks.useSaveCacheConfig();
    assert.equal((await save.mutationFn({ type: '' })).type, '');
    await save.onSuccess();
    assert.deepEqual(plainValue(invalidations), [['settings', 'cache-config']]);
    assert.deepEqual(requests, [
        { method: 'GET', path: '/api/v1/setting/cache/config' },
        { method: 'POST', path: '/api/v1/setting/cache/preview', body: { type: 'redis' } },
        { method: 'POST', path: '/api/v1/setting/cache/test', body: { type: 'redis' } },
        { method: 'POST', path: '/api/v1/setting/cache/save', body: { type: '' } },
    ]);
});
