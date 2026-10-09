'use client';

import { Boxes, Clock3, RefreshCw, RadioTower, Rows3 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';
import { formatAverageLatency, type LatencyUnitMode } from './latency-format';
import type { ModelMarketSummary } from '@/api/endpoints/model';

const EMPTY_SUMMARY: ModelMarketSummary = {
    model_count: 0,
    coverage_count: 0,
    unique_channel_count: 0,
    average_latency_ms: 0,
    last_update_time: '',
};

function formatLastUpdate(value: string | undefined, fallback: string) {
    if (!value) return fallback;
    const formatted = formatDateTime(value);
    if (formatted === '-') return fallback;
    const date = new Date(value);
    if (!Number.isNaN(date.getTime()) && date.getFullYear() <= 1) return fallback;
    return formatted;
}

/**
 * 模型市场摘要内容：标题 + 刷新按钮 + 最近更新时间 + 四项指标。
 * 纯内容块，不自带 Popover，可在任意容器内复用（目前内嵌于 Toolbar 筛选弹层）。
 */
export function ModelMarketSummaryContent({
    summary = EMPTY_SUMMARY,
    requestCount,
    onRefresh,
    isRefreshing,
    latencyUnit = 'auto',
    className,
}: {
    /** 市场摘要数据；尚未拿到数据时按全零渲染。 */
    summary?: ModelMarketSummary;
    requestCount: number;
    onRefresh: () => void;
    isRefreshing: boolean;
    /** 平均延迟的展示单位，跟随工具栏选定的 modelLatencyUnit。 */
    latencyUnit?: LatencyUnitMode;
    className?: string;
}) {
    const t = useTranslations('model');
    const lastUpdateLabel = formatLastUpdate(summary.last_update_time, t('summary.neverUpdated'));

    const metrics = [
        {
            key: 'models',
            icon: Boxes,
            label: t('summary.modelCount'),
            value: summary.model_count.toLocaleString(),
        },
        {
            key: 'coverage',
            icon: Rows3,
            label: t('summary.coverage'),
            value: summary.coverage_count.toLocaleString(),
        },
        {
            key: 'unique',
            icon: RadioTower,
            label: t('summary.uniqueChannels'),
            value: summary.unique_channel_count.toLocaleString(),
        },
        {
            key: 'latency',
            icon: Clock3,
            label: t('summary.averageLatency'),
            value: formatAverageLatency(summary.average_latency_ms, requestCount, latencyUnit),
        },
    ];

    return (
        <div className={cn('grid gap-2 rounded-lg border border-border bg-muted/20 p-2.5', className)}>
            <div className="flex flex-col gap-1.5 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                    <div className="text-xs font-semibold text-foreground sm:text-sm">{t('summary.title')}</div>
                    <div className="text-[0.65rem] text-muted-foreground sm:text-[11px]">{t('summary.description')}</div>
                </div>
                <button
                    type="button"
                    onClick={onRefresh}
                    disabled={isRefreshing}
                    className="inline-flex min-h-9 items-center justify-center gap-1.5 rounded-md border border-border/30 bg-card px-3 py-2 text-xs font-medium text-foreground transition-[color,background-color,border-color] duration-150 hover:border-border hover:bg-muted active:scale-[0.98] disabled:pointer-events-none disabled:opacity-60 sm:h-9 sm:py-0"
                >
                    <RefreshCw className={cn('size-3.5', isRefreshing && 'animate-spin')} />
                    {isRefreshing ? t('summary.refreshing') : t('summary.refresh')}
                </button>
            </div>

            <div className="flex items-center gap-1.5 rounded-lg border border-border/30 bg-card px-2.5 py-1.5 text-[0.65rem] text-muted-foreground sm:gap-2 sm:px-3 sm:py-2 sm:text-[11px]">
                <Clock3 className="size-3.5 shrink-0 text-primary sm:size-4" />
                <span className="min-w-0 truncate">{t('summary.lastUpdate')}: {lastUpdateLabel}</span>
            </div>

            <div className="grid grid-cols-2 gap-1.5 sm:gap-2 lg:grid-cols-4">
                {metrics.map((metric) => (
                    <div key={metric.key} className="min-w-0 overflow-hidden rounded-lg border border-border/30 bg-card px-2 py-1.5 sm:px-2.5 sm:py-2">
                        <div className="flex min-w-0 items-center gap-1 text-[0.6rem] text-muted-foreground sm:gap-1.5 sm:text-[10px]">
                            <metric.icon className="size-3 shrink-0 text-primary sm:size-3.5" />
                            <span className="min-w-0 truncate leading-tight">{metric.label}</span>
                        </div>
                        <div className="mt-0.5 min-w-0 truncate text-[1.15rem] font-semibold leading-none tracking-tight sm:mt-1 sm:text-[1.45rem]">{metric.value}</div>
                    </div>
                ))}
            </div>
        </div>
    );
}
