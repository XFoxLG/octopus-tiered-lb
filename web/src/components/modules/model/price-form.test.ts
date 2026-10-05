import assert from 'node:assert/strict';
import test from 'node:test';

import {
    buildPriceRuleBasePayload,
    formatPriceValue,
    formatWindowsLabel,
    hhmmToMinutes,
    isPriceRuleType,
    minutesToHHMM,
    parseIntegerInput,
    parsePriceInput,
    resolveScheduleWindows,
    resolveWindow,
    validatePriceRuleBase,
    windowErrorKey,
    windowsOverlap,
    windowToForm,
    type PriceRuleFormBase,
} from './price-form.ts';

test('parsePriceInput rejects blank and parses finite numbers', () => {
    assert.equal(parsePriceInput(''), null);
    assert.equal(parsePriceInput('   '), null);
    assert.equal(parsePriceInput('0.5'), 0.5);
    assert.equal(parsePriceInput(' 1.5 '), 1.5);
    assert.equal(parsePriceInput('-1'), -1); // 解析成功，负数由校验层拦截
});

test('parsePriceInput rejects non-finite input instead of coercing to 0', () => {
    assert.equal(parsePriceInput('abc'), null);
    assert.equal(parsePriceInput('NaN'), null);
    assert.equal(parsePriceInput('1e999'), null); // Infinity
    assert.equal(parsePriceInput('Infinity'), null);
});

test('parseIntegerInput accepts safe integers only', () => {
    assert.equal(parseIntegerInput(''), null);
    assert.equal(parseIntegerInput(' 7 '), 7);
    assert.equal(parseIntegerInput('-2'), -2);
    assert.equal(parseIntegerInput('+5'), 5);
    assert.equal(parseIntegerInput('1.5'), null);
    assert.equal(parseIntegerInput('abc'), null);
    assert.equal(parseIntegerInput('1e3'), null);
});

test('hhmmToMinutes parses valid times and rejects invalid ones', () => {
    assert.equal(hhmmToMinutes('00:00'), 0);
    assert.equal(hhmmToMinutes('09:00'), 540);
    assert.equal(hhmmToMinutes('7:30'), 450);
    assert.equal(hhmmToMinutes('23:59'), 1439);
    assert.equal(hhmmToMinutes(''), null);
    assert.equal(hhmmToMinutes('24:00'), 1440);
    assert.equal(hhmmToMinutes('24:01'), null);
    assert.equal(hhmmToMinutes('09:60'), null);
    assert.equal(hhmmToMinutes('9:5'), null);
    assert.equal(hhmmToMinutes('abc'), null);
});

test('minutesToHHMM preserves the end-of-day boundary', () => {
    assert.equal(minutesToHHMM(0), '00:00');
    assert.equal(minutesToHHMM(540), '09:00');
    assert.equal(minutesToHHMM(1439), '23:59');
    assert.equal(minutesToHHMM(1440), '24:00');
    assert.equal(minutesToHHMM(-5), '00:00');
    assert.equal(minutesToHHMM(NaN), '00:00');
    assert.equal(minutesToHHMM(90.7), '01:30');
});

test('resolveWindow: both empty means closed window (0,0)', () => {
    const r = resolveWindow({ start: '', end: '' });
    assert.ok(r.ok);
    assert.deepEqual(r.ok && r.value, { start: 0, end: 0 });
});

test('resolveWindow: one empty or garbage is incomplete', () => {
    assert.deepEqual(resolveWindow({ start: '09:00', end: '' }), { ok: false, reason: 'incomplete' });
    assert.deepEqual(resolveWindow({ start: '', end: '18:00' }), { ok: false, reason: 'incomplete' });
    assert.deepEqual(resolveWindow({ start: '25:00', end: '18:00' }), { ok: false, reason: 'incomplete' });
});

