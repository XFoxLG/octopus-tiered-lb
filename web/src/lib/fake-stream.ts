/**
 * 「疑似伪流式」判定（日志与渠道维度共用）。
 *
 * 背景：部分上游渠道收到请求后先以心跳/空包维持连接，等模型在服务端把内容
 * 全部生成完才集中吐出。网关侧可观测的指纹是：首个可见 Token 出现得非常晚，
 * 晚到占掉单次请求时长的大部分。
 *
 * 阈值来自 2026-10-14 用 60 条真实日志做的校准：真流式请求的首字占比普遍
 * 低于 22%，而实锤伪流式渠道（先心跳、最后 ~0.3 秒灌完整篇正文）为 99.8%–100%。
 *
 * 文案一律带「疑似」：长思考模型也会出现高首字占比，最终需要结合响应内容人工确认。
 */

/** 判定生效的最短单次时长：低于 60 秒的请求首字占比没有诊断价值（小请求普遍偏高）。 */
export const SUSPECT_FAKE_STREAM_MIN_TOTAL_MS = 60_000;

/** 主判定：首字耗时占单次时长 ≥ 60%。 */
export const SUSPECT_FAKE_STREAM_MIN_RATIO = 0.6;

/** 次判定：首字本身 ≥ 60 秒且占比 ≥ 40%（覆盖旧 GG 变体 205s/509s 这类样本）。 */
export const SUSPECT_FAKE_STREAM_LONG_HEAD_MS = 60_000;
export const SUSPECT_FAKE_STREAM_LONG_HEAD_RATIO = 0.4;

/**
 * 判定「首字很晚 / 内容集中到达」的伪流式指纹。
 * firstTokenMs、totalMs 均为毫秒；任一侧缺失（0）时返回 false，不产生标记。
 */
export function isSuspectFakeStreamSignature(firstTokenMs: number, totalMs: number): boolean {
    if (!(firstTokenMs > 0) || !(totalMs > 0)) return false;
    if (totalMs < SUSPECT_FAKE_STREAM_MIN_TOTAL_MS) return false;
    const ratio = firstTokenMs / totalMs;
    if (ratio >= SUSPECT_FAKE_STREAM_MIN_RATIO) return true;
    return firstTokenMs >= SUSPECT_FAKE_STREAM_LONG_HEAD_MS && ratio >= SUSPECT_FAKE_STREAM_LONG_HEAD_RATIO;
}

/** 首字时长的紧凑展示：<1s 用毫秒，否则保留两位小数的秒。 */
export function formatFirstTokenMs(ms: number): string {
    if (!(ms > 0)) return '—';
    if (ms < 1000) return `${Math.round(ms)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}
