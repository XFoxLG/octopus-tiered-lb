import test from 'node:test';
import assert from 'node:assert/strict';

import type { RelayLog, RelayLogDetail } from '@/api/endpoints/log';
import {
    formatJsonForCopy,
    formatLogDuration,
    reportedIPSourceToken,
    resolveFirstTokenMilestones,
    resolveLogDisplayFields,
    resolveLogInvestigationMarkers,
} from './display.ts';

test('formatLogDuration renders a dash when the metric was not recorded', () => {
    assert.equal(formatLogDuration(0), '—');
    assert.equal(formatLogDuration(undefined), '—');
    assert.equal(formatLogDuration(null), '—');
    assert.equal(formatLogDuration(-1), '—');
});

test('formatLogDuration keeps millisecond and second rendering for recorded values', () => {
    assert.equal(formatLogDuration(850), '850ms');
    assert.equal(formatLogDuration(1500), '1.50s');
});

test('resolveFirstTokenMilestones converts absolute timestamps to request-relative values', () => {
    const milestones = resolveFirstTokenMilestones({
        time: 1_700_000_000,
        ftut: 0,
        upstream_first_visible_at: 1_700_000_000_000 + 1_200,
        client_first_flush_at: 1_700_000_000_000 + 1_500,
    });
    assert.equal(milestones.upstreamFirstVisibleMs, 1_200);
    assert.equal(milestones.clientFirstFlushMs, 1_500);
});

test('resolveFirstTokenMilestones hides unobserved or invalid milestones', () => {
    const unobserved = resolveFirstTokenMilestones({ time: 1_700_000_000, ftut: 0 });
    assert.equal(unobserved.upstreamFirstVisibleMs, 0);
    assert.equal(unobserved.clientFirstFlushMs, 0);

    // Timestamps before the request start (clock skew / zeroed columns) are
    // not rendered as negative durations.
    const skewed = resolveFirstTokenMilestones({
        time: 1_700_000_000,
        ftut: 0,
        upstream_first_visible_at: 1_700_000_000_000 - 500,
        client_first_flush_at: 0,
    });
    assert.equal(skewed.upstreamFirstVisibleMs, 0);
    assert.equal(skewed.clientFirstFlushMs, 0);
});

test('resolveFirstTokenMilestones anchors to ftut so values cannot exceed the total duration', () => {
    // 线上实测样本：log.time 秒级截断导致「上游首字 10.16s > 总耗时 9.83s」。
    // ftut 与 client_first_flush_at 是同一时间戳，必须严格一致。
    const milestones = resolveFirstTokenMilestones({
        time: 1_700_000_000,
        ftut: 9_580,
        upstream_first_visible_at: 1_700_000_000_000 + 10_160,
        client_first_flush_at: 1_700_000_000_000 + 10_160,
    });
    assert.equal(milestones.clientFirstFlushMs, 9_580);
    assert.equal(milestones.upstreamFirstVisibleMs, 9_580);
});

test('resolveFirstTokenMilestones keeps the exact upstream lead over client flush', () => {
    // 上游首个可见内容比客户端写出早 1.16s → 上游首字 = ftut − 1.16s。
    const milestones = resolveFirstTokenMilestones({
        time: 1_700_000_000,
        ftut: 9_580,
        upstream_first_visible_at: 1_700_000_000_000 + 9_000,
        client_first_flush_at: 1_700_000_000_000 + 10_160,
    });
    assert.equal(milestones.clientFirstFlushMs, 9_580);
    assert.equal(milestones.upstreamFirstVisibleMs, 8_420);
});


function buildLog(overrides: Partial<RelayLog> = {}): RelayLog {
    return {
        id: 1,
        time: 0,
        request_model_name: 'deepseek-v4-pro-max',
        request_api_key_name: '',
        endpoint_type: '',
        channel: 0,
        channel_name: '',
        actual_model_name: '',
        input_tokens: 0,
        output_tokens: 0,
        ftut: 0,
        use_time: 0,
        cost: 0,
        error: '',
        attempts: [],
        total_attempts: 0,
        ...overrides,
    };
}

