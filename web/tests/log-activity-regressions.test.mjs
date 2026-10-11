import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';

function fixture({ active = true, pages = [[{ id: 'old', time: 1 }]] } = {}) {
    const effects = [], timers = new Map(), requests = [], cancellations = [];
    let timerID = 0;
    let cached = { pages, pageParams: pages.map((_, index) => index + 1) };
    const client = {
        getQueryData: () => cached,
        setQueryData: (_key, updater) => { cached = typeof updater === 'function' ? updater(cached) : updater; },
        fetchQuery: options => options.queryFn({ signal: new AbortController().signal }),
        cancelQueries: options => { cancellations.push(options); return Promise.resolve(); },
        removeQueries() {},
    };
    const react = {
        useState: initial => [initial, () => {}],
        useRef: initial => ({ current: initial }),
        useMemo: fn => fn(),
        useCallback: fn => fn,
        useSyncExternalStore: (_subscribe, snapshot) => snapshot(),
        useEffect: fn => effects.push(fn),
    };
    let queryOptions;
    const dependencies = {
        react,
        '@tanstack/react-query': {
            useQueryClient: () => client,
            useInfiniteQuery: options => { queryOptions = options; return { data: cached }; },
            useMutation: () => ({}), useQuery: () => ({}),
        },
        '../client': { API_BASE_URL: '', apiClient: { get: async url => { requests.push(url); return [{ id: 'new', time: 3 }]; } } },
        '../types': { HttpStatus: {} },
        '@/lib/logger': { logger: { log() {}, error() {}, warn() {} } },
        './user': { useAuthStore: selector => selector({ token: 'test' }) },
        '@/lib/exact-id': { withExactId: value => value, compareExactIds: () => 0 },
    };
    function compile(source, deps) {
        const compiledModule = { exports: {} };
        vm.runInNewContext(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, {
            module: compiledModule, exports: compiledModule.exports,
            require: name => { if (!(name in deps)) throw new Error(`unexpected dependency ${name}`); return deps[name]; },
            AbortController, URLSearchParams, TextDecoder,
            document: { visibilityState: 'visible' },
            window: {
                setTimeout: callback => { timers.set(++timerID, callback); return timerID; },
                clearTimeout: id => timers.delete(id),
                setInterval: callback => { timers.set(++timerID, callback); return timerID; },
                clearInterval: id => timers.delete(id),
            },
            fetch: (_url, options) => {
                requests.push({ stream: true, signal: options.signal });
                return new Promise((_resolve, reject) => options.signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' }))));
            },
        });
        return compiledModule.exports;
    }
    dependencies['@/lib/log-stream-state'] = compile(fs.readFileSync(new URL('../src/lib/log-stream-state.ts', import.meta.url), 'utf8'), {});
    const api = compile(fs.readFileSync(new URL('../src/api/endpoints/log.ts', import.meta.url), 'utf8'), dependencies);
    return { api, requests, cancellations, timers, effects, get cached() { return cached; }, get queryOptions() { return queryOptions; }, run: () => api.useLogs({ active }) };
}

test('hidden logs disable queries and open no stream or refresh timer', () => {
    const f = fixture({ active: false });
    f.run();
    for (const effect of f.effects) effect();
    assert.equal(f.queryOptions.enabled, false);
    assert.equal(f.requests.length, 0);
    assert.equal(f.timers.size, 0);
    assert.equal(f.cancellations.length, 2);
});

test('leaving active logs aborts SSE and clears the fallback refresh timer', async () => {
    const f = fixture();
    f.run();
    const cleanups = f.effects.map(effect => effect()).filter(Boolean);
    const stream = f.requests.find(request => request.stream);
    assert.ok(stream);
    assert.equal(stream.signal.aborted, false);
    for (const cleanup of cleanups) cleanup();
    await Promise.resolve();
    assert.equal(stream.signal.aborted, true);
    assert.equal(f.timers.size, 0);
});

test('refresh performs one head request even after loading 30 history pages', async () => {
    const pages = Array.from({ length: 30 }, (_, index) => [{ id: `old-${index}`, time: 1 }]);
    const f = fixture({ pages });
    const { refresh } = f.api.useLogRefresh(10, { channel_id: 2 });
    await refresh();
    assert.equal(f.requests.length, 1);
    assert.match(f.requests[0], /page=1&/);
    assert.match(f.requests[0], /channel_id=2/);
    assert.equal(f.cached.pages.length, 30);
    assert.equal(f.cached.pages[0][0].id, 'new');
    assert.equal(f.cached.pages[29][0].id, 'old-29');
});
