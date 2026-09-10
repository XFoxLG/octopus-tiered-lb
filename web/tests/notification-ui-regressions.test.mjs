import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import ts from 'typescript';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createTranslator } from 'next-intl';

const require = createRequire(import.meta.url);
const readSource = (relativePath) => fs.readFileSync(new URL(relativePath, import.meta.url), 'utf8');
const readMessages = (locale) => JSON.parse(readSource(`../public/locale/${locale}.json`));

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

const textResolvers = evaluateSource(readSource('../src/components/modules/notification/notif-text.ts'));
const notification = {
    id: 7, type: 'site', severity: 'error', created_at: 1_788_800_000_000, updated_at: 1_788_800_000_000,
    title: 'Site sync completed', content: 'scheduled: success=2 partial=1 failed=3 skipped=4 warnings=5',
    title_key: 'site.batch', title_args: JSON.stringify({ phase: 'sync' }),
    content_key: 'site.batch', content_args: JSON.stringify({ trigger: 'scheduled', success: 2, partial: 1, failed: 3, skipped: 4, warnings: 5 }),
};

function createNotificationTranslator(locale, errors = []) {
    return createTranslator({
        locale: locale.replaceAll('_', '-'), messages: readMessages(locale), namespace: 'notif',
        onError: (error) => errors.push(error),
    });
}

for (const locale of ['en', 'zh_hans', 'zh_hant']) {
    test(`${locale}: historical site batch and account notifications retain readable translations`, () => {
        const errors = [];
        const translate = createNotificationTranslator(locale, errors);
        const title = textResolvers.resolveNotifTitle(notification, translate);
        const content = textResolvers.resolveNotifContent(notification, translate);
        assert.equal(translate.has('site.batch.title'), true);
        assert.match(title, /sync/);
        assert.match(content, /scheduled.*2.*1.*3.*4.*5/);
        if (locale !== 'en') assert.match(title, /站/);
        for (const messageKey of ['site.account_ok', 'site.account_fail']) {
            const accountNotification = {
                ...notification, title_key: messageKey, content_key: messageKey,
                title_args: JSON.stringify({ phase: 'sync' }),
                content_args: JSON.stringify({ phase: 'sync', account_id: 17, detail: 'timeout' }),
            };
            assert.match(textResolvers.resolveNotifTitle(accountNotification, translate), /sync/);
            assert.match(textResolvers.resolveNotifContent(accountNotification, translate), /17.*sync/);
            if (messageKey.endsWith('fail')) {
                assert.match(textResolvers.resolveNotifContent(accountNotification, translate), /timeout/);
            }
        }
        assert.deepEqual(errors, []);
    });
}

test('unknown message keys use stored text without reporting missing translations', () => {
    const errors = [];
    const translate = createNotificationTranslator('zh_hans', errors);
    const unknownNotification = { ...notification, title_key: 'future.message', content_key: 'future.message' };
    assert.equal(textResolvers.resolveNotifTitle(unknownNotification, translate), notification.title);
    assert.equal(textResolvers.resolveNotifContent(unknownNotification, translate), notification.content);
    assert.deepEqual(errors, []);
});

test('malformed arguments, missing ICU values, and key-shaped library fallbacks preserve stored text', () => {
    const translate = createNotificationTranslator('en');
    for (const serializedArguments of ['{broken', 'null', '[]', '42', '"text"', '{}']) {
        assert.equal(textResolvers.resolveNotifContent({
            ...notification, content_key: 'backup.ok', content_args: serializedArguments,
        }, translate), notification.content);
    }
    for (const returnedText of ['future.title', 'notif.future.title', '']) {
        const fallbackTranslator = Object.assign(() => returnedText, { has: () => true });
        assert.equal(textResolvers.resolveNotifTitle({ ...notification, title_key: 'future' }, fallbackTranslator), notification.title);
    }
    const throwingTranslator = Object.assign(() => { throw new Error('Invalid message'); }, { has: () => true });
    assert.equal(textResolvers.resolveNotifTitle(notification, throwingTranslator), notification.title);
});