test('source IP prefers reported metadata while keeping legacy address fallback', () => {
    const log = buildLog({
        client_ip: '127.0.0.1',
        reported_client_ip: '203.0.113.7',
        reported_client_ip_source: 'cf-connecting-ip',
    });
    const reported = resolveLogDisplayFields(log);
    assert.equal(reported.clientIP, '203.0.113.7');
    assert.equal(reported.reportedClientIPSource, 'cf-connecting-ip');

    const legacy = resolveLogDisplayFields(buildLog({ client_ip: '198.51.100.9' }));
    assert.equal(legacy.clientIP, '198.51.100.9');
    assert.equal(legacy.reportedClientIPSource, '');
});

test('source IP and header label both follow the loaded detail', () => {
    const log = buildLog({
        client_ip: '127.0.0.1',
        reported_client_ip: '203.0.113.7',
        reported_client_ip_source: 'cf-connecting-ip',
    });
    const detail: RelayLogDetail = {
        ...log,
        reported_client_ip: '198.51.100.9',
        reported_client_ip_source: 'x-forwarded-for',
        request_content: '',
        response_content: '',
    };
    const result = resolveLogDisplayFields(log, detail);
    assert.equal(result.clientIP, '198.51.100.9');
    assert.equal(result.reportedClientIPSource, 'x-forwarded-for');
});

test('missing reported address never labels a direct address as forwarded', () => {
    const result = resolveLogDisplayFields(buildLog({
        client_ip: '127.0.0.1',
        reported_client_ip: '  ',
        reported_client_ip_source: 'cf-connecting-ip',
    }));
    assert.equal(result.clientIP, '127.0.0.1');
    assert.equal(result.reportedClientIPSource, '');
});

test('resolveLogDisplayFields infers deepseek endpoint and uses attempt channel fallback', () => {
    const log = buildLog({
        attempts: [
            {
                channel_id: 12,
                channel_name: 'DeepSeek Channel',
                model_name: 'deepseek-v4-pro-max',
                attempt_num: 1,
                status: 'success',
                duration: 10,
            },
        ],
    });

    const result = resolveLogDisplayFields(log);
    assert.equal(result.endpointType, 'deepseek');
    assert.equal(result.channelName, 'DeepSeek Channel');
    assert.equal(result.actualModelName, 'deepseek-v4-pro-max');
});

test('resolveLogDisplayFields prefers detail payload over list payload', () => {
    const log = buildLog({
        endpoint_type: '',
        channel_name: '',
        actual_model_name: '',
    });
    const detail: RelayLogDetail = {
        ...log,
        endpoint_type: 'deepseek',
        channel_name: 'Relay Channel',
        actual_model_name: 'deepseek-v4-pro-max',
        request_content: '{}',
        response_content: '{}',
    };

    const result = resolveLogDisplayFields(log, detail);
    assert.equal(result.endpointType, 'deepseek');
    assert.equal(result.channelName, 'Relay Channel');
    assert.equal(result.actualModelName, 'deepseek-v4-pro-max');
});

test('resolveLogDisplayFields falls back to channel id mapping when channel names are empty', () => {
    const log = buildLog({
        channel: 42,
        channel_name: '',
        attempts: [
            {
                channel_id: 42,
                channel_name: '',
                model_name: 'deepseek-v4-pro-max',
                attempt_num: 1,
                status: 'success',
                duration: 10,
            },
        ],
    });

    const result = resolveLogDisplayFields(log, null, new Map([[42, 'DeepSeek Fallback Channel']]));
    assert.equal(result.channelId, 42);
    assert.equal(result.channelName, 'DeepSeek Fallback Channel');
});

test('resolveLogDisplayFields falls back to channel id label when no channel name sources exist', () => {
    const log = buildLog({
        channel: 77,
        channel_name: '',
        attempts: [
            {
                channel_id: 77,
                channel_name: '',
                model_name: 'gpt-4o-mini',
                attempt_num: 1,
                status: 'success',
                duration: 10,
            },
        ],
    });

    const result = resolveLogDisplayFields(log);
    assert.equal(result.channelId, 77);
    // 渠道已删除/无名字时，不再暴露内部字面量 token，落到空串由外层显示占位。
    assert.equal(result.channelName, '');
});

test('resolveLogDisplayFields falls back to chat when only generic chat models exist', () => {
    const log = buildLog({
        request_model_name: 'gpt-4o-mini',
        actual_model_name: 'gpt-4o-mini',
    });

    const result = resolveLogDisplayFields(log);
    assert.equal(result.endpointType, 'chat');
});

