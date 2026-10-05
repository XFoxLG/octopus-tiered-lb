'use client';

// 模型卡片「展开详情」三段（价格 / 运行指标 / 渠道）与渠道标签行的共享实现，
// 桌面 ModelItem 与移动 MobileModelItem 仅以 density 控制排版密度，逻辑只此一份。

import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { ModelMarketItem } from '@/api/endpoints/model';
import { cn } from '@/lib/utils';
import { formatAverageLatency, type LatencyUnitMode } from './latency-format';
import {
    formatPricePair,
    formatSuccessRate,
    isPeakBillingSchedule,
    summarizeChannelTags,
    totalRequests,
} from './pricing';
import { useSettingStore } from '@/stores/setting';

export type DetailDensity = 'desktop' | 'mobile';

const SECTION_CLASS: Record<DetailDensity, string> = {
    desktop: 'rounded-lg border border-border/25 bg-card p-3 sm:p-4',
    mobile: 'rounded-lg border border-border/25 bg-card p-2.5',
};

const SECTION_TITLE_CLASS: Record<DetailDensity, string> = {
    desktop: 'text-sm font-medium text-foreground',
    mobile: 'text-xs font-medium text-foreground',
};

const PRICE_ROW_CLASS: Record<DetailDensity, string> = {
    desktop: 'flex items-center justify-between gap-2 rounded-lg bg-card px-2.5 py-2 sm:gap-3 sm:px-3',
    mobile: 'flex items-center justify-between rounded-md bg-card px-2 py-1.5',
};

const PRICE_LABEL_CLASS: Record<DetailDensity, string> = {
    desktop: 'inline-flex shrink-0 items-center gap-1.5',
    mobile: 'inline-flex shrink-0 items-center gap-1',
};

const PRICE_VALUE_CLASS: Record<DetailDensity, string> = {
    desktop: 'min-w-0 truncate text-right tabular-nums text-foreground',
    mobile: 'min-w-0 truncate text-right tabular-nums text-foreground',
};

const PRICE_ICON_CLASS: Record<DetailDensity, string> = {
    desktop: 'size-3.5',
    mobile: 'size-3',
};

const RUNTIME_GRID_CLASS: Record<DetailDensity, string> = {
    desktop: 'mt-3 grid grid-cols-2 gap-2 text-sm text-muted-foreground',
    mobile: 'mt-2 grid grid-cols-2 gap-1.5 text-xs text-muted-foreground',
};

const RUNTIME_CELL_CLASS: Record<DetailDensity, string> = {
    desktop: 'rounded-lg bg-card px-2.5 py-2 sm:px-3',
    mobile: 'rounded-md bg-card px-2 py-1.5',
};

const RUNTIME_VALUE_CLASS: Record<DetailDensity, string> = {
    desktop: 'mt-1 tabular-nums text-sm font-medium text-foreground sm:text-base',
    mobile: 'mt-0.5 tabular-nums text-sm font-medium text-foreground',
};

const CHANNEL_ROW_CLASS: Record<DetailDensity, string> = {
    desktop:
        'flex flex-col gap-2 rounded-lg bg-card px-3 py-2.5 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between sm:gap-3 sm:py-3',
    mobile: 'flex items-center justify-between gap-2 rounded-md bg-card px-2 py-1.5',
};

const CHANNEL_ENABLED_PILL_CLASS: Record<DetailDensity, string> = {
    desktop: 'rounded-full border px-2 py-0.5 sm:px-2.5 sm:py-1',
    mobile: 'rounded-full border px-1.5 py-px text-[0.6rem]',
};

const KEY_COUNT_PILL_CLASS: Record<DetailDensity, string> = {
    desktop: 'rounded-full border border-border/25 bg-card px-2 py-0.5 text-muted-foreground sm:px-2.5 sm:py-1',
    mobile: 'rounded-full border border-border/25 bg-card px-1.5 py-px text-[0.6rem] text-muted-foreground',
};

const TAGS_ROW_CLASS: Record<DetailDensity, string> = {
    desktop: 'flex flex-wrap gap-1.5 sm:gap-2',
    mobile: 'flex flex-wrap gap-1',
};

