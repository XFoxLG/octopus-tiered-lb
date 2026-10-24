'use client';

import { memo, useMemo, useState } from 'react';
import { Clock, Cpu, Gauge, Zap, AlertCircle, ArrowDownToLine, ArrowUpFromLine, DollarSign, JapaneseYen, ArrowRight, ArrowDown, Percent, RotateCw, ChevronDown, ChevronUp, Pin, KeyRound, Globe, TestTube2, Sigma, Brain, Type, Image as ImageIcon } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { motion, AnimatePresence } from 'motion/react';
import { useTheme } from 'next-themes';
import { type RelayLog, type ChannelAttempt, useLogDetail } from '@/api/endpoints/log';
import { getModelIcon, resolveBrandColor } from '@/lib/model-icons';
import { Badge } from '@/components/ui/badge';
import { cn, formatCount, formatMoney } from '@/lib/utils';
import { formatUnixSeconds } from '@/lib/time';
import { endpointTypeLabelKey } from '@/components/modules/group/utils';
import { formatLogDuration, isUpstreamModelMismatch, reportedIPSourceToken, resolveEffectiveTokenCounts, resolveFirstTokenMilestones, resolveLogDisplayFields, resolveLogInvestigationMarkers, type RelayLogInvestigationMarker } from './display';
import { BoundaryDetails } from './BoundaryDetails';
import { logIssueSummary } from './body-preview';
import { useLogFieldVisibility, useLogFieldVisibilityStore } from './ui-store';
import { useSettingStore } from '@/stores/setting';
import { CopyIconButton } from '@/components/common/CopyButton';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
} from '@/components/ui/morphing-dialog';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';

/** Format count or money result into a compact display string like "1.23 万". */
function fmt({ value, unit }: { value: string; unit: string }) {
    return unit ? `${value} ${unit}` : value;
}

/** Format CNY cost with 2 significant figures. */
function costFmt(cny: number): string {
    if (cny === 0) return "0.00";
    const abs = Math.abs(cny);
    if (abs >= 1) return cny.toFixed(2);
    // Determine decimal places needed for 2 sig figs, then round and format
    const exp = Math.floor(Math.log10(abs));
    const decimals = Math.max(0, 1 - exp);
    const rounded = Number(abs.toFixed(decimals + 1));
    const s = Number(rounded.toPrecision(2));
    const finalDecimals = Math.max(0, 1 - Math.floor(Math.log10(s)));
    return (cny < 0 ? "-" : "") + s.toFixed(finalDecimals);
}

function formatTime(timestamp: number): string {
    return formatUnixSeconds(timestamp, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
    });
}

function formatDuration(ms: number): string {
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}

/** Format tokens-per-second for display. */
function formatTPS(tokens: number, timeMs: number): string {
    if (tokens <= 0 || timeMs <= 0) return '- tk/s';
    const seconds = timeMs / 1000;
    const tps = tokens / seconds;
    if (tps >= 100) return `${tps.toFixed(0)} tk/s`;
    if (tps >= 10) return `${tps.toFixed(1)} tk/s`;
    return `${tps.toFixed(2)} tk/s`;
}

/** 客户端主动断开的错误文本（relay 后端 errClientDisconnected 的固定文案）。
 * 它表示客户端自己取消了请求，不是渠道故障，展示时降级为中性提示而不是红色错误。 */
const CLIENT_DISCONNECT_TEXT = 'client disconnected';

function isClientDisconnectText(text?: string | null): boolean {
    return !!text && text.trim() === CLIENT_DISCONNECT_TEXT;
}

/** Format cache hit rate = cacheReadTokens / totalTokens. */
function formatCacheHitRate(cacheRead: number, total: number): string {
    if (cacheRead <= 0 || total <= 0) return '-';
    const rate = (cacheRead / total) * 100;
    if (rate >= 100) return '100%';
    if (rate >= 10) return `${rate.toFixed(1)}%`;
    return `${rate.toFixed(2)}%`;
}

/** Resolve the badge styling and label key for a single attempt status.
 *
 * Skipped (cooldown / disabled / no key) and circuit-break attempts never
 * reached the upstream, so they are rendered with a neutral muted tone instead
 * of destructive red — otherwise an all-cooldown request looks like a wall of
 * red failures in the relay log (issue #95 改动6).
 */