test('resolveWindow: inverted start>end is rejected, start==end is closed', () => {
    assert.deepEqual(resolveWindow({ start: '18:00', end: '09:00' }), { ok: false, reason: 'inverted' });
    const closed = resolveWindow({ start: '09:00', end: '09:00' });
    assert.ok(closed.ok);
    assert.deepEqual(closed.ok && closed.value, { start: 540, end: 540 });
    const ok = resolveWindow({ start: '09:00', end: '12:00' });
    assert.ok(ok);
    assert.deepEqual(ok.ok && ok.value, { start: 540, end: 720 });
});

test('end-of-day windows survive an edit round trip without shortening coverage', () => {
    const form = windowToForm({ start: 0, end: 1440 });
    assert.deepEqual(form, { start: '00:00', end: '24:00' });
    assert.deepEqual(resolveWindow(form), { ok: true, value: { start: 0, end: 1440 } });
    assert.equal(formatWindowsLabel({ start: 0, end: 1440 }, { start: 0, end: 0 }), '00:00-24:00');
});

test('empty prices and unsafe priorities cannot pass form validation', () => {
    const errors = validatePriceRuleBase({ name: 'example', rule_type: 'exact', rule_value: 'gpt', input: '', output: '0', cache_read: '0', cache_write: '0', sort_order: '9007199254740993', enabled: true });
    assert.equal(errors.input, 'invalidNumber');
    assert.equal(errors.sort_order, 'invalidInteger');
});

test('windowErrorKey maps reasons to i18n keys', () => {
    assert.equal(windowErrorKey('incomplete'), 'windowIncomplete');
    assert.equal(windowErrorKey('inverted'), 'windowInverted');
});

test('windowsOverlap: half-open intervals, closed windows never overlap', () => {
    assert.equal(windowsOverlap({ start: 540, end: 720 }, { start: 840, end: 1080 }), false);
    assert.equal(windowsOverlap({ start: 540, end: 900 }, { start: 840, end: 1080 }), true);
    assert.equal(windowsOverlap({ start: 0, end: 0 }, { start: 540, end: 720 }), false); // 关闭窗
    assert.equal(windowsOverlap({ start: 540, end: 540 }, { start: 540, end: 720 }), false);
    assert.equal(windowsOverlap({ start: 540, end: 840 }, { start: 840, end: 900 }), false); // 半开区间相邻
    assert.equal(windowsOverlap({ start: 540, end: 1080 }, { start: 600, end: 700 }), true); // 包含
});

test('formatWindowsLabel joins active windows and returns null when all closed', () => {
    assert.equal(
        formatWindowsLabel({ start: 540, end: 720 }, { start: 840, end: 1080 }),
        '09:00-12:00 / 14:00-18:00',
    );
    assert.equal(formatWindowsLabel({ start: 540, end: 720 }, { start: 0, end: 0 }), '09:00-12:00');
    assert.equal(formatWindowsLabel({ start: 0, end: 0 }, { start: 0, end: 0 }), null);
    assert.equal(formatWindowsLabel({ start: 720, end: 540 }, { start: 0, end: 0 }), null);
});

test('windowToForm prefills active windows and leaves closed windows blank', () => {
    assert.deepEqual(windowToForm({ start: 540, end: 720 }), { start: '09:00', end: '12:00' });
    assert.deepEqual(windowToForm({ start: 0, end: 0 }), { start: '', end: '' });
    assert.deepEqual(windowToForm({ start: 540, end: 540 }), { start: '', end: '' });
});

test('validatePriceRuleBase reports missing name and rule value', () => {
    const form: PriceRuleFormBase = {
        name: '   ',
        rule_type: 'contains',
        rule_value: '',
        input: '0',
        output: '0',
        cache_read: '0',
        cache_write: '0',
        sort_order: '0',
        enabled: true,
    };
    const errors = validatePriceRuleBase(form);
    assert.equal(errors.name, 'nameRequired');
    assert.equal(errors.rule_value, 'ruleValueRequired');
});