test('resolveLogDisplayFields exposes cache read tokens from detail or list payload', () => {
    const log = buildLog({
        cache_read_tokens: 120,
    });

    const fromList = resolveLogDisplayFields(log);
    assert.equal(fromList.cacheReadTokens, 120);

    const detail: RelayLogDetail = {
        ...log,
        cache_read_tokens: 240,
        request_content: '{}',
        response_content: '{}',
    };
    const fromDetail = resolveLogDisplayFields(log, detail);
    assert.equal(fromDetail.cacheReadTokens, 240);
});

test('resolveLogDisplayFields exposes semantic cache hit flag from detail or list payload', () => {
    const log = buildLog({
        semantic_cache_hit: true,
    });

    const fromList = resolveLogDisplayFields(log);
    assert.equal(fromList.semanticCacheHit, true);

    const detail: RelayLogDetail = {
        ...log,
        semantic_cache_hit: false,
        request_content: '{}',
        response_content: '{}',
    };
    const fromDetail = resolveLogDisplayFields(log, detail);
    assert.equal(fromDetail.semanticCacheHit, false);
});

test('resolveLogDisplayFields infers MiMo Chat request type label', () => {
    const log = buildLog({
        request_model_name: 'mimo-v2.5-pro',
        actual_model_name: 'mimo-v2.5-pro',
    });

    const result = resolveLogDisplayFields(log);
    assert.equal(result.requestTypeKey, 'mimoChat');
});

test('resolveLogDisplayFields infers streaming chat request type label from request content', () => {
    const log = buildLog({
        request_model_name: 'gpt-4o-mini',
        actual_model_name: 'gpt-4o-mini',
    });
    const detail: RelayLogDetail = {
        ...log,
        request_content: '{"stream":true}',
        response_content: '{}',
    };

    const result = resolveLogDisplayFields(log, detail);
    assert.equal(result.requestTypeKey, 'streamingChat');
});

test('resolveLogDisplayFields infers embedding request type label', () => {
    const log = buildLog({
        endpoint_type: 'embeddings',
        request_model_name: 'text-embedding-3-small',
        actual_model_name: 'text-embedding-3-small',
    });

    const result = resolveLogDisplayFields(log);
    assert.equal(result.requestTypeKey, 'embedding');
});

test('resolveLogDisplayFields returns empty request type key for endpoints without a dedicated label', () => {
    const log = buildLog({
        endpoint_type: 'rerank',
        request_model_name: 'bge-reranker-v2-m3',
        actual_model_name: 'bge-reranker-v2-m3',
    });

    const result = resolveLogDisplayFields(log);
    assert.equal(result.requestTypeKey, '');
    assert.equal(result.endpointType, 'rerank');
});

test('formatJsonForCopy pretty-prints minified JSON with two-space indent', () => {
    const result = formatJsonForCopy('{"role":"system","content":"hi"}');
    assert.equal(result, '{\n  "role": "system",\n  "content": "hi"\n}');
});

test('formatJsonForCopy preserves already-formatted JSON semantics', () => {
    const result = formatJsonForCopy('{\n  "a": 1\n}');
    assert.deepEqual(JSON.parse(result), { a: 1 });
});

test('formatJsonForCopy returns non-JSON content unchanged', () => {
    const raw = 'not json { broken';
    assert.equal(formatJsonForCopy(raw), raw);
});

test('formatJsonForCopy returns empty string for empty or missing input', () => {
    assert.equal(formatJsonForCopy(''), '');
    assert.equal(formatJsonForCopy(undefined), '');
    assert.equal(formatJsonForCopy(null), '');
});




test('resolveLogDisplayFields prefers successful attempt adapter_type as outbound protocol', () => {
    const log = buildLog({
        attempts: [
            {
                channel_id: 1,
                channel_name: 'A',
                model_name: 'gpt-5.5',
                adapter_type: 'chat',
                attempt_num: 1,
                status: 'failed',
                duration: 5,
            },
            {
                channel_id: 2,
                channel_name: 'B',
                model_name: 'gpt-5.5',
                adapter_type: 'response',
                attempt_num: 2,
                status: 'success',
                duration: 8,
            },
        ],
    });
    const result = resolveLogDisplayFields(log);
    assert.equal(result.outboundAdapterType, 'response');
});

