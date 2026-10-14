import type { ChannelAttempt, RelayLog, RelayLogDetail } from '../../../api/endpoints/log.ts';
import { inferCapabilities, type CapabilityType } from '../group/capabilities.ts';
import { isSuspectFakeStreamSignature } from '../../../lib/fake-stream.ts';

const capabilityEndpointMap: Record<Exclude<CapabilityType, 'chat' | 'moderation'>, string> = {
    embeddings: 'embeddings',
    rerank: 'rerank',
    image_generation: 'image_generation',
    audio_speech: 'audio_speech',
    audio_transcription: 'audio_transcription',
    video_generation: 'video_generation',
    music_generation: 'music_generation',
    search: 'search',
};

function firstNonEmpty(...values: Array<string | null | undefined>) {
    for (const value of values) {
        const trimmed = value?.trim();
        if (trimmed) return trimmed;
    }
    return '';
}

function lastAttemptValue(
    attempts: ChannelAttempt[] | undefined,
    pick: (attempt: ChannelAttempt) => string | undefined,
) {
    if (!attempts?.length) return '';
    for (let index = attempts.length - 1; index >= 0; index -= 1) {
        const value = pick(attempts[index])?.trim();
        if (value) return value;
    }
    return '';
}

function firstNonZero(...values: Array<number | null | undefined>) {
    for (const value of values) {
        if (typeof value === 'number' && value > 0) return value;
    }
    return 0;
}

/**
 * 统一的「耗时」展示：0、负数或缺失表示该指标没有被记录（例如非流式请求
 * 不记录首字），用破折号而不是误导性的 0ms。
 */