test('legacy plain text and current parameterized notifications keep their original behavior', () => {
    const translate = createNotificationTranslator('en');
    assert.equal(textResolvers.resolveNotifTitle({ ...notification, title_key: undefined }, translate), notification.title);
    assert.equal(textResolvers.resolveNotifContent({ ...notification, content_key: undefined }, translate), notification.content);
    assert.equal(textResolvers.resolveNotifContent({
        ...notification, content_key: 'backup.ok', content_args: JSON.stringify({ file: 'test.zip', size: 2048 }),
    }, translate), 'Backup uploaded to test.zip (2048 bytes).');
});

test('every current backend message and retained historical message has renderable templates in every locale', () => {
    const backendSource = readSource('../../internal/op/notification/message.go');
    const messageKeys = [
        ...Array.from(backendSource.matchAll(/\bNotifKey\s*=\s*"([^"]+)"/g), (match) => match[1]),
        'site.batch', 'site.account_ok', 'site.account_fail', 'alert.firing', 'alert.resolved',
        'report.sent', 'report.failed', 'report.skipped',
    ];
    assert.ok(messageKeys.includes('backup.ok'));
    const argumentsByName = {
        name: 'Example', id: 7, expire_at: '2026-09-09', file: 'test.zip', size: 2048,
        detail: 'timeout', type: 'postgres', restart: 'true', fails: 3, channel: 'Example',
        phase: 'sync', trigger: 'scheduled', success: 2, partial: 1, failed: 3, skipped: 4, warnings: 5, account_id: 17,
    };
    for (const locale of ['en', 'zh_hans', 'zh_hant']) {
        const errors = [];
        const translate = createNotificationTranslator(locale, errors);
        for (const messageKey of messageKeys) {
            for (const field of ['title', 'content']) {
                const translationKey = `${messageKey}.${field}`;
                assert.ok(translate.has(translationKey), `${locale} is missing ${translationKey}`);
                assert.notEqual(translate(translationKey, argumentsByName), `notif.${translationKey}`);
            }
        }
        assert.deepEqual(errors, []);
    }
});