test('resolveLogDisplayFields falls back to last non-empty adapter_type when no success', () => {
    const log = buildLog({
        attempts: [
            {
                channel_id: 1,
                channel_name: 'A',
                model_name: 'claude',
                adapter_type: 'anthropic',
                attempt_num: 1,
                status: 'failed',
                duration: 5,
            },
            {
                channel_id: 2,
                channel_name: 'B',
                model_name: 'claude',
                adapter_type: '',
                attempt_num: 2,
                status: 'failed',
                duration: 5,
            },
        ],
    });
    const result = resolveLogDisplayFields(log);
    assert.equal(result.outboundAdapterType, 'anthropic');
});

test('reportedIPSourceToken maps display-track headers to badge tokens', () => {
    assert.equal(reportedIPSourceToken('cf-connecting-ip'), 'cf');
    assert.equal(reportedIPSourceToken('true-client-ip'), 'tru');
    assert.equal(reportedIPSourceToken('x-forwarded-for'), 'xff');
    assert.equal(reportedIPSourceToken(' True-Client-IP '), 'tru');
    assert.equal(reportedIPSourceToken('none'), '');
    assert.equal(reportedIPSourceToken(''), '');
    assert.equal(reportedIPSourceToken(undefined), '');
    assert.equal(reportedIPSourceToken(null), '');
});

test('resolveLogDisplayFields separates a real client IP from the Render proxy fallback', () => {
    const reported = resolveLogDisplayFields(buildLog({
        client_ip: '10.0.0.5',
        reported_client_ip: '198.51.100.20',
        reported_client_ip_source: 'true-client-ip',
    }));
    assert.equal(reported.hasReportedClientIP, true);
    assert.equal(reported.clientIP, '198.51.100.20');
    assert.equal(reported.rawClientIP, '10.0.0.5');
    assert.equal(reportedIPSourceToken(reported.reportedClientIPSource), 'tru');

    const proxyFallback = resolveLogDisplayFields(buildLog({ client_ip: '10.0.0.5', reported_client_ip: '' }));
    assert.equal(proxyFallback.hasReportedClientIP, false);
    assert.equal(proxyFallback.clientIP, '10.0.0.5');
    assert.equal(proxyFallback.rawClientIP, '10.0.0.5');
    assert.equal(proxyFallback.reportedClientIPSource, '');
});

test('resolveLogInvestigationMarkers flags fake-streaming bursts on long requests', () => {
    // GG 假流式-cache：首字占满单次时长（260.112s / 260.379s，输出 18447 token）
    const fakeCache = resolveLogInvestigationMarkers({
        ftut: 260112,
        use_time: 260379,
        output_tokens: 18447,
        usage_state: 'reported',
        provider_termination_reason: 'complete',
    });
    assert.deepEqual(fakeCache, ['suspect_fake_stream']);

    // 旧 GG 变体：首字 205.3s / 总 509.4s（占比 40.3%，走次判定）
    const legacyVariant = resolveLogInvestigationMarkers({
        ftut: 205299,
        use_time: 509415,
        output_tokens: 23002,
        usage_state: 'reported',
        provider_termination_reason: '',
    });
    assert.deepEqual(legacyVariant, ['suspect_fake_stream']);

    // 尾部突发：首字占比不高，但首字后 82ms 内灌入 10957 token（13.3 万 tok/s）
    const tailBurst = resolveLogInvestigationMarkers({
        ftut: 244183,
        use_time: 244265,
        output_tokens: 10957,
        usage_state: 'reported',
        provider_termination_reason: '',
    });
    assert.deepEqual(tailBurst, ['suspect_fake_stream']);
});

test('resolveLogInvestigationMarkers leaves real streaming logs unmarked', () => {
    // New Cross 真流式：首字 19.5s / 总 90.7s（21.5%），尾部速率 ~99 tok/s
    const realStream = resolveLogInvestigationMarkers({
        ftut: 19538,
        use_time: 90709,
        output_tokens: 7012,
        usage_state: 'reported',
        provider_termination_reason: 'complete',
    });
    assert.deepEqual(realStream, []);

    // 短请求：9.584s / 9.828s（占比 97.5% 但总时长 < 60s，不标记）
    const shortRequest = resolveLogInvestigationMarkers({
        ftut: 9584,
        use_time: 9828,
        output_tokens: 69,
        usage_state: 'reported',
        provider_termination_reason: 'stop',
    });
    assert.deepEqual(shortRequest, []);

    // 非流式请求：ftut=0
    const nonStreaming = resolveLogInvestigationMarkers({
        ftut: 0,
        use_time: 2200,
        output_tokens: 120,
        usage_state: 'reported',
        provider_termination_reason: 'stop',
    });
    assert.deepEqual(nonStreaming, []);
});

