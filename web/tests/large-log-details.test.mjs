import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';
import React from 'react';
import * as jsx from 'react/jsx-runtime';
import { renderToStaticMarkup } from 'react-dom/server';
import * as intl from 'next-intl';
import * as icons from 'lucide-react';

function compile(file, deps = {}) {
    const compiledModule = { exports: {} };
    vm.runInNewContext(ts.transpileModule(fs.readFileSync(new URL(file, import.meta.url), 'utf8'), {
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX },
    }).outputText, {
        module: compiledModule, exports: compiledModule.exports, AbortController, URLSearchParams, TextEncoder,
        require: name => { if (!(name in deps)) throw Error(`Unexpected dependency ${name}`); return deps[name]; },
    });
    return compiledModule.exports;
}
const display = compile('../src/components/modules/log/content-display.ts');
const body = compile('../src/components/modules/log/body-preview.ts');

test('large data URLs are bounded before JSON parse; small JSON and SSE remain readable', () => {
    const huge = '{"image":"' + 'a'.repeat(8 << 20) + '"}';
    const result = display.formatLogText(huge, true);
    assert.equal(result.text.length, display.LOG_TEXT_PREVIEW_CHARS);
    assert.equal(result.truncated, true);
    assert.equal(display.formatLogText('{"text":"ok"}', true).text, '{\n  "text": "ok"\n}');
    const sse = 'data: {"choices":[{"delta":{"content":"safe"}}]}\n\ndata: [DONE]\n\n';
    assert.equal(display.formatLogText(sse, true).text, sse);
});

for (const locale of ['zh_hans', 'zh_hant', 'en']) test(`opening diagnostics does not render all large raw bodies (${locale})`, () => {
    let firstState = true;
    let pageReads = 0;
    const wrapper = tag => function Wrapper({ children, ...props }) { return React.createElement(tag, props, children); };
    const deps = {
        'react/jsx-runtime': jsx,
        react: { ...React, useState: initial => { const value = firstState ? true : initial; firstState = false; return [value, () => {}]; } },
        'next-intl': intl, 'lucide-react': icons,
        './ua': { resolveClientName: () => '' },
        '@/components/ui/badge': { Badge: wrapper('span') },
        '@/components/ui/button': { Button: wrapper('button') },
        '@/api/endpoints/log': { useDownloadRelayLogContent: () => ({}), useRelayLogContentPage: () => { pageReads++; return {}; } },
        '@/lib/utils': { cn: (...values) => values.filter(Boolean).join(' ') },
        './body-preview': body, './content-display': display,
        './display.ts': { resolveOutboundMaxTokens: () => ({ value: null, attempts: [] }), resolveTokenLimitEvidence: () => ({}) },
        './TransportDetails': { TransportDetails: () => null },
    };
    const { BoundaryDetails } = compile('../src/components/modules/log/BoundaryDetails.tsx', deps);
    const giant = 'SAFE_START ' + 'x'.repeat(4 << 20) + ' END_MUST_NOT_RENDER';
    const contents = Array.from({ length: 8 }, (_, i) => ({ id: i + 1, relay_log_id: '999', boundary: i === 0 ? 'client_ingress' : 'upstream_request',
        attempt_num: i, slot: 0, kind: 'body', state: 'ready', complete: true, captured_bytes: giant.length, text: giant }));
    const detail = { id: '999', request_content: giant, response_content: 'safe answer', contents,
        attempts: contents.slice(1).map(c => ({ attempt_num: c.attempt_num, channel_id: 1, status: 'success' })) };
    const messages = JSON.parse(fs.readFileSync(new URL(`../public/locale/${locale}.json`, import.meta.url), 'utf8'));
    const html = renderToStaticMarkup(React.createElement(intl.NextIntlClientProvider, { locale: locale === 'en' ? 'en' : 'zh', messages, timeZone: 'UTC' },
        React.createElement(BoundaryDetails, { detail, isLoading: false })));
    assert.ok(html.length < 100_000, `rendered ${html.length} characters`);
    assert.ok(html.includes(messages.log.card.forensics.previewLimited));
    assert.ok(html.includes(messages.log.card.forensics.viewContent));
    assert.ok(!html.includes('END_MUST_NOT_RENDER'));
    assert.ok(!html.includes('MISSING_MESSAGE'));
    assert.equal(pageReads, 0);
});

test('closing or superseding a detail fetch aborts it and ignores late results', async () => {
    const writes = [], requests = [], effects = [];
    let slot = 0;
    const deps = {
        react: { useState: initial => { const index = slot++; return [initial, value => writes.push([index, value])]; }, useRef: initial => ({ current: initial }),
            useEffect: fn => effects.push(fn), useCallback: fn => fn, useMemo: fn => fn() },
        '@tanstack/react-query': {},
        '../client': { API_BASE_URL: '', apiClient: { get: (url, _params, _auth, signal) => new Promise(resolve => requests.push({ url, signal, resolve })) } },
        '../types': { HttpStatus: {} }, '@/lib/logger': { logger: { error() {} } }, './user': {},
        '@/lib/exact-id': { exactId: id => String(id), withExactId: value => value }, '@/lib/log-stream-state': {},
    };
    const { useLogDetail } = compile('../src/api/endpoints/log.ts', deps);
    const hook = useLogDetail();
    const old = hook.fetchDetail('111');
    const next = hook.fetchDetail('222');
    assert.equal(requests[0].signal.aborted, true);
    assert.match(requests[1].url, /preview=true/);
    requests[0].resolve({ id: '111' });
    requests[1].resolve({ id: '222' });
    await Promise.all([old, next]);
    assert.ok(!writes.some(([, value]) => value?.id === '111'));
    assert.ok(writes.some(([, value]) => value?.id === '222'));
    const pending = hook.fetchDetail('333');
    hook.reset();
    const before = writes.length;
    requests[2].resolve({ id: '333' });
    await pending;
    assert.equal(requests[2].signal.aborted, true);
    assert.equal(writes.length, before);
    const unmount = effects[0]();
    const last = hook.fetchDetail('444');
    unmount();
    assert.equal(requests[3].signal.aborted, true);
    requests[3].resolve(null);
    await last;
});
