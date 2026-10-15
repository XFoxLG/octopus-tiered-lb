'use client';

import { useState } from 'react';
import { Activity, AlertTriangle, CircleOff, ShieldCheck, Radar, ChevronDown, ChevronRight, Gauge } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { useAnalyticsGroupHealth } from '@/api/endpoints/analytics';
import { ObservatorySection, QueryState, StatusBadge, formatUnixTime } from './shared';
import { useAnalyticsCacheTtl } from './cache-context';
import { useNavStore } from '@/components/modules/navbar/nav-store';
import { useNavHandoff } from '@/lib/nav-handoff';
import { cn } from '@/lib/utils';
import { formatFirstTokenMs, isSuspectFakeStreamSignature } from '@/lib/fake-stream';
import type { FailingChannelItem, AutoStrategySnapshotItem } from '@/api/endpoints/analytics';

function getStatusTone(status: 'healthy' | 'warning' | 'degraded' | 'down' | 'empty') {
    switch (status) {
        case 'healthy':
            return 'success' as const;
        case 'warning':
        case 'degraded':
            return 'warning' as const;
        case 'down':
            return 'danger' as const;
        default:
            return 'neutral' as const;
    }
}

function StatusIconFor({ status }: { status: 'healthy' | 'warning' | 'degraded' | 'down' | 'empty' }) {
    switch (status) {
        case 'healthy':
            return <ShieldCheck className="h-4 w-4" />;
        case 'down':
            return <CircleOff className="h-4 w-4" />;
        case 'warning':
        case 'degraded':
            return <AlertTriangle className="h-4 w-4" />;
        default:
            return <Activity className="h-4 w-4" />;
    }
}

// successRateTone 把一个成功率映射为色调，用于 Auto 策略表现的高亮。
function successRateTone(rate: number, minMet: boolean): 'danger' | 'warning' | 'success' | 'neutral' {
    if (!minMet) return 'neutral';
    if (rate < 50) return 'danger';
    if (rate < 90) return 'warning';
    return 'success';
}

function successRateClass(tone: 'danger' | 'warning' | 'success' | 'neutral') {
    switch (tone) {
        case 'danger':
            return 'text-destructive';
        case 'warning':
            return 'text-amber-600 dark:text-amber-400';
        case 'success':
            return 'text-accent';
        default:
            return 'text-muted-foreground';
    }
}

// formatContextLimit 把上下文上限转换为紧凑显示（32768 → 32K，110000 → 110K）。
function formatContextLimit(tokens: number): string {
    if (tokens >= 1000) return `${Math.round(tokens / 1000)}K`;
    return String(tokens);
}

