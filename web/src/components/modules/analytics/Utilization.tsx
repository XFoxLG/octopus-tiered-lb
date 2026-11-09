'use client';

import { NonChatStats } from '@/components/common/NonChatStats';

import type { ReactNode } from 'react';
import { ArrowDownUp, KeyRound, Layers3, Radio, Waves } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { useAnalyticsUtilization, type AnalyticsRange, type AnalyticsProviderBreakdownItem, type AnalyticsModelBreakdownItem, type AnalyticsAPIKeyBreakdownItem } from '@/api/endpoints/analytics';
import { formatCount, formatMoney } from '@/lib/utils';
import { ObservatorySection, QueryState, StatusBadge, formatPercent } from './shared';
import { useAnalyticsCacheTtl } from './cache-context';
import { useBreakdownSort, type AnalyticsSortKey } from './use-sort';

type BreakdownItem = AnalyticsProviderBreakdownItem | AnalyticsModelBreakdownItem | AnalyticsAPIKeyBreakdownItem;

const SORT_OPTIONS: AnalyticsSortKey[] = ['requests', 'success_rate', 'cost'];

function BreakdownCard({
    title,
    icon: Icon,
    items,
    getName,
    getMeta,
    noBillingHint,
}: {
    title: string;
    icon: typeof Radio;
    items: BreakdownItem[];
    getName: (item: BreakdownItem) => string;
    getMeta?: (item: BreakdownItem) => ReactNode;
    noBillingHint?: string;
}) {
    const t = useTranslations('analytics');
    const { sorted, sortKey, setSortKey, sortOrder, toggleOrder } = useBreakdownSort(items);
    const allCostZero = items.length > 0 && items.every((item) => item.total_cost === 0);
    const sortLabelKeys: Record<AnalyticsSortKey, string> = {
        requests: 'sort.byRequests',
        success_rate: 'sort.bySuccessRate',
        cost: 'sort.byCost',
    };
    return (
        <article className="rounded-lg border border-border/30 bg-card p-4 shadow-sm ">
            <div className="mb-4 flex items-center justify-between gap-2">
                <div className="flex items-center gap-3">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary shadow-sm">
                        <Icon className="h-4 w-4" />
                    </div>
                    <h4 className="text-sm font-semibold">{title}</h4>
                </div>
                <div className="flex items-center gap-1">
                    <select
                        value={sortKey}
                        onChange={(e) => setSortKey(e.target.value as AnalyticsSortKey)}
                        className="h-6 rounded-md border border-border/50 bg-background px-1.5 text-[11px] outline-none focus:border-primary/30"
                    >
                        {SORT_OPTIONS.map((opt) => (
                            <option key={opt} value={opt}>
                                {t(sortLabelKeys[opt])}
                            </option>
                        ))}
                    </select>
                    <button
                        type="button"
                        onClick={toggleOrder}
                        title={sortOrder === 'desc' ? t('sort.descending') : t('sort.ascending')}
                        className="flex h-6 w-6 items-center justify-center rounded-md border border-border/50 bg-background text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                    >
                        <ArrowDownUp className={`size-3 ${sortOrder === 'asc' ? 'rotate-180' : ''}`} />
                    </button>
                </div>
            </div>

            <div className="max-h-80 space-y-3 overflow-y-auto pr-1">
                {sorted.map((item) => (
                    <div key={`${title}-${getName(item)}`} className="rounded-lg border border-border/25 bg-card px-3 py-3 shadow-sm">
                        <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0">
                                <div className="truncate text-sm font-medium">{getName(item)}</div>
                                <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                                    <span>{formatCount(item.request_count).formatted.value}{formatCount(item.request_count).formatted.unit}</span>
                                    <span>{formatPercent(item.success_rate).formatted.value}%</span>
                                    {getMeta ? getMeta(item) : null}
                                </div>
                            </div>
                            <div className="shrink-0 text-right">
                                <div className="text-sm font-semibold">
                                    {formatMoney(item.total_cost).formatted.value}
                                    <span className="ml-0.5 text-xs text-muted-foreground">{formatMoney(item.total_cost).formatted.unit}</span>
                                </div>
                                <div className="mt-1 text-xs text-muted-foreground">
                                    {formatCount(item.total_tokens).formatted.value}
                                    <span className="ml-0.5">{formatCount(item.total_tokens).formatted.unit}</span>
                                </div>
                            </div>
                        </div>
                        <NonChatStats metrics={item} />
                    </div>
                ))}
            </div>

            {allCostZero && noBillingHint ? (
                <p className="mt-3 rounded-lg border border-amber-500/15 bg-amber-500/6 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
                    {noBillingHint}
                </p>
            ) : null}
        </article>
    );
}

export function Utilization({ range }: { range: AnalyticsRange }) {
    const t = useTranslations('analytics');
    const cacheTtl = useAnalyticsCacheTtl();
    const { data, isLoading, error } = useAnalyticsUtilization(range, cacheTtl);

    const isEmpty =
        !data ||
        (
            data.provider_breakdown.length === 0 &&
            data.model_breakdown.length === 0 &&
            data.apikey_breakdown.length === 0
        );

    return (
        <ObservatorySection
            title={t('cards.utilization.title')}
            description={t('utilization.description')}
            icon={Waves}
        >
            <QueryState
                loading={isLoading}
                error={error}
                empty={isEmpty}
                emptyLabel={isLoading ? t('states.loading') : t('utilization.empty')}
            >
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                    <BreakdownCard
                        title={t('utilization.providers')}
                        icon={Radio}
                        items={data?.provider_breakdown ?? []}
                        getName={(item) => (item as AnalyticsProviderBreakdownItem).channel_name}
                        getMeta={(item) => {
                            const provider = item as AnalyticsProviderBreakdownItem;
                            return (
                                <StatusBadge
                                    label={provider.enabled ? t('utilization.enabled') : t('utilization.disabled')}
                                    tone={provider.enabled ? 'success' : 'neutral'}
                                />
                            );
                        }}
                        noBillingHint={t('utilization.noBilling')}
                    />
                    <BreakdownCard
                        title={t('utilization.models')}
                        icon={Layers3}
                        items={data?.model_breakdown ?? []}
                        getName={(item) => (item as AnalyticsModelBreakdownItem).model_name}
                        noBillingHint={t('utilization.noBilling')}
                    />
                    <BreakdownCard
                        title={t('utilization.apikeys')}
                        icon={KeyRound}
                        items={data?.apikey_breakdown ?? []}
                        getName={(item) => (item as AnalyticsAPIKeyBreakdownItem).name}
                        noBillingHint={t('utilization.noBilling')}
                    />
                </div>
            </QueryState>
        </ObservatorySection>
    );
}