function attemptStatusBadge(status: ChannelAttempt['status']): {
    className: string;
    labelKey: 'success' | 'failed' | 'cancelled' | 'skipped' | 'circuitBreak';
} {
    switch (status) {
        case 'success':
            return { className: 'bg-primary/15 text-primary', labelKey: 'success' };
        case 'cancelled':
            return { className: 'bg-slate-500/15 text-slate-600 dark:text-slate-300', labelKey: 'cancelled' };
        case 'circuit_break':
            return { className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400', labelKey: 'circuitBreak' };
        case 'skipped':
            return { className: 'bg-muted text-muted-foreground', labelKey: 'skipped' };
        case 'failed':
        default:
            return { className: 'bg-destructive/15 text-destructive', labelKey: 'failed' };
    }
}

/** Resolve the detail-card container and message accent for a single attempt status.
 *
 * Only real upstream failures use the destructive red accent. Skipped and
 * circuit-break attempts use muted/amber tones so cooldown-only requests don't
 * paint the whole diagnostic panel red (issue #95 改动6).
 */
function attemptStatusCard(status: ChannelAttempt['status']): {
    card: string;
    msg: string;
} {
    switch (status) {
        case 'success':
            return { card: 'bg-primary/5 border-primary/20 hover:bg-primary/10', msg: 'text-foreground/80 border-foreground/20' };
        case 'cancelled':
            return { card: 'bg-slate-500/5 border-slate-500/20 hover:bg-slate-500/10', msg: 'text-slate-600 dark:text-slate-300 border-slate-500/30' };
        case 'circuit_break':
            return { card: 'bg-amber-500/5 border-amber-500/20 hover:bg-amber-500/10', msg: 'text-amber-700 dark:text-amber-300 border-amber-500/30' };
        case 'skipped':
            return { card: 'bg-muted/40 border-border/50 hover:bg-muted/60', msg: 'text-muted-foreground border-border/40' };
        case 'failed':
        default:
            return { card: 'bg-destructive/5 border-destructive/20 hover:bg-destructive/10', msg: 'text-destructive/90 border-destructive/30' };
    }
}

interface RetryBadgeWithTooltipProps {
    channelName: string;
    brandColor: string;
    attempts: ChannelAttempt[];
    channelNameById?: ReadonlyMap<number, string>;
}

function RetryBadgeWithTooltip({ channelName, brandColor, attempts, channelNameById }: RetryBadgeWithTooltipProps) {
    const t = useTranslations('log.card');
    const { resolvedTheme } = useTheme();
    const badgeColor = resolveBrandColor(brandColor, resolvedTheme === 'dark');

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <Badge
                    variant="secondary"
                    className="shrink-0 text-xs px-1.5 py-0.5 cursor-help font-medium"
                    style={{ backgroundColor: `${badgeColor}15`, color: badgeColor }}
                >
                    <RotateCw className="size-3 mr-1 opacity-80" />
                    {channelName}
                </Badge>
            </TooltipTrigger>
            <TooltipContent className="border bg-card p-2 min-w-[280px] max-w-[calc(100vw-2rem)] rounded-xl flex flex-col gap-1">
                {attempts.map((attempt, idx) => (
                    <div key={idx} className="flex flex-col w-full">
                        <div className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-muted/50 transition-colors">
                            {(() => {
                                const badge = attemptStatusBadge(attempt.status);
                                return (
                                    <Badge
                                        className={cn(
                                            "h-5 shrink-0 px-1.5 text-[10px] font-bold uppercase shadow-none border-0",
                                            badge.className
                                        )}
                                    >
                                        {t(badge.labelKey)}
                                    </Badge>
                                );
                            })()}
                            <div className="flex min-w-0 flex-col flex-1">
                                <span className="truncate text-xs font-semibold text-foreground">
                                    {attempt.channel_name?.trim() || channelNameById?.get(attempt.channel_id) || `Channel #${attempt.channel_id}`}
                                </span>
                                <span className="text-[10px] text-muted-foreground">
                                    {attempt.model_name}{attempt.adapter_type ? ` • ${attempt.adapter_type}` : ''} • {formatDuration(attempt.duration)}
                                </span>
                            </div>
                        </div>
                        {
                            idx < attempts.length - 1 && (
                                <div className="flex justify-center py-0.5">
                                    <ArrowDown className="size-3 text-muted-foreground/30" />
                                </div>
                            )
                        }
                    </div>
                ))}
            </TooltipContent>
        </Tooltip >
    );
}

// 调查标记徽标（v2.8.10）：把排查时需要一眼看到的异常指纹标在卡片与详情底部。
// 文案与阈值解释都走 i18n，避免界面出现无法理解的缩写。
const INVESTIGATION_MARKER_LABEL_KEY: Record<RelayLogInvestigationMarker, string> = {
    suspect_fake_stream: 'investigation.suspectFakeStream',
    length_truncated: 'investigation.lengthTruncated',
    stream_interrupted: 'investigation.streamInterrupted',
    empty_output: 'investigation.emptyOutput',
    output_uncounted: 'investigation.outputUncounted',
    client_interrupted: 'investigation.clientInterrupted',
    usage_not_reported: 'investigation.usageNotReported',
    model_mismatch: 'investigation.modelMismatch',
};

const INVESTIGATION_MARKER_HINT_KEY: Record<RelayLogInvestigationMarker, string> = {
    suspect_fake_stream: 'investigation.suspectFakeStreamHint',
    length_truncated: 'investigation.lengthTruncatedHint',
    stream_interrupted: 'investigation.streamInterruptedHint',
    empty_output: 'investigation.emptyOutputHint',
    output_uncounted: 'investigation.outputUncountedHint',
    client_interrupted: 'investigation.clientInterruptedHint',
    usage_not_reported: 'investigation.usageNotReportedHint',
    model_mismatch: 'investigation.modelMismatchHint',
};

const INVESTIGATION_MARKER_CLASS: Record<RelayLogInvestigationMarker, string> = {
    suspect_fake_stream: 'border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400',
    length_truncated: 'border-destructive/50 bg-destructive/10 text-destructive',
    stream_interrupted: 'border-destructive/50 bg-destructive/10 text-destructive',
    empty_output: 'border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400',
    output_uncounted: 'border-border/60 bg-muted/60 text-muted-foreground',
    client_interrupted: 'border-border/60 bg-muted/60 text-muted-foreground',
    usage_not_reported: 'border-border/60 bg-muted/60 text-muted-foreground',
    model_mismatch: 'border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400',
};

function LogInvestigationBadge({ marker }: { marker: RelayLogInvestigationMarker }) {
    const t = useTranslations('log.card');
    return (
        <span
            className={cn('shrink-0 rounded border px-1.5 py-0.5 text-[10px] leading-4', INVESTIGATION_MARKER_CLASS[marker])}
            title={t(INVESTIGATION_MARKER_HINT_KEY[marker])}
        >
            {t(INVESTIGATION_MARKER_LABEL_KEY[marker])}
        </span>
    );
}

