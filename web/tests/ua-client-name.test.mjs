import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';

// ua.ts 是纯函数模块，用与 group-ui-regressions 相同的 TS 转译方式加载，
// 避免为它引入 vitest 依赖。
const source = fs.readFileSync(new URL('../src/components/modules/log/ua.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
});
const evaluatedModule = { exports: {} };
vm.runInNewContext(compiled.outputText, {
    module: evaluatedModule,
    exports: evaluatedModule.exports,
    require: () => { throw new Error('ua.ts should have no runtime imports'); },
});
const { resolveClientName } = evaluatedModule.exports;

test('recognizes known client keywords', () => {
    assert.equal(resolveClientName('SillyTavern/1.13.0 (compatible; Mozilla/5.0)'), '酒馆 SillyTavern');
    assert.equal(resolveClientName('Tavo/2.1 okhttp'), 'Tavo');
    assert.equal(resolveClientName('RocheApp/3.0'), 'Roche 小手机');
    assert.equal(resolveClientName('InternalBeyond-Mobile/1.1'), 'IB 小手机');
    assert.equal(resolveClientName('RikkaHub/2.4.13'), 'RikkaHub');
    assert.equal(resolveClientName('OrangeChat/2.2.3'), '橘瓣 OrangeChat');
});

test('matches keywords case-insensitively', () => {
    assert.equal(resolveClientName('sillytavern/1.0'), '酒馆 SillyTavern');
    assert.equal(resolveClientName('RIKKAHUB/1.0'), 'RikkaHub');
});

test('labels Android WebView before Chrome so in-app clients are not plain browsers', () => {
    const webviewUA = 'Mozilla/5.0 (Linux; Android 14; Pixel 8; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/130.0.0.0 Mobile Safari/537.36';
    assert.equal(resolveClientName(webviewUA), '应用内 WebView');
});

test('recognizes common browsers with Edge/Chrome priority', () => {
    const edgeUA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0';
    assert.equal(resolveClientName(edgeUA), 'Edge 浏览器');
    assert.equal(resolveClientName('Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36'), 'Chrome 浏览器');
    assert.equal(resolveClientName('Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15'), 'Safari 浏览器');
});

test('returns empty for unknown or empty UA so caller shows the raw string', () => {
    assert.equal(resolveClientName('curl/8.0'), '');
    assert.equal(resolveClientName(''), '');
    assert.equal(resolveClientName(undefined), '');
});