const centerSource = readSource('../src/components/modules/notification/index.tsx');
const centerSyntax = ts.createSourceFile('notification.tsx', centerSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const withoutImports = centerSyntax.statements.filter((node) => !ts.isImportDeclaration(node)).map((node) => node.getText(centerSyntax)).join('\n');

function collectNodes(sourceFile, predicate) {
    const matches = [];
    const visit = (node) => {
        if (predicate(node)) matches.push(node);
        ts.forEachChild(node, visit);
    };
    visit(sourceFile);
    return matches;
}

test('list cards and details render historical text with wrapping and keyboard selection', () => {
    const messages = readMessages('zh_hans');
    const components = evaluateSource(`${withoutImports}\nexport { NotificationCard, NotificationDetailContent };`, {
        ...textResolvers,
        useTranslations: (namespace) => createTranslator({ locale: 'zh-Hans', messages, namespace, onError: (error) => { throw error; } }),
        Badge: ({ children }) => createElement('span', null, children),
        Button: ({ children }) => createElement('button', null, children),
        Trash2: () => null,
    });
    const card = renderToStaticMarkup(createElement(components.NotificationCard, { item: notification, selected: true, onSelect: () => {} }));
    const detail = renderToStaticMarkup(createElement(components.NotificationDetailContent, { selected: notification }));
    for (const markup of [card, detail]) {
        assert.match(markup, /站点 sync 已完成/);
        assert.match(markup, /成功=2/);
        assert.doesNotMatch(markup, /notif\.site\./);
    }
    assert.match(card, /aria-pressed="true"/);
    assert.match(card, /focus-visible:ring/);
    assert.match(card, /line-clamp-2[^"\n]*break-words/);
});

test('notification bell renders the same historical messages without raw keys', () => {
    const bellSource = readSource('../src/components/modules/notification/NotificationBell.tsx');
    const bellSyntax = ts.createSourceFile('NotificationBell.tsx', bellSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    const bellDeclarations = bellSyntax.statements.filter((node) => !ts.isImportDeclaration(node)).map((node) => node.getText(bellSyntax)).join('\n');
    const messages = readMessages('zh_hans');
    const { NotificationBell } = evaluateSource(bellDeclarations, {
        ...textResolvers,
        useTranslations: (namespace) => createTranslator({ locale: 'zh-Hans', messages, namespace, onError: (error) => { throw error; } }),
        useNavStore: () => () => {},
        useUnreadNotificationCount: () => ({ data: { count: 1 } }),
        useNotifications: () => ({ data: [notification] }),
        useMarkAllNotificationsRead: () => ({ mutate: () => {}, isPending: false }),
        useNotificationStream: () => {},
        Popover: ({ children }) => createElement('div', null, children),
        PopoverTrigger: ({ children }) => createElement('div', null, children),
        PopoverContent: ({ children }) => createElement('div', null, children),
        Button: ({ children }) => createElement('button', null, children),
        Badge: ({ children }) => createElement('span', null, children),
        Bell: () => null, CheckCheck: () => null, ExternalLink: () => null,
    });
    const markup = renderToStaticMarkup(createElement(NotificationBell));
    assert.match(markup, /站点 sync 已完成/);
    assert.match(markup, /成功=2/);
    assert.doesNotMatch(markup, /notif\.site\./);
});

test('detail dialog covers phone and tablet widths up to the desktop sidebar breakpoint', () => {
    const responsiveDeclaration = collectNodes(centerSyntax, (node) => ts.isVariableDeclaration(node)
        && node.initializer && ts.isCallExpression(node.initializer)
        && ['useIsMobile', 'useIsBelowBreakpoint'].includes(node.initializer.expression.getText(centerSyntax)))[0];
    assert.ok(responsiveDeclaration, 'Notification must select its detail layout responsively');
    const dialog = collectNodes(centerSyntax, (node) => ts.isJsxOpeningElement(node)
        && node.tagName.getText(centerSyntax) === 'Dialog')[0];
    const openExpression = dialog.attributes.properties.find((attribute) => attribute.name?.getText(centerSyntax) === 'open').initializer.expression;
    const layout = evaluateSource(`export function isDialogOpen(viewportWidth, selectedNotificationID) {
        const useIsMobile = () => viewportWidth < 768;
        const useIsBelowBreakpoint = (breakpoint) => viewportWidth < breakpoint;
        const ${responsiveDeclaration.getText(centerSyntax)};
        return ${openExpression.getText(centerSyntax)};
    }`);
    for (const viewportWidth of [375, 767, 768, 820, 960, 1023]) {
        assert.equal(layout.isDialogOpen(viewportWidth, notification.id), true, `${viewportWidth}px needs a detail dialog`);
        assert.equal(layout.isDialogOpen(viewportWidth, undefined), false);
    }
    for (const viewportWidth of [1024, 1280, 1920]) {
        assert.equal(layout.isDialogOpen(viewportWidth, notification.id), false);
    }
    const aside = collectNodes(centerSyntax, (node) => ts.isJsxOpeningElement(node)
        && node.tagName.getText(centerSyntax) === 'aside')[0];
    assert.match(aside.getText(centerSyntax), /lg:block/);
    assert.match(aside.getText(centerSyntax), /overflow-y-auto/);
});

test('type filters include retained historical records without requiring their retired features', () => {
    const declaration = collectNodes(centerSyntax, (node) => ts.isVariableStatement(node)
        && node.declarationList.declarations.some((entry) => entry.name.getText(centerSyntax) === 'NOTIFICATION_TYPES'))[0];
    const { notificationTypes } = evaluateSource(`${declaration.getText(centerSyntax)}\nexport const notificationTypes = NOTIFICATION_TYPES;`);
    for (const notificationType of ['', 'site', 'alert', 'report', 'usage', 'channel_expire', 'backup', 'system', 'key_health']) {
        assert.ok(notificationTypes.includes(notificationType), `Missing filter for ${notificationType}`);
    }
});