const TAG_PILL_CLASS: Record<DetailDensity, string> = {
    desktop: 'rounded-full border border-border/25 bg-card px-2 py-0.5 text-[0.68rem] text-muted-foreground sm:px-2.5 sm:py-1 sm:text-xs',
    mobile: 'rounded-full border border-border/25 bg-card px-1.5 py-px text-[0.6rem] text-muted-foreground',
};

type PricingSectionProps = {
    model: ModelMarketItem;
    brandColor: string;
    density: DetailDensity;
};

function PricingSection({ model, brandColor, density }: PricingSectionProps) {
    const t = useTranslations('model');
    const { chinaMode, exchangeRate } = useSettingStore();
    const rowsClassName = density === 'desktop' ? 'mt-3 space-y-2 text-sm text-muted-foreground' : 'mt-2 space-y-1.5 text-xs text-muted-foreground';

    return (
        <div className={SECTION_CLASS[density]}>
            <h4 className={SECTION_TITLE_CLASS[density]}>{t('detail.pricing')}</h4>
            <div className={rowsClassName}>
                <div className={PRICE_ROW_CLASS[density]}>
                    <span className={PRICE_LABEL_CLASS[density]}>
                        <ArrowDownToLine className={PRICE_ICON_CLASS[density]} style={{ color: brandColor }} />
                        {t('card.inputCache')}
                    </span>
                    <span className={PRICE_VALUE_CLASS[density]}>
                        {formatPricePair(model.input, model.cache_read, chinaMode, exchangeRate)}
                    </span>
                </div>
                <div className={PRICE_ROW_CLASS[density]}>
                    <span className={PRICE_LABEL_CLASS[density]}>
                        <ArrowUpFromLine className={PRICE_ICON_CLASS[density]} style={{ color: brandColor }} />
                        {t('card.outputCache')}
                    </span>
                    <span className={PRICE_VALUE_CLASS[density]}>
                        {formatPricePair(model.output, model.cache_write, chinaMode, exchangeRate)}
                    </span>
                </div>
                {isPeakBillingSchedule(model.billing_schedule) && (
                    <p className="text-[0.62rem] leading-relaxed text-amber-600 dark:text-amber-400 sm:text-[0.68rem]">
                        {t('overlay.peakBillingHint')}
                    </p>
                )}
            </div>
        </div>
    );
}

type RuntimeSectionProps = {
    model: ModelMarketItem;
    latencyUnit: LatencyUnitMode;
    density: DetailDensity;
};

function RuntimeSection({ model, latencyUnit, density }: RuntimeSectionProps) {
    const t = useTranslations('model');
    const requestCount = totalRequests(model.request_success, model.request_failed);
    const latencyLabel = formatAverageLatency(model.average_latency_ms, requestCount, latencyUnit);
    const successRateLabel = formatSuccessRate(model.success_rate, requestCount);

    const cells = [
        { label: t('detail.requestSuccess'), value: model.request_success.toLocaleString() },
        { label: t('detail.requestFailed'), value: model.request_failed.toLocaleString() },
        { label: t('card.averageLatency'), value: latencyLabel },
        { label: t('card.successRate'), value: successRateLabel },
    ];

    return (
        <div className={SECTION_CLASS[density]}>
            <h4 className={SECTION_TITLE_CLASS[density]}>{t('detail.runtime')}</h4>
            <div className={RUNTIME_GRID_CLASS[density]}>
                {cells.map((cell) => (
                    <div key={cell.label} className={RUNTIME_CELL_CLASS[density]}>
                        <div>{cell.label}</div>
                        <div className={RUNTIME_VALUE_CLASS[density]}>{cell.value}</div>
                    </div>
                ))}
            </div>
        </div>
    );
}

type ChannelsSectionProps = {
    model: ModelMarketItem;
    density: DetailDensity;
};