export const LogCard = memo(function LogCard({ log, channelNameById, channelKeyLabelById }: {
    log: RelayLog;
    channelNameById?: ReadonlyMap<number, string>;
    // 上游渠道 Key 标识：channel_key_id → 「备注 / 脱敏 Key」；本地只读取已加载的渠道列表，不新增后端字段。
    channelKeyLabelById?: ReadonlyMap<number, string>;
}) {
    const t = useTranslations('log.card');
    const tCommon = useTranslations('common');
    const tGroup = useTranslations('group');
    const { detail, error: detailError, isLoading: isDetailLoading, fetchDetail, reset: resetDetail } = useLogDetail();
    const hasError = !!log.error;
    const clientDisconnected = isClientDisconnectText(log.error);
    const hasMultipleAttempts = log.attempts && log.attempts.length > 1;
    // forwardedCount 统计真实发往上游的尝试次数（成功+失败），排除冷却跳过与熔断跳过。
    // 当它小于总尝试次数时，单独展示以便区分「实际请求报错」与「冷却中未请求」
    // （issue #95 改动6）。
    const forwardedCount = useMemo(
        () => (log.attempts ?? []).filter((a) => a.status === 'success' || a.status === 'failed' || a.status === 'cancelled').length,
        [log.attempts],
    );
    const [isDiagnosticExpanded, setIsDiagnosticExpanded] = useState(false);
    const displayFields = useMemo(() => resolveLogDisplayFields(log, detail, channelNameById), [channelNameById, detail, log]);
    // 首字双里程碑（上游首个可见内容 / 客户端首次写出）：日志时间戳是 Unix
    // 毫秒，log.time 是请求开始的 Unix 秒。列表接口不返回这两个字段，所以优先
    // 用详情数据；未观测到的里程碑不展示对应行。
    const firstTokenMilestones = useMemo(
        () => resolveFirstTokenMilestones(detail ?? log),
        [detail, log],
    );
    // 调查标记：疑似伪流式、长度截断、各类用量成因（v2.8.10）。纯前端计算，
    // 数据全部来自列表接口已有字段。
    const investigationMarkers = useMemo(() => resolveLogInvestigationMarkers(log), [log]);
    const vis = useLogFieldVisibility();
    const compact = useLogFieldVisibilityStore(s => s.compact);
    const chinaMode = useSettingStore((s) => s.chinaMode);
    const { Avatar: ModelAvatar, color: brandColor } = useMemo(
        () => getModelIcon(displayFields.actualModelName),
        [displayFields.actualModelName]
    );
    // 深色模式下黑/深色系品牌色（Grok/Kimi/Replicate/MiniMax/Yi 等）直接用作
    // Badge 文字会「黑字+透明黑底」看不清，按主题解析为可读颜色（issue: 日志深色模式对比度）
    const { resolvedTheme } = useTheme();
    const badgeColor = resolveBrandColor(brandColor, resolvedTheme === 'dark');
    const requestAPIKeyName = displayFields.requestAPIKeyName;
    const clientIP = displayFields.clientIP;
    const ipSourceToken = reportedIPSourceToken(displayFields.reportedClientIPSource);
    const cacheReadTokens = displayFields.cacheReadTokens;
    const semanticCacheHit = displayFields.semanticCacheHit;
    // v2.8.13：上游漏报的方向优先使用网关估算值（est > 0），其余显示上报值。
    const tokenCounts = resolveEffectiveTokenCounts(log);
    const effectiveInputTokens = Math.max(0, tokenCounts.inputTokens - cacheReadTokens);
    const inputLabel = cacheReadTokens > 0 ? t('realInput') : t('input');
    const displayChannelName = displayFields.channelName || '-';
    const displayEndpointType = useMemo(() => {
        const adapter = displayFields.outboundAdapterType;
        if (adapter) {
            const label = t(`adapterLabels.${adapter}`);
            // next-intl 缺 key 时可能回传 key 路径；有专用文案用文案，否则用原始 adapter
            if (label && !label.includes('adapterLabels.')) return label;
            return adapter;
        }
        // 旧日志无 adapter_type 时回退 endpoint/request type，避免整块空白
        const reqTypeKey = displayFields.requestTypeKey;
        if (reqTypeKey) {
            const label = t(`requestTypeLabels.${reqTypeKey}`);
            if (label && !label.includes('requestTypeLabels.')) return label;
        }
        const rawEndpointType = displayFields.endpointType;
        if (!rawEndpointType) return '';
        const labelKey = endpointTypeLabelKey(rawEndpointType);
        return labelKey ? tGroup(labelKey) : rawEndpointType;
    }, [displayFields.endpointType, displayFields.outboundAdapterType, displayFields.requestTypeKey, t, tGroup]);
    const displayActualModelName = displayFields.actualModelName || '-';
    const displayRequestModelName = displayFields.requestModelName || log.request_model_name;

    const responseContent = detail?.response_content;
    // usageKnown 判定该条日志的用量是否可信。后端 usage_state 有值时按成因直接判定
    //（reported=可信，其余成因=不可信）；历史行无标注（空值）退回旧启发式：
    // 有 token/费用/错误即视为"已知"，否则再探测响应里的 usage 块。
    const usageKnown = useMemo(() => {
        if (log.usage_state) {
            return log.usage_state === 'reported';
        }
        if (log.input_tokens > 0 || log.output_tokens > 0 || Number(log.cost) > 0) {
            return true;
        }
        if (log.error) {
            return true;
        }
        if (!responseContent) {
            return false;
        }
        try {
            const parsed = JSON.parse(responseContent) as { usage?: unknown };
            return parsed.usage !== undefined;
        } catch {
            return false;
        }
    }, [log.usage_state, log.cost, log.error, log.input_tokens, log.output_tokens, responseContent]);

    // 不可信时的成因文案：按 usage_state 显示真实原因，而不是笼统的"未知"。
    const usageStateHint = useMemo(() => {
        if (!log.usage_state || log.usage_state === 'reported') return '';
        return t(`usageState.${log.usage_state}`);
    }, [log.usage_state, t]);

    // v2.8.13：估算行以「≈」前缀标注并挂估算说明 tooltip；费用与统计仍按上报值计算。
    const estimatedHint = t('investigation.estimatedHint');
    const inputTokenDisplay = tokenCounts.inputEstimated
        ? `≈${fmt(formatCount(effectiveInputTokens).formatted)}`
        : usageKnown
            ? fmt(formatCount(effectiveInputTokens).formatted)
            : (usageStateHint || tCommon('unknown'));
    const outputTokenDisplay = tokenCounts.outputEstimated
        ? `≈${fmt(formatCount(tokenCounts.outputTokens).formatted)}`
        : usageKnown
            ? fmt(formatCount(tokenCounts.outputTokens).formatted)
            : (usageStateHint || tCommon('unknown'));
    // 总消耗 = 真实输入 + 缓存输入 + 输出 = input_tokens(含缓存) + output_tokens（issue #107）
    const totalTokenDisplay = tokenCounts.estimated
        ? `≈${fmt(formatCount(tokenCounts.totalTokens).formatted)}`
        : usageKnown
            ? fmt(formatCount(tokenCounts.totalTokens).formatted)
            : (usageStateHint || tCommon('unknown'));
    const costDisplay = usageKnown
        ? (chinaMode
            ? costFmt(formatMoney(Number(log.cost)).raw)
            : formatMoney(Number(log.cost)).raw.toFixed(2))
        : (usageStateHint || tCommon('unknown'));

    return (
        // Root layout already provides the single global TooltipProvider (shared
        // GlobalTooltipProvider + one TooltipOverlay) used by every other module
        // (channel/group/setting cards). Wrapping EACH retry LogCard in its own
        // provider creates many parallel global tooltip singletons — each with its
        // own layoutId animation + useFloating autoUpdate listeners — which spirals
        // into a layout-animation loop and OOMs the tab on hover. Reuse the global one.
        <MorphingDialog onOpen={() => fetchDetail(log.id)} onClose={resetDetail}>
                <MorphingDialogTrigger
                    className={cn(
                        "rounded-xl border bg-card w-full text-left",
                        hasError ? "border-destructive/40" : "border-border",
                    )}
                >
                    <div className={cn("p-2.5 sm:p-4 grid grid-cols-[auto_1fr] gap-2.5 sm:gap-4", hasError ? "items-start" : "items-center")}>
                        <div className="sm:hidden"><ModelAvatar size={36} /></div>
                        <div className="hidden sm:block"><ModelAvatar size={40} /></div>
                        <div className="min-w-0 flex flex-col gap-2">
                            <div className="flex min-w-0 flex-wrap items-center gap-2 text-sm md:flex-nowrap">
                                <span className="min-w-0 max-w-full font-semibold text-card-foreground truncate md:max-w-[32%]" title={displayRequestModelName}>
                                    {displayRequestModelName}
                                </span>
                                {log.is_test && (
                                    <Badge
                                        variant="outline"
                                        className="shrink-0 text-xs px-1.5 py-0 border-blue-400/50 text-blue-500 dark:text-blue-400"
                                        title={t('testLog')}
                                    >
                                        <TestTube2 className="size-3 mr-1" />
                                        {t('testLog')}
                                    </Badge>
                                )}
                                {(log.image_count ?? 0) > 0 && (
                                    <Badge
                                        variant="outline"
                                        className="shrink-0 text-xs px-1.5 py-0 border-violet-400/50 text-violet-500 dark:text-violet-400"
                                        title={t('imageBadgeHint')}
                                    >
                                        <ImageIcon className="size-3 mr-1" />
                                        {t('imageBadge', { count: log.image_count ?? 0 })}
                                    </Badge>
                                )}
                                {vis.endpointType && displayEndpointType && (
                                    <Badge
                                        variant="secondary"
                                        className="max-w-full shrink-0 text-xs px-1.5 py-0"
                                        style={{ backgroundColor: `${badgeColor}15`, color: badgeColor }}
                                        title={displayEndpointType}
                                    >
                                        <span className="block max-w-[10rem] truncate">{displayEndpointType}</span>
                                    </Badge>
                                )}
                                {vis.channelName && (
                                    <>
                                        <ArrowRight className="size-3.5 shrink-0 text-muted-foreground/50" />
                                        {hasMultipleAttempts ? (
                                            <RetryBadgeWithTooltip
                                                channelName={displayChannelName}
                                                brandColor={brandColor}
                                                attempts={log.attempts!}
                                                channelNameById={channelNameById}
                                            />
                                        ) : (
                                            <Badge
                                                variant="secondary"
                                                className="max-w-full shrink-0 text-xs px-1.5 py-0"
                                                style={{ backgroundColor: `${badgeColor}15`, color: badgeColor }}
                                                title={displayChannelName}
                                            >
                                                <span className="block max-w-[18rem] truncate">{displayChannelName}</span>
                                            </Badge>
                                        )}
                                    </>
                                )}
                                {vis.actualModel && (!compact || displayActualModelName !== displayRequestModelName) && (
                                    <span className="min-w-0 text-muted-foreground truncate md:flex-1" title={displayActualModelName}>
                                        {displayActualModelName}
                                    </span>
                                )}
                                {log.attempts?.some(a => a.sticky) && (
                                    <Pin className="size-3.5 shrink-0 text-amber-500" />
                                )}
                            </div>
                            {investigationMarkers.length > 0 && (
                                <div className="flex flex-wrap gap-1.5">
                                    {investigationMarkers.map((marker) => (
                                        <LogInvestigationBadge key={marker} marker={marker} />
                                    ))}
                                </div>
                            )}
                            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-xs tabular-nums text-muted-foreground">
                                <div className="flex items-center gap-1.5">
                                    <Clock className="size-3.5 shrink-0" style={{ color: badgeColor }} />
                                    <span>{formatTime(log.time)}</span>
                                </div>
                                {vis.apiKeyName && requestAPIKeyName && (
                                    <div className="flex items-center gap-1.5">
                                        <KeyRound className="size-3.5 shrink-0 text-orange-500" />
                                        <span className="truncate" title={requestAPIKeyName}>
                                            {requestAPIKeyName}
                                        </span>
                                    </div>
                                )}
                                {vis.clientIP && (displayFields.hasReportedClientIP || displayFields.rawClientIP) && (
                                    <div className="flex items-center gap-1.5">
                                        <Globe className={cn('size-3.5 shrink-0', displayFields.hasReportedClientIP ? 'text-sky-500' : 'text-muted-foreground/60')} />
                                        {displayFields.hasReportedClientIP ? <>
                                            <span className="truncate" title={clientIP}>{clientIP}</span>
                                            {ipSourceToken && (
                                                <span className="shrink-0 rounded-sm bg-sky-500/10 px-1 text-[10px] leading-4 text-sky-600 dark:text-sky-400" title={t('reportedIPHint', { source: displayFields.reportedClientIPSource })}>
                                                    {t(`ipSource.${ipSourceToken}`)}
                                                </span>
                                            )}
                                        </> : (
                                            // Render 等托管平台上取不到转发来源时，直连地址是平台代理 IP，
                                            // 不能按真实客户端展示，只给灰色「代理 IP」并提示原始值。
                                            <span className="truncate text-muted-foreground/70" title={t('ipProxyTooltip', { ip: displayFields.rawClientIP })}>
                                                {t('ipProxy')}
                                            </span>
                                        )}
                                    </div>
                                )}
                                <div className="flex items-center gap-1.5">
                                    <Zap className="size-3.5 shrink-0 text-amber-500" />
                                    {log.ftut > 0 ? (
                                        <span>{t('firstToken')} {formatLogDuration(log.ftut)}</span>
                                    ) : (
                                        <Tooltip>
                                            <TooltipTrigger asChild>
                                                <span className="cursor-help underline decoration-dotted underline-offset-2">
                                                    {t('firstToken')} {formatLogDuration(log.ftut)}
                                                </span>
                                            </TooltipTrigger>
                                            <TooltipContent className="border bg-card p-2 rounded-xl text-xs">
                                                {t('firstTokenNotRecorded')}
                                            </TooltipContent>
                                        </Tooltip>
                                    )}
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <Cpu className="size-3.5 shrink-0 text-blue-500" />
                                    <span>{t('totalTime')} {formatDuration(log.use_time)}</span>
                                </div>
                                {vis.tps && (
                                    <div className="flex items-center gap-1.5">
                                        <Gauge className="size-3.5 shrink-0 text-lime-500" />
                                        <span>{t('tps')} {formatTPS(tokenCounts.outputTokens, log.use_time)}</span>
                                    </div>
                                )}
                                {vis.cacheHitRate && cacheReadTokens > 0 && (
                                    <div className="flex items-center gap-1.5">
                                        <Percent className="size-3.5 shrink-0 text-teal-500" />
                                        <span>{t('cacheHitRate')} {formatCacheHitRate(cacheReadTokens, tokenCounts.totalTokens)}</span>
                                    </div>
                                )}
                                <div className="flex items-center gap-1.5">
                                    <ArrowDownToLine className="size-3.5 shrink-0 text-green-500" />
                                    <span title={tokenCounts.inputEstimated ? estimatedHint : undefined}>
                                        {inputLabel} {inputTokenDisplay}
                                    </span>
                                </div>
                                {semanticCacheHit && (
                                    <div className="flex items-center gap-1.5">
                                        <ArrowDownToLine className="size-3.5 shrink-0 text-cyan-500" />
                                        <span>{t('semanticCacheHit')}</span>
                                    </div>
                                )}
                                {cacheReadTokens > 0 && (
                                    <div className="flex items-center gap-1.5">
                                        <ArrowDownToLine className="size-3.5 shrink-0 text-teal-500" />
                                        <span>{t('cacheHit')} {fmt(formatCount(cacheReadTokens).formatted)}</span>
                                    </div>
                                )}
                                <div className="flex items-center gap-1.5">
                                    <ArrowUpFromLine className="size-3.5 shrink-0 text-purple-500" />
                                    <span title={tokenCounts.outputEstimated ? estimatedHint : undefined}>
                                        {t('output')} {outputTokenDisplay}
                                    </span>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <Sigma className="size-3.5 shrink-0 text-rose-500" />
                                    <span
                                        className="font-medium text-rose-600 dark:text-rose-400"
                                        title={tokenCounts.estimated ? estimatedHint : undefined}
                                    >
                                        {t('totalTokens')} {totalTokenDisplay}
                                    </span>
                                </div>
                                {vis.cost && (
                                    <div className="flex items-center gap-1.5">
                                        {chinaMode ? <JapaneseYen className="size-3.5 shrink-0 text-emerald-500" /> : <DollarSign className="size-3.5 shrink-0 text-emerald-500" />}
                                        <span className="font-medium text-emerald-600 dark:text-emerald-400">
                                            {t('cost')} {costDisplay}
                                        </span>
                                        {log.billing_window === 'peak' && (
                                            <Badge
                                                variant="outline"
                                                className="shrink-0 text-xs px-1.5 py-0 border-amber-400/50 text-amber-500 dark:text-amber-400"
                                                title={t('billingWindowHint')}
                                            >
                                                {t('billingWindowPeak')}
                                            </Badge>
                                        )}
                                        {log.billing_window === 'offpeak' && (
                                            <Badge
                                                variant="outline"
                                                className="shrink-0 text-xs px-1.5 py-0 border-sky-400/50 text-sky-500 dark:text-sky-400"
                                                title={t('billingWindowHint')}
                                            >
                                                {t('billingWindowOffPeak')}
                                            </Badge>
                                        )}
                                    </div>
                                )}
                                {vis.reasoningEffort && !!log.reasoning_effort && (
                                    <div className="flex items-center gap-1.5">
                                        <Brain className="size-3.5 shrink-0 text-violet-500" />
                                        <span>{t('reasoningEffort')} {log.reasoning_effort}</span>
                                    </div>
                                )}
                                {vis.reasoningTokens && (log.reasoning_tokens ?? 0) > 0 && (
                                    <div className="flex items-center gap-1.5">
                                        <Brain className="size-3.5 shrink-0 text-indigo-500" />
                                        <span>{t('reasoningTokens')} {fmt(formatCount(log.reasoning_tokens ?? 0).formatted)}{t('reasoningTokensUnit')}</span>
                                    </div>
                                )}
                                {vis.reasoningTokens && (log.reasoning_tokens ?? 0) <= 0 && (log.reasoning_chars ?? 0) > 0 && (
                                    <div className="flex items-center gap-1.5">
                                        <Type className="size-3.5 shrink-0 text-indigo-500" />
                                        <span>{t('reasoningChars')} {fmt(formatCount(log.reasoning_chars ?? 0).formatted)}{t('reasoningCharsUnit')}</span>
                                    </div>
                                )}
                            </div>
                            {hasError && (
                                <div
                                    className={cn(
                                        "p-2.5 rounded-xl border overflow-hidden",
                                        clientDisconnected ? "bg-muted/60 border-border/60" : "bg-destructive/10 border-destructive/20",
                                    )}
                                    title={clientDisconnected ? t('clientDisconnectedHint') : undefined}
                                >
                                    <p className={cn("text-xs line-clamp-2", clientDisconnected ? "text-muted-foreground" : "text-destructive")}>
                                        {clientDisconnected ? t('clientDisconnected') : compact ? t(`issueSummary.${logIssueSummary(log.error || '')}`) : log.error}
                                    </p>
                                </div>
                            )}
                        </div>
                    </div>
                </MorphingDialogTrigger>

                <MorphingDialogContainer>
                    <MorphingDialogContent className="relative flex max-h-[calc(100dvh-2rem)] min-h-0 w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-xl bg-card px-6 py-4 text-card-foreground md:w-[95vw] md:max-w-7xl">
                        <MorphingDialogClose className="top-4 right-5 text-muted-foreground hover:text-foreground transition-colors" />
                        <MorphingDialogTitle className="flex items-center gap-2 mb-3 pr-12 text-sm">
                            <ModelAvatar size={28} />
                            <span className="min-w-0 shrink truncate font-semibold text-card-foreground" title={displayRequestModelName}>{displayRequestModelName}</span>
                            {log.is_test && (
                                <Badge
                                    variant="outline"
                                    className="shrink-0 text-xs px-1.5 py-0 border-blue-400/50 text-blue-500 dark:text-blue-400"
                                >
                                    <TestTube2 className="size-3 mr-1" />
                                    {t('testLog')}
                                </Badge>
                            )}
                            {(log.image_count ?? 0) > 0 && (
                                <Badge
                                    variant="outline"
                                    className="shrink-0 text-xs px-1.5 py-0 border-violet-400/50 text-violet-500 dark:text-violet-400"
                                    title={t('imageBadgeHint')}
                                >
                                    <ImageIcon className="size-3 mr-1" />
                                    {t('imageBadge', { count: log.image_count ?? 0 })}
                                </Badge>
                            )}
                            <ArrowRight className="size-3.5 text-muted-foreground/50" />
                            {vis.endpointType && displayEndpointType && (
                                <Badge
                                    variant="secondary"
                                    className="max-w-full shrink-0 text-xs px-1.5 py-0"
                                    style={{ backgroundColor: `${badgeColor}15`, color: badgeColor }}
                                    title={displayEndpointType}
                                >
                                    <span className="block max-w-[10rem] truncate">{displayEndpointType}</span>
                                </Badge>
                            )}
                            {vis.channelName && (
                                <>
                                    <ArrowRight className="size-3.5 text-muted-foreground/50" />
                                    {hasMultipleAttempts ? (
                                        <RetryBadgeWithTooltip
                                            channelName={displayChannelName}
                                            brandColor={brandColor}
                                            attempts={log.attempts!}
                                            channelNameById={channelNameById}
                                        />
                                    ) : (
                                        <Badge
                                            variant="secondary"
                                            className="max-w-full text-xs px-1.5 py-0"
                                            style={{ backgroundColor: `${badgeColor}15`, color: badgeColor }}
                                            title={displayChannelName}
                                        >
                                            <span className="block max-w-[18rem] truncate">{displayChannelName}</span>
                                        </Badge>
                                    )}
                                </>
                            )}
                            {vis.actualModel && (
                                <span className="min-w-0 flex-1 truncate text-muted-foreground" title={displayActualModelName}>{displayActualModelName}</span>
                            )}
                            {log.upstream_model && (
                                <span
                                    className={cn(
                                        'shrink-0 rounded border px-1 py-0.5 text-[10px]',
                                        isUpstreamModelMismatch(log.upstream_model, log.actual_model_name)
                                            ? 'border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400'
                                            : 'border-border/60 bg-muted/60 text-muted-foreground',
                                    )}
                                    title={t('investigation.upstreamModelHint')}
                                >
                                    {t('investigation.upstreamModelLabel')}: {log.upstream_model}
                                </span>
                            )}
                            {log.attempts?.some(a => a.sticky) && (
                                <Pin className="size-3.5 shrink-0 text-amber-500" />
                            )}
                        </MorphingDialogTitle>

                        <MorphingDialogDescription className="flex min-h-0 flex-1 overflow-hidden">
                            <div className="min-h-0 flex-1 space-y-4 overflow-y-auto">
                                {(hasError || hasMultipleAttempts) && (
                                    <div className={cn(
                                        "flex flex-col rounded-xl border",
                                        hasError
                                            ? "bg-destructive/5 border-destructive/20"
                                            : "bg-secondary/30 border-border/50"
                                    )}>
                                        <button
                                            type="button"
                                            className={cn(
                                                "flex w-full items-center gap-2 px-3 py-2.5 shrink-0 cursor-pointer select-none text-left hover:bg-muted/50 transition-colors",
                                                hasError && "hover:bg-destructive/10"
                                            )}
                                            onClick={() => setIsDiagnosticExpanded(!isDiagnosticExpanded)}
                                            aria-expanded={isDiagnosticExpanded}
                                            aria-controls={`log-diagnostics-${log.id}`}
                                        >
                                            {hasError ? (
                                                <AlertCircle className="size-4 text-destructive" />
                                            ) : (
                                                <RotateCw className="size-4 text-muted-foreground" />
                                            )}
                                            <span className={cn(
                                                "text-sm font-medium",
                                                hasError ? "text-destructive" : "text-secondary-foreground"
                                            )}>
                                                {hasError ? t('errorInfo') : t('retryDetails')}
                                            </span>
                                            <div className="ml-auto flex items-center gap-2">
                                                {hasMultipleAttempts && (
                                                    <Badge
                                                        variant="outline"
                                                        className={cn(
                                                            "text-xs border-0",
                                                            hasError
                                                                ? "bg-destructive/10 text-destructive"
                                                                : "bg-secondary text-secondary-foreground"
                                                        )}
                                                    >
                                                        {log.total_attempts || log.attempts!.length} {t('attempts')}
                                                    </Badge>
                                                )}
                                                {hasMultipleAttempts && forwardedCount < (log.total_attempts || log.attempts!.length) && (
                                                    <Badge
                                                        variant="outline"
                                                        className="text-xs border-0 bg-muted/50 text-muted-foreground"
                                                        title={t('forwardedAttempts', { count: forwardedCount })}
                                                    >
                                                        {t('forwardedAttempts', { count: forwardedCount })}
                                                    </Badge>
                                                )}
                                                {isDiagnosticExpanded ? (
                                                    <ChevronUp className="size-4 text-muted-foreground" />
                                                ) : (
                                                    <ChevronDown className="size-4 text-muted-foreground" />
                                                )}
                                            </div>
                                        </button>

                                        <AnimatePresence initial={false}>
                                            {isDiagnosticExpanded && (
                                                <motion.div
                                                    id={`log-diagnostics-${log.id}`}
                                                    initial={{ height: 0, opacity: 0 }}
                                                    animate={{ height: "auto", opacity: 1 }}
                                                    exit={{ height: 0, opacity: 0 }}
                                                    transition={{ duration: 0.2, ease: "easeInOut" }}
                                                    className="overflow-hidden flex flex-col min-h-0"
                                                >
                                                    <div className="flex-1 overflow-auto p-2.5 md:p-3 flex flex-col gap-4">
                                                        {hasError && (
                                                            <div className="relative pl-1">
                                                                <div className="absolute right-0 top-0">
                                                                    <CopyIconButton
                                                                        text={log.error ?? ''}
                                                                        className="p-1 rounded-md text-destructive/60 hover:text-destructive hover:bg-destructive/10 transition-colors"
                                                                        copyIconClassName="size-4"
                                                                        checkIconClassName="size-4"
                                                                    />
                                                                </div>
                                                                <p className="text-sm text-destructive whitespace-pre-wrap wrap-break-word pr-8 leading-relaxed">
                                                                    {log.error}
                                                                </p>
                                                            </div>
                                                        )}

                                                        {hasMultipleAttempts && (
                                                            <div className="flex flex-col gap-2">
                                                                {log.attempts!.map((attempt, idx) => {
                                                                    const statusBadge = attemptStatusBadge(attempt.status);
                                                                    const statusCard = attemptStatusCard(attempt.status);
                                                                    return (
                                                                    <div
                                                                        key={idx}
                                                                        className={cn(
                                                                            "text-xs p-2.5 rounded-xl border transition-colors flex flex-col gap-2",
                                                                            isClientDisconnectText(attempt.msg)
                                                                                ? "bg-muted/40 border-border/50 hover:bg-muted/60"
                                                                                : statusCard.card
                                                                        )}
                                                                    >
                                                                        <div className="flex items-center gap-2">
                                                                            <Badge
                                                                                className={cn(
                                                                                    "h-5 shrink-0 px-1.5 text-[10px] font-bold uppercase shadow-none border-0",
                                                                                    isClientDisconnectText(attempt.msg)
                                                                                        ? "bg-muted text-muted-foreground"
                                                                                        : statusBadge.className
                                                                                )}
                                                                            >
                                                                                {isClientDisconnectText(attempt.msg) ? t('clientDisconnected') : t(statusBadge.labelKey)}
                                                                            </Badge>
                                                                            <span className="font-semibold text-foreground">
                                                                                {attempt.channel_name?.trim() || channelNameById?.get(attempt.channel_id) || `Channel #${attempt.channel_id}`}
                                                                            </span>
                                                                            <span className="text-muted-foreground">
                                                                                ({attempt.model_name})
                                                                            </span>
                                                                            {attempt.adapter_type && (
                                                                                <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-muted text-muted-foreground font-medium">
                                                                                    {attempt.adapter_type}
                                                                                </span>
                                                                            )}
                                                                            {typeof attempt.channel_key_id === 'number' && attempt.channel_key_id > 0 && (
                                                                                <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-muted/70 text-muted-foreground" title={t('channelKeyHint')}>
                                                                                    {channelKeyLabelById?.get(attempt.channel_key_id) ?? t('channelKeyFallback', { id: attempt.channel_key_id })}
                                                                                </span>
                                                                            )}
                                                                            <span className="ml-auto text-muted-foreground tabular-nums font-mono">
                                                                                {formatDuration(attempt.duration)}
                                                                            </span>
                                                                        </div>
                                                                        {attempt.msg && (
                                                                            <div className={cn(
                                                                                "pl-2 border-l-2 text-[11px] leading-relaxed",
                                                                                isClientDisconnectText(attempt.msg)
                                                                                    ? "text-muted-foreground border-border/60"
                                                                                    : statusCard.msg
                                                                            )}>
                                                                                {isClientDisconnectText(attempt.msg) ? t('clientDisconnected') : attempt.msg}
                                                                            </div>
                                                                        )}
                                                                    </div>
                                                                    );
                                                                })}
                                                            </div>
                                                        )}
                                                    </div>
                                                </motion.div>
                                            )}
                                        </AnimatePresence>
                                    </div>
                                )}
                                <BoundaryDetails detail={detail} error={detailError} isLoading={isDetailLoading} />
                            </div>
                        </MorphingDialogDescription>

                        <div className="shrink-0 border-t border-border/50 pt-3 text-xs text-muted-foreground">
                            {investigationMarkers.length > 0 && (
                                <div className="mb-2 flex flex-wrap gap-1.5">
                                    {investigationMarkers.map((marker) => (
                                        <LogInvestigationBadge key={marker} marker={marker} />
                                    ))}
                                </div>
                            )}
                            <div className="flex flex-wrap items-center gap-3 md:gap-4">
                            <div className="flex items-center gap-1.5">
                                <Clock className="size-3.5" style={{ color: badgeColor }} />
                                <span className="tabular-nums">{formatTime(log.time)}</span>
                            </div>
                            {vis.apiKeyName && requestAPIKeyName && (
                                <div className="flex min-w-0 items-center gap-1.5">
                                    <KeyRound className="size-3.5 shrink-0 text-orange-500" />
                                    <span className="truncate" title={requestAPIKeyName}>
                                        {requestAPIKeyName}
                                    </span>
                                </div>
                            )}
                            <div className="flex items-center gap-1.5">
                                <Zap className="size-3.5 text-amber-500" />
                                {log.ftut > 0 ? (
                                    <span>{t('firstTokenTime')}: {formatLogDuration(log.ftut)}</span>
                                ) : (
                                    <Tooltip>
                                        <TooltipTrigger asChild>
                                            <span className="cursor-help underline decoration-dotted underline-offset-2">
                                                {t('firstTokenTime')}: {formatLogDuration(log.ftut)}
                                            </span>
                                        </TooltipTrigger>
                                        <TooltipContent className="border bg-card p-2 rounded-xl text-xs">
                                            {t('firstTokenNotRecorded')}
                                        </TooltipContent>
                                    </Tooltip>
                                )}
                            </div>
                            {firstTokenMilestones.upstreamFirstVisibleMs > 0 && (
                                <div className="flex items-center gap-1.5">
                                    <ArrowDown className="size-3.5 text-sky-500" />
                                    <span>{t('upstreamFirstToken')}: {formatLogDuration(firstTokenMilestones.upstreamFirstVisibleMs)}</span>
                                </div>
                            )}
                            {firstTokenMilestones.clientFirstFlushMs > 0 && (
                                <div className="flex items-center gap-1.5">
                                    <ArrowUpFromLine className="size-3.5 text-emerald-500" />
                                    <span>{t('clientFirstFlush')}: {formatLogDuration(firstTokenMilestones.clientFirstFlushMs)}</span>
                                </div>
                            )}
                            <div className="flex items-center gap-1.5">
                                <Cpu className="size-3.5 text-blue-500" />
                                <span>{t('totalTime')}: {formatDuration(log.use_time)}</span>
                            </div>
                            {vis.tps && (
                                <div className="flex items-center gap-1.5">
                                    <Gauge className="size-3.5 text-lime-500" />
                                    <span>{t('tps')}: {formatTPS(tokenCounts.outputTokens, log.use_time)}</span>
                                </div>
                            )}
                            {vis.cacheHitRate && cacheReadTokens > 0 && (
                                <div className="flex items-center gap-1.5">
                                    <Percent className="size-3.5 text-teal-500" />
                                    <span>{t('cacheHitRate')}: {formatCacheHitRate(cacheReadTokens, tokenCounts.totalTokens)}</span>
                                </div>
                            )}
                            <div className="flex items-center gap-1.5">
                                <ArrowDownToLine className="size-3.5 text-green-500" />
                                <span title={tokenCounts.inputEstimated ? estimatedHint : undefined}>
                                    {inputLabel}: {inputTokenDisplay}
                                </span>
                            </div>
                            <div className="flex items-center gap-1.5">
                                <ArrowUpFromLine className="size-3.5 text-purple-500" />
                                <span title={tokenCounts.outputEstimated ? estimatedHint : undefined}>
                                    {t('output')}: {outputTokenDisplay}
                                </span>
                            </div>
                            <div className="flex items-center gap-1.5">
                                <Sigma className="size-3.5 text-rose-500" />
                                <span
                                    className="font-medium text-rose-600 dark:text-rose-400"
                                    title={tokenCounts.estimated ? estimatedHint : undefined}
                                >
                                    {t('totalTokens')}: {totalTokenDisplay}
                                </span>
                            </div>
                            {cacheReadTokens > 0 && (
                                <div className="flex items-center gap-1.5">
                                    <ArrowDownToLine className="size-3.5 text-teal-500" />
                                    <span>{t('cacheHit')}: {fmt(formatCount(cacheReadTokens).formatted)}</span>
                                </div>
                            )}
                            {semanticCacheHit && (
                                <div className="flex items-center gap-1.5">
                                    <ArrowDownToLine className="size-3.5 text-cyan-500" />
                                    <span>{t('semanticCacheHit')}</span>
                                </div>
                            )}
                            {vis.cost && (
                                <div className="flex items-center gap-1.5">
                                    {chinaMode ? <JapaneseYen className="size-3.5 text-emerald-500" /> : <DollarSign className="size-3.5 text-emerald-500" />}
                                    <span className="font-medium text-emerald-600 dark:text-emerald-400">
                                        {t('cost')}: {costDisplay}
                                    </span>
                                    {log.billing_window === 'peak' && (
                                        <Badge
                                            variant="outline"
                                            className="shrink-0 text-xs px-1.5 py-0 border-amber-400/50 text-amber-500 dark:text-amber-400"
                                            title={t('billingWindowHint')}
                                        >
                                            {t('billingWindowPeak')}
                                        </Badge>
                                    )}
                                    {log.billing_window === 'offpeak' && (
                                        <Badge
                                            variant="outline"
                                            className="shrink-0 text-xs px-1.5 py-0 border-sky-400/50 text-sky-500 dark:text-sky-400"
                                            title={t('billingWindowHint')}
                                        >
                                            {t('billingWindowOffPeak')}
                                        </Badge>
                                    )}
                                </div>
                            )}
                            {vis.reasoningEffort && !!log.reasoning_effort && (
                                <div className="flex items-center gap-1.5">
                                    <Brain className="size-3.5 text-violet-500" />
                                    <span>{t('reasoningEffort')}: {log.reasoning_effort}</span>
                                </div>
                            )}
                            {vis.reasoningTokens && (log.reasoning_tokens ?? 0) > 0 && (
                                <div className="flex items-center gap-1.5">
                                    <Brain className="size-3.5 text-indigo-500" />
                                    <span>{t('reasoningTokens')}: {fmt(formatCount(log.reasoning_tokens ?? 0).formatted)}{t('reasoningTokensUnit')}</span>
                                </div>
                            )}
                            {vis.reasoningTokens && (log.reasoning_tokens ?? 0) <= 0 && (log.reasoning_chars ?? 0) > 0 && (
                                <div className="flex items-center gap-1.5">
                                    <Type className="size-3.5 text-indigo-500" />
                                    <span>{t('reasoningChars')}: {fmt(formatCount(log.reasoning_chars ?? 0).formatted)}{t('reasoningCharsUnit')}</span>
                                </div>
                            )}
                            </div>
                        </div>
                    </MorphingDialogContent>
                </MorphingDialogContainer>
            </MorphingDialog>
    );
});