export function formatLogDuration(ms: number | null | undefined): string {
    if (typeof ms !== 'number' || !Number.isFinite(ms) || ms <= 0) return '—';
    if (ms < 1000) return `${Math.round(ms)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}

export interface RelayLogFirstTokenMilestones {
    /** 上游首个可见内容相对请求开始的毫秒数；0 表示未观测到。 */
    upstreamFirstVisibleMs: number;
    /** 网关首次 Write+Flush 相对请求开始的毫秒数；0 表示未观测到。 */
    clientFirstFlushMs: number;
}

/**
 * 详情页两个首字里程碑（相对请求开始的毫秒数；0 = 未观测到，调用方隐藏该行）。
 *
 * 精确口径：后端 `ftut` 就是 `client_first_flush_at - 请求开始`（同一时间戳相减），
 * `upstream_first_visible_at` 与 `client_first_flush_at` 也来自同一时钟，两者之差
 * 精确。所以：
 *   客户端首字 = ftut
 *   上游首字   = ftut +（upstream_first_visible_at − client_first_flush_at）
 *
 * 为什么不直接「时间戳 − log.time」：log.time 是 Unix **秒**（被截断），最多会多算
 * 0.999s，实测出现过「上游首字 10.16s > 总耗时 9.83s」的矛盾。缺 ftut 的历史
 * 数据（或非流式）退回秒级差值计算，行为与旧版一致。
 */
export function resolveFirstTokenMilestones(
    log: Pick<RelayLog, 'time' | 'ftut' | 'upstream_first_visible_at' | 'client_first_flush_at'>,
): RelayLogFirstTokenMilestones {
    const upstreamAt = typeof log.upstream_first_visible_at === 'number' && log.upstream_first_visible_at > 0
        ? log.upstream_first_visible_at
        : 0;
    const clientAt = typeof log.client_first_flush_at === 'number' && log.client_first_flush_at > 0
        ? log.client_first_flush_at
        : 0;
    const ftut = typeof log.ftut === 'number' && log.ftut > 0 ? log.ftut : 0;

    if (ftut > 0 && clientAt > 0) {
        return {
            upstreamFirstVisibleMs: upstreamAt > 0 ? Math.max(0, ftut + (upstreamAt - clientAt)) : 0,
            clientFirstFlushMs: ftut,
        };
    }

    const startedAtMs = typeof log.time === 'number' && log.time > 0 ? log.time * 1000 : 0;
    const deltaFromSeconds = (milestone: number) => {
        if (startedAtMs <= 0 || milestone <= 0) return 0;
        const value = milestone - startedAtMs;
        return value > 0 ? value : 0;
    };
    return {
        upstreamFirstVisibleMs: deltaFromSeconds(upstreamAt),
        clientFirstFlushMs: deltaFromSeconds(clientAt),
    };
}

/**
 * 日志调查标记：把「排查问题时需要一眼看到」的异常指纹从日志字段里算出来。
 * 全部基于列表接口已返回的字段，不新增查询；文案一律带「疑似」或直接引用
 * 后端 usage_state 的成因标注，不做超出证据的判断。
 */
export type RelayLogInvestigationMarker =
    | 'suspect_fake_stream'
    | 'length_truncated'
    | 'stream_interrupted'
    | 'empty_output'
    | 'output_uncounted'
    | 'client_interrupted'
    | 'usage_not_reported';

/** 首字后内容以异常高的速率集中到达：真流式约 80–110 tok/s，实锤伪流式为 6.5 万–13 万 tok/s。 */
const FAKE_STREAM_BURST_MIN_TPS = 1500;

/**
 * 扣除「首个可见内容之前就已结束的尝试」消耗的时间。
 *
 * `ftut`/`use_time` 都是相对**整个请求**（含失败重试链）计的，而「疑似伪流式」要看的
 * 是真正产出内容那一次尝试内部的首字占比。实测案例（2026-10-14 16:14:02）：前 5 次
 * 尝试失败共 102s（Bosco 单次 504 就白等 101.5s），第 6 次 23.8s 正常流式，未扣除时
 * 占比 89% 被误标为伪流式；02:10 的 20K 长输出样本同理（4 次失败 186s）。
 * 判定键是尝试顺序 + 各自耗时：累加直到某次尝试跨越首个可见内容为止。
 */
function resolveLeadingAttemptMs(attempts: ChannelAttempt[] | undefined, firstVisibleMs: number): number {
    if (!Array.isArray(attempts) || firstVisibleMs <= 0) return 0;
    let leading = 0;
    for (const attempt of attempts) {
        const duration = typeof attempt?.duration === 'number' && attempt.duration > 0 ? attempt.duration : 0;
        if (duration <= 0) continue;
        if (leading + duration > firstVisibleMs) break;
        leading += duration;
    }
    return leading;
}

export function resolveLogInvestigationMarkers(
    log: Pick<RelayLog, 'ftut' | 'use_time' | 'output_tokens' | 'usage_state' | 'provider_termination_reason' | 'termination_cause' | 'attempts' | 'client_write_bytes'>,
): RelayLogInvestigationMarker[] {
    const markers: RelayLogInvestigationMarker[] = [];
    const ftutRaw = typeof log.ftut === 'number' ? log.ftut : 0;
    const useTimeRaw = typeof log.use_time === 'number' ? log.use_time : 0;
    const outputTokens = typeof log.output_tokens === 'number' ? log.output_tokens : 0;
    const usageState = log.usage_state ?? '';
    // 伪流式判据必须扣掉「首个可见内容之前」的失败重试耗时，否则重试链会抬高首字占比。
    const leadingAttemptMs = resolveLeadingAttemptMs(log.attempts, ftutRaw);
    const ftut = ftutRaw > 0 ? Math.max(0, ftutRaw - leadingAttemptMs) : 0;
    const useTime = useTimeRaw > 0 ? Math.max(0, useTimeRaw - leadingAttemptMs) : 0;

    if (outputTokens > 0) {
        if (isSuspectFakeStreamSignature(ftut, useTime)) {
            markers.push('suspect_fake_stream');
        } else if (ftut > 0 && useTime > ftut) {
            // 首字占比不高但首字之后内容以极高速度集中到达（尾部突发）。
            const tailTps = outputTokens / ((useTime - ftut) / 1000);
            if (tailTps >= FAKE_STREAM_BURST_MIN_TPS) {
                markers.push('suspect_fake_stream');
            }
        }
    }

    const providerReason = (log.provider_termination_reason ?? '').trim().toLowerCase();
    if (providerReason === 'length' || providerReason === 'max_tokens') {
        markers.push('length_truncated');
    }
    // 空输出：后端成因标注，或「用量可信但输出 0 token」的成功日志（实测案例：
    // 输出 0.00 却以 stop/complete 收尾的早停渠道）。内容被上游拦截的终止原因
    // 不算空输出（有明确解释）。
    const terminationCause = (log.termination_cause ?? '').trim().toLowerCase();
    const explainedNoOutput = ['content_filter', 'refusal', 'prompt_blocked'].includes(terminationCause);
    const emptyOutputByUsage = usageState === 'empty_output';
    const zeroTokensReported =
        outputTokens === 0 &&
        (usageState === 'reported' || usageState === 'not_reported') &&
        !explainedNoOutput;
    // 「输出 Token 为 0」有两种成因：真没内容（空输出）；或渠道回报了请求用量但完成量
    // 恒为 0、实际有内容写出（实测 烁-gemini 渠道：回答 OK 却自报 completion=0）。
    // 后者标「输出量未计」，不再冒充「空输出」。
    const deliveredBytes = typeof log.client_write_bytes === 'number' ? log.client_write_bytes : null;
    if (emptyOutputByUsage) {
        markers.push('empty_output');
    } else if (zeroTokensReported) {
        if (deliveredBytes !== null && deliveredBytes > 0) {
            markers.push('output_uncounted');
        } else {
            markers.push('empty_output');
        }
    }
    if (usageState === 'missing_terminal') markers.push('stream_interrupted');
    if (usageState === 'client_disconnected') markers.push('client_interrupted');
    if (usageState === 'not_reported') markers.push('usage_not_reported');

    return markers;
}

export type ReportedIPSourceToken = 'cf' | 'tru' | 'xff' | '';

/**
 * 把展示轨来源头映射成前端徽标 token。
 * cf-connecting-ip → cf；true-client-ip → tru；x-forwarded-for → xff；
 * none、未知值或空值 → ''（外层按「代理 IP」呈现，不标注来源）。
 */
export function reportedIPSourceToken(source: string | null | undefined): ReportedIPSourceToken {
    const normalized = source?.trim().toLowerCase();
    if (normalized === 'cf-connecting-ip') return 'cf';
    if (normalized === 'true-client-ip') return 'tru';
    if (normalized === 'x-forwarded-for') return 'xff';
    return '';
}

function lastAttemptChannelId(attempts: ChannelAttempt[] | undefined) {
    if (!attempts?.length) return 0;
    for (let index = attempts.length - 1; index >= 0; index -= 1) {
        const value = attempts[index]?.channel_id;
        if (typeof value === 'number' && value > 0) return value;
    }
    return 0;
}

/** 取最终成功 attempt 的 adapter_type；无成功则取最后一个非空 adapter_type。 */
function resolveOutboundAdapterType(attempts: ChannelAttempt[] | undefined): string {
    if (!attempts?.length) return '';
    for (let i = attempts.length - 1; i >= 0; i--) {
        const a = attempts[i];
        if (a.status === 'success' && a.adapter_type?.trim()) {
            return a.adapter_type.trim();
        }
    }
    for (let i = attempts.length - 1; i >= 0; i--) {
        const t = attempts[i].adapter_type?.trim();
        if (t) return t;
    }
    return '';
}


function isStreamRequest(requestContent?: string | null) {
    if (!requestContent) return false;
    try {
        const parsed = JSON.parse(requestContent) as { stream?: unknown };
        return parsed.stream === true;
    } catch {
        return false;
    }
}

function inferRequestTypeKey(endpointType: string, modelNames: string[], requestContent?: string | null) {
    const normalizedEndpoint = endpointType.trim().toLowerCase();
    const normalizedNames = modelNames
        .map((name) => name.trim().toLowerCase())
        .filter(Boolean);
    const streaming = isStreamRequest(requestContent);

    if (normalizedEndpoint === 'embeddings') return 'embedding';
    if (normalizedEndpoint === 'responses') return 'responses';
    if (normalizedEndpoint === 'messages') return 'anthropicMessages';
    if (normalizedEndpoint === 'gemini') return 'gemini';
    if (normalizedEndpoint === 'mimo' || normalizedNames.some((name) => name.includes('mimo'))) return 'mimoChat';
    if (normalizedNames.some((name) => name.includes('gemini'))) return 'gemini';
    if (normalizedNames.some((name) => name.includes('claude'))) return 'anthropicMessages';
    if (normalizedNames.some((name) => name.includes('doubao') || name.includes('volcengine') || name.includes('ark'))) return 'volcengine';
    if (normalizedEndpoint === 'deepseek' || normalizedEndpoint === 'chat') {
        return streaming ? 'streamingChat' : 'chat';
    }
    // Unrecognized endpoint types (rerank, image_generation, ...) have no
    // dedicated request-type label. Return empty so the caller falls back to
    // the endpoint-type label instead of triggering a missing i18n key.
    return normalizedEndpoint ? '' : (streaming ? 'streamingChat' : 'chat');
}
function inferEndpointTypeFromModels(modelNames: string[]) {
    const normalizedNames = modelNames
        .map((name) => name.trim().toLowerCase())
        .filter(Boolean);

    if (normalizedNames.some((name) => name.includes('deepseek'))) {
        return 'deepseek';
    }
    if (normalizedNames.some((name) => name.includes('mimo'))) {
        return 'mimo';
    }

    for (const modelName of normalizedNames) {
        const capability = inferCapabilities(modelName).find((item) => item !== 'chat');
        if (!capability) continue;
        if (capability === 'moderation') return 'moderations';
        return capabilityEndpointMap[capability];
    }

    return normalizedNames.length > 0 ? 'chat' : '';
}

export function resolveLogDisplayFields(
    log: RelayLog,
    detail?: RelayLogDetail | null,
    channelNameById?: ReadonlyMap<number, string>,
) {
    const mergedAttempts = detail?.attempts?.length ? detail.attempts : log.attempts;

    const requestModelName = firstNonEmpty(detail?.request_model_name, log.request_model_name);
    const requestContent = firstNonEmpty(detail?.request_content, '');
    const actualModelName = firstNonEmpty(
        detail?.actual_model_name,
        log.actual_model_name,
        lastAttemptValue(mergedAttempts, (attempt) => attempt.model_name),
        requestModelName,
    );
    const endpointType = firstNonEmpty(
        detail?.endpoint_type,
        log.endpoint_type,
        inferEndpointTypeFromModels([
            actualModelName,
            requestModelName,
            lastAttemptValue(mergedAttempts, (attempt) => attempt.model_name),
        ]),
    );
    const channelId = firstNonZero(detail?.channel, log.channel, lastAttemptChannelId(mergedAttempts));
    const channelName = firstNonEmpty(
        detail?.channel_name,
        log.channel_name,
        lastAttemptValue(mergedAttempts, (attempt) => attempt.channel_name),
        channelId > 0 ? channelNameById?.get(channelId) : '',
    );
    // Keep a reported address paired with the header that supplied it.
    const reportedIPRecord = detail?.reported_client_ip?.trim() ? detail : log;
    const reportedClientIP = firstNonEmpty(reportedIPRecord.reported_client_ip);
    const rawClientIP = firstNonEmpty(detail?.client_ip, log.client_ip);

    return {
        requestAPIKeyName: firstNonEmpty(detail?.request_api_key_name, log.request_api_key_name),
        clientIP: firstNonEmpty(reportedClientIP, rawClientIP),
        // reported 为空时 clientIP 会回退到平台代理地址，用它区分「真实来源」与「代理 IP」。
        hasReportedClientIP: Boolean(reportedClientIP),
        rawClientIP,
        reportedClientIPSource: reportedClientIP ? firstNonEmpty(reportedIPRecord.reported_client_ip_source) : '',
        requestModelName,
        actualModelName,
        endpointType,
        requestTypeKey: inferRequestTypeKey(endpointType, [actualModelName, requestModelName], requestContent),
        outboundAdapterType: resolveOutboundAdapterType(mergedAttempts),
        channelId,
        channelName,
        semanticCacheHit: detail?.semantic_cache_hit ?? log.semantic_cache_hit ?? false,
        cacheReadTokens: detail?.cache_read_tokens ?? log.cache_read_tokens ?? 0,
    };
}

// formatJsonForCopy pretty-prints JSON content for clipboard use so that copied
// request/response bodies keep their newlines and indentation instead of being
// pasted as a single minified line. Non-JSON content is returned unchanged.
export function formatJsonForCopy(content: string | undefined | null): string {
    if (!content) return '';
    try {
        return JSON.stringify(JSON.parse(content), null, 2);
    } catch {
        return content;
    }
}