test('resolveLogInvestigationMarkers ignores retry-chain time before the first visible token', () => {
    // 10/14 16:14:02（yuecheng）：前 5 次尝试失败共 102s，第 6 次 23.8s 正常流式；
    // 未扣除重试耗时会把首字占比抬到 89% 造成误报。
    const retriedRealStream = resolveLogInvestigationMarkers({
        ftut: 111960,
        use_time: 125815,
        output_tokens: 314,
        usage_state: 'reported',
        provider_termination_reason: '',
        termination_cause: '',
        attempts: [
            { attempt_num: 1, channel_id: 97, channel_name: 'GG', model_name: '假流式-a', status: 'failed', duration: 183 },
            { attempt_num: 2, channel_id: 97, channel_name: 'GG', model_name: '假流式-a', status: 'failed', duration: 190 },
            { attempt_num: 3, channel_id: 289, channel_name: 'Bosco', model_name: 'gemini-3.1-pro', status: 'failed', duration: 101482 },
            { attempt_num: 4, channel_id: 97, channel_name: 'GG', model_name: '抗截断-a', status: 'failed', duration: 73 },
            { attempt_num: 5, channel_id: 97, channel_name: 'GG', model_name: '抗截断-a', status: 'failed', duration: 68 },
            { attempt_num: 6, channel_id: 51, channel_name: 'yuecheng', model_name: 'gemini-3.1-pro', status: 'success', duration: 23804 },
        ],
    });
    assert.deepEqual(retriedRealStream, []);

    // 10/14 02:10:02（GG，20K 长输出）：4 次失败共 186s，最后一次 230.5s 真流式。
    const longOutputAfterRetries = resolveLogInvestigationMarkers({
        ftut: 247533,
        use_time: 456899,
        output_tokens: 20263,
        usage_state: 'reported',
        provider_termination_reason: '',
        termination_cause: '',
        attempts: [
            { attempt_num: 1, channel_id: 335, channel_name: 'wududu', model_name: 'gemini-3.1-pro-low', status: 'skipped', duration: 0 },
            { attempt_num: 2, channel_id: 97, channel_name: 'GG', model_name: 'gemini-3.1-pro-preview-cache', status: 'failed', duration: 69330 },
            { attempt_num: 3, channel_id: 97, channel_name: 'GG', model_name: 'gemini-3.1-pro-preview-cache', status: 'failed', duration: 34138 },
            { attempt_num: 4, channel_id: 97, channel_name: 'GG', model_name: 'gemini-3.1-pro-preview-cache', status: 'failed', duration: 47109 },
            { attempt_num: 5, channel_id: 97, channel_name: 'GG', model_name: 'gemini-3.1-pro-preview-cache', status: 'failed', duration: 35722 },
            { attempt_num: 6, channel_id: 97, channel_name: 'GG', model_name: 'gemini-3.1-pro-preview-cache', status: 'success', duration: 230510 },
        ],
    });
    assert.deepEqual(longOutputAfterRetries, []);

    // 对照组：失败重试之后仍是一次「临近结束才集中输出」的真伪流式，扣除后依旧成立。
    const dumpAfterRetries = resolveLogInvestigationMarkers({
        ftut: 200000,
        use_time: 200300,
        output_tokens: 10000,
        usage_state: 'reported',
        provider_termination_reason: '',
        termination_cause: '',
        attempts: [
            { attempt_num: 1, channel_id: 9, channel_name: 'dead', model_name: 'x', status: 'failed', duration: 100000 },
            { attempt_num: 2, channel_id: 97, channel_name: 'GG', model_name: '假流式-x', status: 'success', duration: 100300 },
        ],
    });
    assert.deepEqual(dumpAfterRetries, ['suspect_fake_stream']);
});