function GroupHealthCard({
    item,
    autoItems,
}: {
    item: import('@/api/endpoints/analytics').AnalyticsGroupHealthItem;
    autoItems: AutoStrategySnapshotItem[];
}) {
    const t = useTranslations('analytics');
    const setActiveItem = useNavStore((s) => s.setActiveItem);
    const setPendingLogFilter = useNavHandoff((s) => s.setPendingLogFilter);
    const [showFailing, setShowFailing] = useState(false);
    const [showAuto, setShowAuto] = useState(false);

    const isAutoGroup = item.mode === 5;

    // 点击失败渠道：跳转日志并预填渠道 + 开启尝试穿透。
    const jumpToLogForChannel = (fc: FailingChannelItem) => {
        setPendingLogFilter({
            channel_id: fc.channel_id,
            include_attempts: true,
        });
        setActiveItem('log');
    };

    return (
        <article className="rounded-lg border border-border/30 bg-card p-4 shadow-sm">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary shadow-sm">
                            <StatusIconFor status={item.status} />
                        </div>
                        <div className="min-w-0">
                            <h4 className="truncate text-sm font-semibold">{item.group_name}</h4>
                            <p className="text-xs text-muted-foreground">
                                {t('routeHealth.endpointType')}: {item.endpoint_type}
                            </p>
                        </div>
                    </div>
                </div>
                <StatusBadge
                    label={t(`routeHealth.statuses.${item.status}`)}
                    tone={getStatusTone(item.status)}
                />
            </div>

            <div className="mt-4 grid grid-cols-2 gap-3">
                <div className="rounded-lg border border-border/25 bg-card p-3 shadow-sm">
                    <div className="text-xs text-muted-foreground">{t('routeHealth.healthScore')}</div>
                    <div className="mt-2 text-2xl font-semibold">{item.health_score}</div>
                </div>
                <div className="rounded-lg border border-border/25 bg-card p-3 shadow-sm">
                    <div className="text-xs text-muted-foreground">{t('routeHealth.failureCount')}</div>
                    <div className="mt-2 text-2xl font-semibold">{item.failure_count}</div>
                </div>
                <div className="rounded-lg border border-border/25 bg-card p-3 shadow-sm">
                    <div className="text-xs text-muted-foreground">{t('routeHealth.enabledItems')}</div>
                    <div className="mt-2 text-sm font-semibold">
                        {item.enabled_item_count} / {item.item_count}
                    </div>
                </div>
                <div className="rounded-lg border border-border/25 bg-card p-3 shadow-sm">
                    <div className="text-xs text-muted-foreground">{t('routeHealth.disabledItems')}</div>
                    <div className="mt-2 text-sm font-semibold">{item.disabled_item_count}</div>
                </div>
            </div>

            <div className="mt-4 text-xs text-muted-foreground">
                {t('routeHealth.lastFailure')}:
                <span className="ml-1 text-foreground">
                    {item.last_failure_at ? formatUnixTime(item.last_failure_at) : t('routeHealth.lastFailureEmpty')}
                </span>
            </div>

            {/* 失败渠道下钻：仅在组有失败渠道时展示 */}
            {item.failing_channels && item.failing_channels.length > 0 && (
                <div className="mt-3">
                    <button
                        type="button"
                        onClick={() => setShowFailing((v) => !v)}
                        className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                    >
                        {showFailing ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                        <AlertTriangle className="size-3.5 text-amber-500" />
                        {t('routeHealth.failingChannels.title')} ({item.failing_channels.length})
                    </button>
                    {showFailing && (
                        <ul className="mt-2 space-y-1">
                            {item.failing_channels.map((fc, idx) => (
                                <li
                                    key={`${fc.channel_id}-${fc.model_name}-${idx}`}
                                    className="flex items-center justify-between gap-2 rounded-md border border-border/25 bg-background/40 px-2.5 py-1.5 text-xs"
                                >
                                    <div className="min-w-0">
                                        <div className="truncate font-medium text-foreground">{fc.channel_name || `#${fc.channel_id}`}</div>
                                        <div className="truncate text-muted-foreground">{fc.model_name}</div>
                                    </div>
                                    <div className="flex shrink-0 items-center gap-2">
                                        <span className="text-destructive font-semibold">{fc.failure_count}</span>
                                        {fc.last_failure_at ? (
                                            <span className="text-muted-foreground">{formatUnixTime(fc.last_failure_at)}</span>
                                        ) : null}
                                        <button
                                            type="button"
                                            onClick={() => jumpToLogForChannel(fc)}
                                            className="rounded border border-border/40 px-1.5 py-0.5 text-[10px] text-primary hover:bg-primary/10 transition-colors"
                                            title={t('routeHealth.failingChannels.viewLogs')}
                                        >
                                            {t('routeHealth.failingChannels.viewLogs')}
                                        </button>
                                    </div>
                                </li>
                            ))}
                        </ul>
                    )}
                </div>
            )}

            {/* 渠道实时表现：v2.8.10 起对所有分组模式展示（轮询/随机组同样记录运行态
                统计），指标含成功率、样本、延迟与「可见首字」；疑似伪流式的渠道会带标记。 */}
            {autoItems.length > 0 && (
                <div className="mt-3">
                    <button
                        type="button"
                        onClick={() => setShowAuto((v) => !v)}
                        className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                    >
                        {showAuto ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                        <Gauge className="size-3.5 text-primary" />
                        {isAutoGroup ? t('routeHealth.autoStrategy.title') : t('routeHealth.autoStrategy.titleGeneric')} ({autoItems.length})
                    </button>
                    {showAuto && (
                        <ul className="mt-2 space-y-1">
                            {autoItems.map((ai, idx) => {
                                const tone = successRateTone(ai.success_rate, ai.min_samples_met);
                                const softTotal = (ai.fake_stream_count ?? 0) + (ai.empty_output_count ?? 0) + (ai.missing_terminal_count ?? 0) + (ai.truncated_count ?? 0);
                                const mismatchRate = ai.upstream_model_samples > 0 ? (ai.upstream_model_mismatch / ai.upstream_model_samples) * 100 : null;
                                return (
                                    <li
                                        key={`${ai.channel_id}-${ai.model_name}-${idx}`}
                                        className="flex items-center justify-between gap-2 rounded-md border border-border/25 bg-background/40 px-2.5 py-1.5 text-xs"
                                    >
                                        <div className="min-w-0">
                                            <div className="truncate font-medium text-foreground">
                                                {ai.channel_name || `#${ai.channel_id}`}
                                                {!ai.enabled && (
                                                    <span className="ml-1 text-muted-foreground">({t('routeHealth.autoStrategy.disabled')})</span>
                                                )}
                                                {ai.dead_channel && (
                                                    <span
                                                        className="ml-1 rounded border border-destructive/50 bg-destructive/10 px-1 py-0.5 text-[10px] text-destructive"
                                                        title={t('routeHealth.autoStrategy.deadChannelHint')}
                                                    >
                                                        {t('routeHealth.autoStrategy.deadChannelBadge')}
                                                    </span>
                                                )}
                                            </div>
                                            <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-muted-foreground">
                                                <span className="truncate">{ai.model_name}</span>
                                                {typeof ai.context_limit_tokens === 'number' && ai.context_limit_tokens > 0 && (
                                                    <span
                                                        className="shrink-0 rounded border border-border/60 bg-muted/60 px-1 py-0.5 text-[10px] text-muted-foreground"
                                                        title={t('routeHealth.autoStrategy.contextLimitHint', {
                                                            source: ai.context_limit_source === 'manual'
                                                                ? t('routeHealth.autoStrategy.contextLimitSourceManual')
                                                                : t('routeHealth.autoStrategy.contextLimitSourceLearned'),
                                                        })}
                                                    >
                                                        {t('routeHealth.autoStrategy.contextLimitBadge', { size: formatContextLimit(ai.context_limit_tokens) })}
                                                    </span>
                                                )}
                                                {softTotal > 0 && (
                                                    <span
                                                        className="shrink-0 rounded border border-amber-500/50 bg-amber-500/10 px-1 py-0.5 text-[10px] text-amber-600 dark:text-amber-400"
                                                        title={t('routeHealth.autoStrategy.softSignalsHint', {
                                                            fake: ai.fake_stream_count ?? 0,
                                                            empty: ai.empty_output_count ?? 0,
                                                            terminal: ai.missing_terminal_count ?? 0,
                                                            truncated: ai.truncated_count ?? 0,
                                                            samples: ai.soft_sample_count ?? 0,
                                                            score: (ai.soft_score ?? 1).toFixed(2),
                                                        })}
                                                    >
                                                        {t('routeHealth.autoStrategy.softSignalsBadge', { count: softTotal })}
                                                    </span>
                                                )}
                                                {mismatchRate !== null && ai.upstream_model_mismatch > 0 && (
                                                    <span
                                                        className="shrink-0 rounded border border-amber-500/50 bg-amber-500/10 px-1 py-0.5 text-[10px] text-amber-600 dark:text-amber-400"
                                                        title={t('routeHealth.autoStrategy.modelMismatchHint', {
                                                            mismatch: ai.upstream_model_mismatch,
                                                            total: ai.upstream_model_samples,
                                                        })}
                                                    >
                                                        {t('routeHealth.autoStrategy.modelMismatchBadge', { rate: mismatchRate.toFixed(0) })}
                                                    </span>
                                                )}
                                            </div>
                                            {ai.last_decision_reason && (
                                                <div
                                                    className="truncate text-[10px] text-muted-foreground"
                                                    title={`${ai.last_decision_reason}${ai.last_decision_at ? ` @ ${formatUnixTime(ai.last_decision_at)}` : ''}`}
                                                >
                                                    {t('routeHealth.autoStrategy.lastDecision')}: {ai.last_decision_reason}
                                                </div>
                                            )}
                                        </div>
                                        <div className="flex shrink-0 items-center gap-2">
                                            <span className={cn('font-semibold', successRateClass(tone))}>
                                                {ai.success_rate.toFixed(1)}%
                                            </span>
                                            <span className="text-muted-foreground">
                                                {ai.min_samples_met
                                                    ? `${ai.sample_count}${t('routeHealth.autoStrategy.samples')}`
                                                    : t('routeHealth.autoStrategy.insufficient')}
                                            </span>
                                            {ai.avg_latency_ms > 0 && (
                                                <span className="text-muted-foreground">{Math.round(ai.avg_latency_ms)}ms</span>
                                            )}
                                            {ai.ttft_samples > 0 && ai.avg_ttft_ms > 0 && (
                                                <span
                                                    className="text-muted-foreground"
                                                    title={t('routeHealth.autoStrategy.ttftHint', { count: ai.ttft_samples })}
                                                >
                                                    {t('routeHealth.autoStrategy.ttftLabel')} {formatFirstTokenMs(ai.avg_ttft_ms)}
                                                </span>
                                            )}
                                            {isSuspectFakeStreamSignature(ai.avg_ttft_ms, ai.avg_latency_ms) && (
                                                <span
                                                    className="shrink-0 rounded border border-amber-500/50 bg-amber-500/10 px-1 py-0.5 text-[10px] text-amber-600 dark:text-amber-400"
                                                    title={t('routeHealth.autoStrategy.fakeStreamHint')}
                                                >
                                                    {t('routeHealth.autoStrategy.fakeStreamBadge')}
                                                </span>
                                            )}
                                        </div>
                                    </li>
                                );
                            })}
                        </ul>
                    )}
                </div>
            )}
        </article>
    );
}

export function GroupHealth() {
    const t = useTranslations('analytics');
    const cacheTtl = useAnalyticsCacheTtl();
    const { data, isLoading, error } = useAnalyticsGroupHealth(cacheTtl);

    // Auto 策略实时表现现由后端在 AnalyticsGroupHealthGet 内按本组 (channel_id, model_name)
    // 精确过滤后填入 item.auto_items，前端不再单独请求 /analytics/auto-strategy，也不再在
    // 客户端按 channel_ids 过滤——后者会把跨组渠道的他组模型泄漏进来（issue #87 Bug 修复）。

    return (
        <ObservatorySection
            title={t('cards.routeHealth.title')}
            description={t('routeHealth.description')}
            icon={Radar}
        >
            <QueryState
                loading={isLoading}
                error={error}
                empty={!data || data.length === 0}
                emptyLabel={isLoading ? t('states.loading') : t('routeHealth.empty')}
            >
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                    {(data ?? []).map((item) => (
                        <GroupHealthCard
                            key={`${item.group_id}-${item.endpoint_type}`}
                            item={item}
                            autoItems={item.auto_items ?? []}
                        />
                    ))}
                </div>
            </QueryState>
        </ObservatorySection>
    );
}
