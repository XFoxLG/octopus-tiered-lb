import assert from 'node:assert/strict';
import test from 'node:test';
import {
    formatPriceDraftValue,
    formatPricePair,
    formatSuccessRate,
    isPeakBillingSchedule,
    parsePriceDraft,
    parsePriceInput,
    priceDraftFromModel,
    summarizeChannelTags,
    totalRequests,
} from './pricing.ts';

test('parsePriceInput rejects blank input instead of silently zeroing it', () => {
    assert.equal(parsePriceInput(''), null);
    assert.equal(parsePriceInput('   '), null);
});

test('parsePriceInput accepts non-negative decimals, leading-dot form, and plain scientific notation', () => {
    assert.equal(parsePriceInput('0'), 0);
    assert.equal(parsePriceInput('10'), 10);
    assert.equal(parsePriceInput('0.03'), 0.03);
    assert.equal(parsePriceInput('.5'), 0.5);
    assert.equal(parsePriceInput(' 2.75 '), 2.75);
    assert.equal(parsePriceInput('007'), 7);
    assert.equal(parsePriceInput('1e5'), 100000);
    assert.equal(parsePriceInput('1.2E-7'), 1.2e-7);
    assert.equal(parsePriceInput('.5e3'), 500);
});

test('parsePriceInput rejects negatives, NaN, malformed and incomplete numbers instead of zeroing them', () => {
    const rejected = ['-1', '-0.5', 'abc', 'NaN', 'Infinity', '-Infinity', '1.2.3', '5.', '1,5', '+3', '0x10', '1e', 'e5', '1e+'];
    for (const raw of rejected) {
        assert.equal(parsePriceInput(raw), null, `expected "${raw}" to be rejected`);
    }
});

test('parsePriceDraft reports invalid fields in order and parses the rest', () => {
    const result = parsePriceDraft({ input: '0.03', output: '-1', cache_read: '', cache_write: 'oops' });
    assert.deepEqual(result.invalidFields, ['output', 'cache_read', 'cache_write']);
    assert.equal(result.prices.input, 0.03);

    const allValid = parsePriceDraft({ input: '1', output: '2.5', cache_read: '0', cache_write: '.25' });
    assert.deepEqual(allValid.invalidFields, []);
    assert.deepEqual(allValid.prices, { input: 1, output: 2.5, cache_read: 0, cache_write: 0.25 });
});

test('formatPriceDraftValue keeps decimals readable and preserves native exponent notation', () => {
    assert.equal(formatPriceDraftValue(0), '0');
    assert.equal(formatPriceDraftValue(0.03), '0.03');
    assert.equal(formatPriceDraftValue(1000), '1000');
    assert.equal(formatPriceDraftValue(1e-6), '0.000001');
    assert.equal(formatPriceDraftValue(1e-7), '1e-7');
    assert.equal(formatPriceDraftValue(Number.NaN), '0');
    assert.equal(formatPriceDraftValue(Number.POSITIVE_INFINITY), '0');
});

test('priceDraftFromModel mirrors model prices into draft strings', () => {
    assert.deepEqual(
        priceDraftFromModel({ input: 0.03, output: 0.06, cache_read: 0, cache_write: 1e-6 }),
        { input: '0.03', output: '0.06', cache_read: '0', cache_write: '0.000001' },
    );
});

test('totalRequests sums success and failure counters', () => {
    assert.equal(totalRequests(12, 3), 15);
    assert.equal(totalRequests(0, 0), 0);
});

test('formatSuccessRate keeps one decimal and falls back to dash without requests', () => {
    assert.equal(formatSuccessRate(0.995, 10), '99.5%');
    assert.equal(formatSuccessRate(0.99, 10), '99.0%');
    assert.equal(formatSuccessRate(0.5, 0), '—');
    assert.equal(formatSuccessRate(Number.NaN, 5), '—');
});

test('formatPricePair renders USD by default and converts via exchange rate in China mode', () => {
    assert.equal(formatPricePair(0.03, 0.02, false, 7.25), '0.03/0.02$');
    assert.equal(formatPricePair(2, 1, true, 7.25), '14.50/7.25¥');
});

test('formatPricePair guards against invalid numbers and exchange rates', () => {
    assert.equal(formatPricePair(Number.NaN, 1, false, 1), '0.00/1.00$');
    assert.equal(formatPricePair(2, 1, true, Number.NaN), '2.00/1.00¥');
    assert.equal(formatPricePair(2, 1, true, 0), '2.00/1.00¥');
});

test('summarizeChannelTags truncates and counts hidden entries', () => {
    const channels = [1, 2, 3, 4, 5].map((id) => ({
        channel_id: id,
        channel_name: `channel-${id}`,
        enabled: true,
        enabled_key_count: id,
    }));

    const summary = summarizeChannelTags(channels, 3);
    assert.equal(summary.visible.length, 3);
    assert.equal(summary.hiddenCount, 2);

    assert.equal(summarizeChannelTags(channels, 10).hiddenCount, 0);
    assert.equal(summarizeChannelTags(channels, 0).visible.length, 0);
    assert.equal(summarizeChannelTags(channels, 0).hiddenCount, 5);
    assert.equal(summarizeChannelTags(channels, -1).hiddenCount, 5);
});

test('isPeakBillingSchedule only matches the DeepSeek peak billing marker', () => {
    assert.equal(isPeakBillingSchedule('deepseek_v4'), true);
    assert.equal(isPeakBillingSchedule(''), false);
    assert.equal(isPeakBillingSchedule(undefined), false);
    assert.equal(isPeakBillingSchedule(null), false);
    assert.equal(isPeakBillingSchedule('other'), false);
});