function ChannelsSection({ model, density }: ChannelsSectionProps) {
    const t = useTranslations('model');
    const isDesktop = density === 'desktop';

    return (
        <div className={SECTION_CLASS[density]}>
            <div className={isDesktop ? 'flex flex-wrap items-center justify-between gap-2 sm:gap-3' : 'flex items-center justify-between'}>
                <h4 className={SECTION_TITLE_CLASS[density]}>{t('detail.channels')}</h4>
                <span className={isDesktop ? 'text-xs text-muted-foreground' : 'text-[0.65rem] text-muted-foreground'}>
                    {model.channels.length} {t('card.channels')}
                </span>
            </div>
            <div className={isDesktop ? 'mt-3 grid gap-2' : 'mt-2 space-y-1.5'}>
                {model.channels.length === 0 ? (
                    <div className={isDesktop
                        ? 'rounded-lg bg-card px-3 py-2 text-sm text-muted-foreground'
                        : 'rounded-md bg-card px-2 py-1.5 text-xs text-muted-foreground'}
                    >
                        {t('detail.noChannels')}
                    </div>
                ) : (
                    model.channels.map((channel) => (
                        <div
                            key={channel.channel_id}
                            className={CHANNEL_ROW_CLASS[density]}
                        >
                            <div className="min-w-0">
                                <div className={cn('truncate font-medium text-foreground', isDesktop ? 'text-sm' : 'text-xs')}>
                                    {channel.channel_name}
                                </div>
                                <div className={isDesktop ? 'mt-0.5 text-xs text-muted-foreground' : 'text-[0.65rem] text-muted-foreground'}>
                                    ID {channel.channel_id}
                                </div>
                            </div>
                            <div className={isDesktop ? 'flex flex-wrap items-center gap-1.5 text-xs sm:gap-2' : 'flex shrink-0 items-center gap-1.5'}>
                                <span
                                    className={cn(
                                        CHANNEL_ENABLED_PILL_CLASS[density],
                                        channel.enabled
                                            ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-700'
                                            : 'border-border/25 bg-card text-muted-foreground',
                                    )}
                                >
                                    {channel.enabled ? t('detail.enabled') : t('detail.disabled')}
                                </span>
                                {/* 移动端原先写死 "3k" 会被读作三千，统一为「N 个 Key」标签 */}
                                <span className={KEY_COUNT_PILL_CLASS[density]}>
                                    {channel.enabled_key_count} {t('detail.keyCount')}
                                </span>
                            </div>
                        </div>
                    ))
                )}
            </div>
        </div>
    );
}

export function ChannelTagsRow({
    model,
    maxVisible,
    density,
    keyPrefix,
}: {
    model: ModelMarketItem;
    maxVisible: number;
    density: DetailDensity;
    keyPrefix?: string;
}) {
    const { visible, hiddenCount } = summarizeChannelTags(model.channels, maxVisible);

    return (
        <div className={TAGS_ROW_CLASS[density]}>
            {visible.map((channel) => (
                <span
                    key={`${keyPrefix ?? 'tag'}-${channel.channel_id}`}
                    className={TAG_PILL_CLASS[density]}
                >
                    {channel.channel_name}
                </span>
            ))}
            {hiddenCount > 0 ? (
                <span className={TAG_PILL_CLASS[density]}>+{hiddenCount}</span>
            ) : null}
        </div>
    );
}

/**
 * 展开态详情主体：桌面为「价格+运行（2/3 列）→ 渠道」，移动为纵向堆叠。
 * 外层容器的分隔线与内边距由调用方决定（两种卡片各不相同）。
 */
export function ModelItemDetail({
    model,
    brandColor,
    latencyUnit,
    density,
    wideColumns = false,
}: {
    model: ModelMarketItem;
    brandColor: string;
    latencyUnit: LatencyUnitMode;
    density: DetailDensity;
    /** 桌面 list 布局时价格 / 运行两卡并排为三列（xl），默认两列。 */
    wideColumns?: boolean;
}) {
    if (density === 'desktop') {
        return (
            <div className="space-y-4">
                <div className={cn('grid gap-3', wideColumns ? 'xl:grid-cols-3' : 'grid-cols-1 md:grid-cols-2')}>
                    <PricingSection model={model} brandColor={brandColor} density="desktop" />
                    <RuntimeSection model={model} latencyUnit={latencyUnit} density="desktop" />
                </div>
                <ChannelsSection model={model} density="desktop" />
            </div>
        );
    }

    return (
        <div className="space-y-3">
            <PricingSection model={model} brandColor={brandColor} density="mobile" />
            <RuntimeSection model={model} latencyUnit={latencyUnit} density="mobile" />
            <ChannelsSection model={model} density="mobile" />
        </div>
    );
}
