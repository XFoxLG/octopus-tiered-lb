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
                index: 1, enabled: 1,
                backend: 'Redis / Valkey', health: 'Healthy', tls: 'TLS', error: 'Connection refused',
                order: 'Chat Completions → Responses API',
                summary: 'passed=3 failed=0 unsupported=2 unknown=0',
                protocols: 'chat_only, responses_only',
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

// Guard against the class of bug where a component references a key that no locale
// defines: next-intl then renders the raw key (e.g. "channel.form.errorMessageTemplate")
// straight into the UI. The narrow per-component checks above cannot catch a newly
// added key, so walk every source file and resolve each literal `t(...)` call against
// the namespaces that file actually binds.
function collectSourceFiles(dir, acc = []) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
            if (entry.name === 'node_modules' || entry.name === '.next') continue;
            collectSourceFiles(full, acc);
        } else if (/\.tsx?$/.test(entry.name)) {
            acc.push(full);
        }
    }
    return acc;
}

function assertEveryReferencedKeyExists() {
    const enMessages = readJson(path.join(localeDir, 'en.json'));
    const enKeys = collectKeys(enMessages);
    const srcRoot = path.join(webRoot, 'src');
    const unresolved = [];

    const unwrapAwait = (node) => (node && ts.isAwaitExpression(node) ? node.expression : node);
    const literalText = (node) => {
        if (!node) return null;
        if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text;
        if (ts.isParenthesizedExpression(node)) return literalText(node.expression);
        if (ts.isConditionalExpression(node)) return literalText(node.whenTrue) ?? literalText(node.whenFalse);
        return null;
    };

    for (const file of collectSourceFiles(srcRoot)) {
        const sourceFile = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
        const namespaces = new Set();
        const boundVars = new Set();

        const collectBindings = (node) => {
            if (ts.isVariableDeclaration(node) && node.initializer) {
                const init = unwrapAwait(node.initializer);
                const isFactory = ts.isCallExpression(init)
                    && ts.isIdentifier(init.expression)
                    && ['useTranslations', 'getTranslations'].includes(init.expression.text);
                if (isFactory) {
                    namespaces.add(literalText(init.arguments[0]) ?? '');
                    if (ts.isIdentifier(node.name)) boundVars.add(node.name.text);
                    else if (ts.isObjectBindingPattern(node.name)) {
                        for (const element of node.name.elements) {
                            if (element.name && ts.isIdentifier(element.name)) boundVars.add(element.name.text);
                        }
                    }
                }
            }
            ts.forEachChild(node, collectBindings);
        };
        collectBindings(sourceFile);
        if (namespaces.size === 0 || boundVars.size === 0) continue;

        const checkCalls = (node) => {
            let keyNode = null;
            if (ts.isCallExpression(node)) {
                if (ts.isIdentifier(node.expression) && boundVars.has(node.expression.text)) {
                    keyNode = node.arguments[0];
                } else if (
                    ts.isPropertyAccessExpression(node.expression)
                    && node.expression.name.text === 'raw'
                    && ts.isIdentifier(node.expression.expression)
                    && boundVars.has(node.expression.expression.text)
                ) {
                    keyNode = node.arguments[0];
                }
            }
            if (keyNode) {
                const key = literalText(keyNode);
                // Dynamic keys cannot be resolved statically; skip them rather than guess.
                if (key !== null) {
                    const candidates = [...namespaces].map((ns) => (ns ? `${ns}.${key}` : key));
                    if (!candidates.includes(key)) candidates.push(key);
                    if (!candidates.some((candidate) => enKeys.has(candidate))) {
                        const line = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
                        unresolved.push(`${path.relative(webRoot, file)}:${line} -> ${key} (tried: ${candidates.join(', ')})`);
                    }
                }
            }
            ts.forEachChild(node, checkCalls);
        };
        checkCalls(sourceFile);
    }

    assert.deepEqual(
        unresolved,
        [],
        `Components reference translation keys with no matching locale entry (the UI would show the raw key):\n${unresolved.join('\n')}`,
    );
}

function assertGroupUiTranslations() {
    const paths = new Set([
        ...collectLiteralTranslationPaths('src/components/modules/group/Editor.tsx', 'group'),
        ...collectLiteralTranslationPaths('src/components/modules/group/GroupListItem.tsx', 'group'),
        ...['running', 'success', 'partial', 'failed'].map((status) => `group.card.healthProbeStatus.${status}`),
        ...['label', 'hint', 'buffer', 'immediate'].map((key) => `setting.retry.reasoningBufferStrategy.${key}`),
        ...[
            'upstreamProtocols',
            'upstreamProtocolsHint',
            'upstreamProtocolsFollowGroup',
            'upstreamProtocolsOrder',
            'upstreamProtocolsConflict',
            'channelTimeouts',
            'channelTimeoutsHint',
            'channelTimeoutsValueHint',
            'channelFirstTokenTimeout',
            'channelAttemptTimeout',
            'channelStreamIdleTimeout',
            'channelReasoningBufferStrategy',
            'channelReasoningBufferStrategyHint',
            'channelReasoningBufferStrategyInherit',
            'channelReasoningBufferStrategyBuffer',
            'channelReasoningBufferStrategyImmediate',
        ].map((key) => `channel.form.${key}`),
        // 协议名与协议说明通过模板键（t(`upstreamProtocol.${protocol}`)）动态取，
        // 字面量扫描看不到，必须显式枚举，否则漏键会静默显示原始 key。
        ...['chat', 'responses', 'messages', 'chat_only', 'responses_only', 'messages_only', 'passthrough', 'raw']
            .flatMap((protocol) => [`channel.form.upstreamProtocol.${protocol}`, `channel.form.upstreamProtocolHint.${protocol}`]),
        // 探测弹窗：协议/能力项的键名通过模板拼接，同样必须显式枚举。
        ...[
            'title', 'description', 'modelLabel', 'modelPlaceholder', 'modelHint', 'modelRequired',
            'skipModelTestWarning', 'skipModelTestConfirm', 'idleHint', 'running',
            'sectionProtocol', 'sectionCapability', 'summary', 'start', 'rerun', 'close',
            'apply', 'applied', 'applyHint', 'applyNoChange', 'applyAdded',
        ].map((key) => `channel.probe.${key}`),
        ...['protocol_chat', 'protocol_responses', 'protocol_messages', 'protocol_gemini',
            'models', 'text_generation', 'tool_calling', 'structured_output', 'web_search']
            .map((item) => `channel.probe.item.${item}`),
        ...['pass', 'fail', 'unsupported', 'unknown'].map((verdict) => `channel.probe.verdict.${verdict}`),
        'channel.detail.testModel.probe',
        'channel.detail.probeDialog.title',
        'channel.detail.probeDialog.description',
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
    assertEveryReferencedKeyExists();
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