test('resolveLogInvestigationMarkers separates uncounted output from truly empty output', () => {
    // 烁-gemini「回答 OK 却自报 completion=0」：已写出 567 字节 → 输出量未计，不再冒充空输出。
    const uncounted = resolveLogInvestigationMarkers({
        ftut: 4335,
        use_time: 4483,
        output_tokens: 0,
        usage_state: 'reported',
        provider_termination_reason: 'STOP',
        termination_cause: 'complete',
        client_write_bytes: 567,
    });
    assert.deepEqual(uncounted, ['output_uncounted']);

    // 后端权威成因（empty_output）优先，即使有字节写出也按空输出标注。
    const backendEmpty = resolveLogInvestigationMarkers({
        ftut: 800,
        use_time: 1500,
        output_tokens: 0,
        usage_state: 'empty_output',
        provider_termination_reason: '',
        termination_cause: '',
        client_write_bytes: 120,
    });
    assert.deepEqual(backendEmpty, ['empty_output']);

    // 历史行缺 client_write_bytes 时保持旧行为，不猜测。
    const legacyNoBytes = resolveLogInvestigationMarkers({
        ftut: 0,
        use_time: 3000,
        output_tokens: 0,
        usage_state: 'reported',
        provider_termination_reason: '',
        termination_cause: '',
    });
    assert.deepEqual(legacyNoBytes, ['empty_output']);
});

test('resolveLogInvestigationMarkers maps usage_state causes to markers', () => {
    const build = (usageState: string, extra: Partial<Parameters<typeof resolveLogInvestigationMarkers>[0]> = {}) =>
        resolveLogInvestigationMarkers({
            ftut: 1200,
            use_time: 3400,
            output_tokens: 40,
            usage_state: usageState,
            provider_termination_reason: 'stop',
            ...extra,
        });

    assert.deepEqual(build('missing_terminal'), ['stream_interrupted']);
    assert.deepEqual(build('empty_output'), ['empty_output']);
    assert.deepEqual(build('client_disconnected'), ['client_interrupted']);
    assert.deepEqual(build('not_reported'), ['usage_not_reported']);
    assert.deepEqual(build('reported'), []);
    // 空 usage_state（历史行）不猜测成因
    assert.deepEqual(build(''), []);

    // 长度截断：上游终止原因 length / max_tokens（大小写不敏感）
    assert.deepEqual(
        build('reported', { provider_termination_reason: 'length' }),
        ['length_truncated'],
    );
    assert.deepEqual(
        build('reported', { provider_termination_reason: 'MAX_TOKENS' }),
        ['length_truncated'],
    );
});

test('resolveLogInvestigationMarkers flags trusted zero-output successes as empty output', () => {
    // 烁-gemini 早停样本（13:07:25）：成功、用量可信但输出 0 token
    const zeroOutput = resolveLogInvestigationMarkers({
        ftut: 9171,
        use_time: 14252,
        output_tokens: 0,
        usage_state: 'reported',
        provider_termination_reason: 'complete',
        termination_cause: 'complete',
    });
    assert.deepEqual(zeroOutput, ['empty_output']);

    // 上游拦截（内容过滤）的零输出有明确解释，不标空输出
    const filtered = resolveLogInvestigationMarkers({
        ftut: 800,
        use_time: 1500,
        output_tokens: 0,
        usage_state: 'reported',
        provider_termination_reason: 'content_filter',
        termination_cause: 'content_filter',
    });
    assert.deepEqual(filtered, []);

    // 客户端中断导致的零输出只标客户端中断
    const disconnected = resolveLogInvestigationMarkers({
        ftut: 0,
        use_time: 309702,
        output_tokens: 0,
        usage_state: 'client_disconnected',
        provider_termination_reason: '',
        termination_cause: '',
    });
    assert.deepEqual(disconnected, ['client_interrupted']);

    // 历史行（无 usage_state）不猜测
    const legacy = resolveLogInvestigationMarkers({
        ftut: 0,
        use_time: 126257,
        output_tokens: 0,
        usage_state: '',
        provider_termination_reason: '',
        termination_cause: '',
    });
    assert.deepEqual(legacy, []);
});

test('resolveLogInvestigationMarkers stacks independent markers', () => {
    // 合成叠加用例：伪流式指纹（首字 110.7s / 总 112.9s）+ 客户端断开成因
    const stacked = resolveLogInvestigationMarkers({
        ftut: 110709,
        use_time: 112930,
        output_tokens: 234,
        usage_state: 'client_disconnected',
        provider_termination_reason: '',
    });
    assert.deepEqual(stacked, ['suspect_fake_stream', 'client_interrupted']);
});
