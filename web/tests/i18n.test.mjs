import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { createTranslator } from 'next-intl';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const webRoot = path.resolve(__dirname, '..');
const localeDir = path.join(webRoot, 'public', 'locale');
const localeFiles = ['en.json', 'zh_hans.json', 'zh_hant.json'];

function readJson(filePath) {
    const source = fs.readFileSync(filePath, 'utf8');
    const sourceFile = ts.parseJsonText(filePath, source);
    const validateUniqueKeys = (node) => {
        if (ts.isObjectLiteralExpression(node)) {
            const keys = new Set();
            for (const property of node.properties) {
                if (!ts.isPropertyAssignment(property)) continue;
                const key = property.name.text;
                assert.ok(!keys.has(key), `${filePath} contains duplicate JSON key ${key}`);
                keys.add(key);
            }
        }
        ts.forEachChild(node, validateUniqueKeys);
    };
    validateUniqueKeys(sourceFile);
    return JSON.parse(source);
}

function collectKeys(value, prefix = '', keys = new Set()) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
        return keys;
    }

    for (const [key, child] of Object.entries(value)) {
        const next = prefix ? `${prefix}.${key}` : key;
        keys.add(next);
        collectKeys(child, next, keys);
    }

    return keys;
}

function assertLocaleParity() {
    const [baseName, ...restNames] = localeFiles;
    const base = collectKeys(readJson(path.join(localeDir, baseName)));

    for (const name of restNames) {
        const current = collectKeys(readJson(path.join(localeDir, name)));
        const missing = [...base].filter((key) => !current.has(key));
        const extra = [...current].filter((key) => !base.has(key));
        assert.deepEqual(missing, [], `${name} missing locale keys:\n${missing.join('\n')}`);
        assert.deepEqual(extra, [], `${name} has extra locale keys:\n${extra.join('\n')}`);
    }
}

function assertNoHardcodedCopy(relativePath, forbiddenSnippets) {
    const content = fs.readFileSync(path.join(webRoot, relativePath), 'utf8');
    for (const snippet of forbiddenSnippets) {
        assert.equal(
            content.includes(snippet),
            false,
            `${relativePath} still contains hardcoded copy: ${snippet}`,
        );
    }
}

function assertTranslatedPaths(localeName, messages, paths) {
    const translate = createTranslator({
        locale: localeName.replace('.json', '').replaceAll('_', '-'),
        messages,
        onError: (error) => { throw error; },
    });
    for (const messagePath of paths) {
        const message = messagePath.split('.').reduce((value, segment) => value?.[segment], messages);
        assert.equal(typeof message, 'string', `${localeName} must define the actual UI path ${messagePath}`);
        assert.ok(message.trim(), `${localeName} has empty copy at ${messagePath}`);
        if (!messagePath.endsWith('.placeholder') && !messagePath.endsWith('.example')) {
            const translated = translate(messagePath, {
                count: 2, completed: 1, total: 2, ms: 25, value: 'Chat', id: 1,
                backend: 'Redis / Valkey', health: 'Healthy', tls: 'TLS', error: 'Connection refused',
            });
            assert.notEqual(translated, messagePath, `${localeName} renders a raw key at ${messagePath}`);
        }
    }
}

function collectLiteralTranslationPaths(relativePath, namespace) {
    const filePath = path.join(webRoot, relativePath);
    const sourceFile = ts.createSourceFile(filePath, fs.readFileSync(filePath, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    const paths = new Set();
    const collectKey = (expression) => {
        if (!expression) return;
        if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
            paths.add(`${namespace}.${expression.text}`);
        } else if (ts.isConditionalExpression(expression)) {
            collectKey(expression.whenTrue);
            collectKey(expression.whenFalse);
        }
    };
    const visit = (node) => {
        const isTranslationCall = ts.isCallExpression(node) && (
            (ts.isIdentifier(node.expression) && ['t', 'translate'].includes(node.expression.text))
            || (ts.isPropertyAccessExpression(node.expression) && node.expression.expression.getText(sourceFile) === 't' && node.expression.name.text === 'raw')
        );
        if (isTranslationCall) {
            collectKey(node.arguments[0]);
        }
        ts.forEachChild(node, visit);
    };
    visit(sourceFile);
    return paths;
}

function assertGroupUiTranslations() {
    const paths = new Set([
        ...collectLiteralTranslationPaths('src/components/modules/group/Editor.tsx', 'group'),
        ...collectLiteralTranslationPaths('src/components/modules/group/GroupListItem.tsx', 'group'),
        ...['running', 'success', 'partial', 'failed'].map((status) => `group.card.healthProbeStatus.${status}`),
        ...['label', 'hint', 'buffer', 'immediate'].map((key) => `setting.retry.reasoningBufferStrategy.${key}`),
        ...['outboundFormatOverride', 'outboundFormatOverrideHint', 'outboundFormatOverrideFollowGroup', 'outboundFormatOverrideChatOnly', 'outboundFormatOverrideResponsesOnly']
            .map((key) => `channel.form.${key}`),
    ]);
    for (const localeName of localeFiles) {
        assertTranslatedPaths(localeName, readJson(path.join(localeDir, localeName)), paths);
    }
}

function assertSettingsUiTranslations() {
    const paths = new Set([
        ...collectLiteralTranslationPaths('src/components/modules/setting/AIRoute.tsx', 'setting'),
        ...collectLiteralTranslationPaths('src/components/modules/setting/Cache.tsx', 'setting'),
        ...collectLiteralTranslationPaths('src/components/modules/setting/RequestFilter.tsx', 'setting'),
        ...['timeoutSeconds', 'parallelism'].flatMap((field) =>
            ['label', 'hint'].map((part) => `setting.aiRoute.${field}.${part}`)),
        ...['baseURL', 'apiKey', 'model', 'timeoutSeconds', 'parallelism', 'servicesJSON']
            .map((field) => `setting.aiRoute.validation.${field}`),
        ...['file', 'environment', 'deployment'].map((source) => `setting.redis.source.${source}`),
    ]);
    for (const localeName of localeFiles) {
        assertTranslatedPaths(localeName, readJson(path.join(localeDir, localeName)), paths);
    }
}

function run() {
    assertLocaleParity();
    assertGroupUiTranslations();
    assertSettingsUiTranslations();
    const en = readJson(path.join(localeDir, 'en.json'));
    assert.equal(en.login?.welcome, 'Welcome back', 'en.json should define login.welcome');
    assertNoHardcodedCopy('src/components/modules/group/Editor.tsx', [
        'API 分类',
        'Condition (JSON)',
        'aria-label="search"',
    ]);
    assertNoHardcodedCopy('src/components/modules/channel/Form.tsx', [
        'title="Remove"',
    ]);
    assertNoHardcodedCopy('src/components/modules/channel/templates.ts', [
        "description: '",
    ]);
}

run();
console.log('i18n checks passed');