test('validatePriceRuleBase rejects non-finite and negative prices plus bad sort order', () => {
    const form: PriceRuleFormBase = {
        name: 'ok',
        rule_type: 'contains',
        rule_value: 'gpt',
        input: 'abc',
        output: '-1',
        cache_read: '1e999',
        cache_write: '0.5',
        sort_order: '1.5',
        enabled: true,
    };
    const errors = validatePriceRuleBase(form);
    assert.equal(errors.input, 'invalidNumber');
    assert.equal(errors.output, 'nonNegativeRequired');
    assert.equal(errors.cache_read, 'invalidNumber');
    assert.equal(errors.cache_write, undefined);
    assert.equal(errors.sort_order, 'invalidInteger');
    assert.ok(Object.values(errors).some((v) => v !== undefined));
});

test('validatePriceRuleBase passes a valid form', () => {
    const errors = validatePriceRuleBase({
        name: 'my-chat',
        rule_type: 'prefix',
        rule_value: 'gpt/',
        input: '0.5',
        output: '2',
        cache_read: '0.1',
        cache_write: '0',
        sort_order: '-3',
        enabled: false,
    });
    assert.deepEqual(errors, {});
});

test('buildPriceRuleBasePayload trims and coerces blanks to 0', () => {
    const payload = buildPriceRuleBasePayload({
        name: '  my-chat  ',
        rule_type: 'contains',
        rule_value: '  gpt  ',
        input: '',
        output: '1.5',
        cache_read: 'abc',
        cache_write: '',
        sort_order: '',
        enabled: true,
    });
    assert.equal(payload.name, 'my-chat');
    assert.equal(payload.rule_value, 'gpt');
    assert.equal(payload.input, 0);
    assert.equal(payload.output, 1.5);
    assert.equal(payload.cache_read, 0); // 非法值兜底（校验层应先行拦截）
    assert.equal(payload.cache_write, 0);
    assert.equal(payload.sort_order, 0);
    assert.equal(payload.enabled, true);
});

test('isPriceRuleType narrows to known rule types', () => {
    assert.ok(isPriceRuleType('exact'));
    assert.ok(isPriceRuleType('prefix'));
    assert.ok(isPriceRuleType('contains'));
    assert.ok(!isPriceRuleType('regex'));
});

test('resolveScheduleWindows returns overlap hint without failing', () => {
    const ok = resolveScheduleWindows(
        { start: '09:00', end: '12:00' },
        { start: '10:00', end: '18:00' },
    );
    assert.ok(ok.ok);
    if (ok.ok) {
        assert.deepEqual(ok.w1, { start: 540, end: 720 });
        assert.deepEqual(ok.w2, { start: 600, end: 1080 });
        assert.equal(ok.overlap, true);
    }

    const apart = resolveScheduleWindows(
        { start: '09:00', end: '12:00' },
        { start: '14:00', end: '18:00' },
    );
    assert.ok(apart.ok);
    assert.ok(!apart.ok || apart.overlap === false);

    const closed = resolveScheduleWindows({ start: '', end: '' }, { start: '', end: '' });
    assert.ok(closed.ok);
    assert.ok(!closed.ok || closed.overlap === false);
});

test('resolveScheduleWindows reports per-window errors', () => {
    const bad = resolveScheduleWindows(
        { start: '09:00', end: '' },
        { start: '18:00', end: '09:00' },
    );
    assert.ok(!bad.ok);
    if (!bad.ok) {
        assert.equal(bad.errors.w1, 'incomplete');
        assert.equal(bad.errors.w2, 'inverted');
    }
});

test('formatPriceValue stringifies numbers defensively', () => {
    assert.equal(formatPriceValue(0.5), '0.5');
    assert.equal(formatPriceValue(-0), '0');
    assert.equal(formatPriceValue(undefined as unknown as number), '0');
});
