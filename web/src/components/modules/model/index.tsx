'use client';

import { useEffect, useId, useState, type ReactNode } from 'react';
import { MotionConfig, AnimatePresence, motion } from 'motion/react';
import { Boxes, ChevronDown, RotateCcw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { useNavStore } from '@/components/modules/navbar';
import { useModelMarket } from '@/api/endpoints/model';
import { useSettingList, SettingKey } from '@/api/endpoints/setting';
import { useTranslations } from 'next-intl';
import { ModelItem } from './Item';
import { MobileModelItem } from './MobileModelItem';
import { EndpointsView } from './EndpointsView';
import { PriceCategoriesView } from './PriceCategoriesView';
import { useIsMobile } from '@/hooks/use-mobile';
import { useSearchStore, useToolbarViewOptionsStore } from '@/components/modules/toolbar';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { sortModelMarketItems } from './sort';
import { useModelFilters, MODEL_CAPABILITY_OPTIONS } from './useModelFilters';
import { useModelViewStore } from './view-store';
import { useNormalizeRulesSync } from './useNormalizeRulesSync';
import { cn } from '@/lib/utils';

const FILTER_COLLAPSED_STORAGE_KEY = 'model-filter-collapsed';

function readFilterCollapsed(): boolean {
    if (typeof window === 'undefined') return false;
    try {
        return window.localStorage.getItem(FILTER_COLLAPSED_STORAGE_KEY) === '1';
    } catch {
        return false;
    }
}

function persistFilterCollapsed(collapsed: boolean) {
    if (typeof window === 'undefined') return;
    try {
        window.localStorage.setItem(FILTER_COLLAPSED_STORAGE_KEY, collapsed ? '1' : '0');
    } catch {
        /* ignore quota / privacy mode errors */
    }
}

export function Model() {
    const t = useTranslations('model');
    const tFilter = useTranslations('modelFilter');
    const activeItem = useNavStore((s) => s.activeItem);
    const view = useModelViewStore((s) => s.modelView);
    const marketActive = activeItem === 'model' && view === 'market';
    const { data: market, isLoading, error, refetch } = useModelMarket(marketActive);
    const filterBodyId = useId();
    const { data: settings } = useSettingList();
    // 归一化去重依赖运行时规则（来自 DB Setting），需在筛选前注入。
    useNormalizeRulesSync();
    const isMobile = useIsMobile();
    const pageKey = 'model' as const;
    const searchTerm = useSearchStore((s) => s.getSearchTerm(pageKey));
    const layout = useToolbarViewOptionsStore((s) => s.getLayout(pageKey));
    const filter = useToolbarViewOptionsStore((s) => s.modelFilter);
    const modelSortMode = useToolbarViewOptionsStore((s) => s.modelSortMode);
    const modelLatencyUnit = useToolbarViewOptionsStore((s) => s.modelLatencyUnit);

    const { capability, provider, dedupe, setCapability, setProvider, setDedupe, initializeDedupe, resetFilters } = useModelViewStore();
    const setPricingFilter = useToolbarViewOptionsStore((s) => s.setModelFilter);
    const setSearchTerm = useSearchStore((s) => s.setSearchTerm);
    useEffect(() => {
        if (!settings) return;
        initializeDedupe(settings.find((s) => s.key === SettingKey.ModelNormalizeMarketDedupeDefault)?.value === 'true');
    }, [settings, initializeDedupe]);
    // 筛选条折叠状态（默认展开，随内容滚动上滑，可手动收起腾出空间）。
    const [filterCollapsed, setFilterCollapsed] = useState<boolean>(readFilterCollapsed);

    const sortedModels = sortModelMarketItems(market?.items ?? [], modelSortMode);
    const { visible: visibleModels, providers, capabilityQuery } = useModelFilters({
        items: sortedModels,
        searchTerm,
        capability,
        provider,
        dedupe,
        pricing: filter,
        enabled: marketActive,
    });
    const hasAnyModel = (market?.items.length ?? 0) > 0;

    const activeFilterCount = [capability !== 'all', provider !== '', dedupe, filter !== 'all', searchTerm.trim() !== ''].filter(Boolean).length;
    const clearFilters = () => {
        resetFilters();
        setPricingFilter('all');
        setSearchTerm(pageKey, '');
    };
    const waitingForCapabilities = capability !== 'all' && capabilityQuery.isLoading;
    const loadError = !market ? error : capability !== 'all' && !capabilityQuery.data ? capabilityQuery.error : null;

    const toggleFilterCollapsed = () => {
        setFilterCollapsed((prev) => {
            const next = !prev;
            persistFilterCollapsed(next);
            return next;
        });
    };

    // 筛选条作为 VirtualizedGrid 的 header，与列表共享同一个滚动容器，
    // 下滑时随卡片一起向上滚走（同 hub 总览），同时列表保持虚拟化。
    const filterHeader: ReactNode = (
        <div className="mb-3 overflow-hidden rounded-xl border border-border/50 bg-card text-card-foreground sm:mb-4">
            <div className="flex items-center gap-2 pr-3 md:pr-4">
            <button
                type="button"
                onClick={toggleFilterCollapsed}
                aria-expanded={!filterCollapsed}
                aria-controls={filterBodyId}
                aria-label={filterCollapsed ? tFilter('filterExpand') : tFilter('filterCollapse')}
                className="flex min-w-0 flex-1 flex-wrap items-center gap-2 p-3 text-left transition-colors hover:bg-muted/40 md:p-4"
            >
                <span className="text-xs font-semibold text-foreground sm:text-sm">{tFilter('filterTitle')}</span>
                <span className="text-xs tabular-nums text-muted-foreground" role="status" aria-live="polite">{tFilter('resultCount', { visible: visibleModels.length, total: sortedModels.length })}</span>
                {activeFilterCount > 0 ? (
                    <span className="rounded-full border border-primary/30 bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
                        {tFilter('filterSummary', { count: activeFilterCount })}
                    </span>
                ) : null}
                <ChevronDown
                    className={cn(
                        'ml-auto size-4 shrink-0 text-muted-foreground transition-transform duration-200',
                        filterCollapsed ? '' : 'rotate-180',
                    )}
                />
            </button>
            {activeFilterCount > 0 && <Button variant="ghost" size="sm" onClick={clearFilters} className="shrink-0 gap-1.5 text-xs"><RotateCcw className="size-3.5" /><span className="hidden sm:inline">{tFilter('reset')}</span><span className="sr-only sm:hidden">{tFilter('reset')}</span></Button>}
            </div>

            <AnimatePresence initial={false}>
                {!filterCollapsed ? (
                    <motion.div
                        key="filter-body"
                        id={filterBodyId}
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.22 }}
                        className="overflow-hidden"
                    >
                        <div className="flex flex-col gap-2 px-3 pb-3 md:px-4 md:pb-4">
                            <div className="flex flex-wrap items-center gap-1.5">
                                <span className="mr-1 text-xs font-medium text-muted-foreground">{tFilter('capabilityLabel')}</span>
                                {MODEL_CAPABILITY_OPTIONS.map((value) => {
                                    const active = capability === value;
                                    const labelKey = value === 'all' ? 'all' : value;
                                    return (
                                        <button
                                            key={value}
                                            type="button"
                                            onClick={() => setCapability(value)}
                                            aria-pressed={active}
                                            className={cn(
                                                'rounded-md border px-2.5 py-1 text-xs font-medium transition-colors',
                                                active
                                                    ? 'border-primary/40 bg-primary/10 text-primary'
                                                    : 'border-border bg-card text-muted-foreground hover:text-foreground',
                                            )}
                                        >
                                            {value === 'all' ? tFilter('all') : tFilter(`capability.${labelKey}`)}
                                        </button>
                                    );
                                })}
                            </div>

                            <div className="flex flex-wrap items-center gap-1.5">
                                <span className="mr-1 text-xs font-medium text-muted-foreground">{tFilter('provider')}</span>
                                <button
                                    type="button"
                                    onClick={() => setProvider('')}
                                    aria-pressed={provider === ''}
                                    className={cn(
                                        'rounded-md border px-2.5 py-1 text-xs font-medium transition-colors',
                                        provider === ''
                                            ? 'border-primary/40 bg-primary/10 text-primary'
                                            : 'border-border bg-card text-muted-foreground hover:text-foreground',
                                    )}
                                >
                                    {tFilter('all')}
                                </button>
                                {providers.map((value) => {
                                    const active = provider === value;
                                    return (
                                        <button
                                            key={value}
                                            type="button"
                                            onClick={() => setProvider(active ? '' : value)}
                                            aria-pressed={active}
                                            className={cn(
                                                'rounded-md border px-2.5 py-1 text-xs font-medium transition-colors',
                                                active
                                                    ? 'border-primary/40 bg-primary/10 text-primary'
                                                    : 'border-border bg-card text-muted-foreground hover:text-foreground',
                                            )}
                                        >
                                            {value}
                                        </button>
                                    );
                                })}
                            </div>

                            <div className="flex items-center gap-2">
                                <button
                                    type="button"
                                    onClick={() => setDedupe(!dedupe)}
                                    aria-pressed={dedupe}
                                    className={cn(
                                        'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs font-medium transition-colors',
                                        dedupe
                                            ? 'border-primary/40 bg-primary/10 text-primary'
                                            : 'border-border bg-card text-muted-foreground hover:text-foreground',
                                    )}
                                >
                                    {tFilter('dedupe')}
                                </button>
                                <span className="text-xs text-muted-foreground">{tFilter('dedupeHint')}</span>
                            </div>
                        </div>
                    </motion.div>
                ) : null}
            </AnimatePresence>
        </div>
    );

    return (
        <section className="relative flex h-full min-h-0 flex-col overflow-hidden rounded-t-xl" aria-label={t('marketTitle')}>
            {view === 'endpoints' ? (
                <div className="flex h-full min-h-0 flex-col overflow-y-auto overscroll-contain rounded-t-xl pb-3 md:pb-4">
                    <EndpointsView />
                </div>
            ) : view === 'categories' ? (
                <div className="flex h-full min-h-0 flex-col overflow-y-auto overscroll-contain rounded-t-xl pb-3 md:pb-4">
                    <PriceCategoriesView />
                </div>
            ) : (
                isLoading || waitingForCapabilities ? (
                    <LoadingState message={t('loading')} />
                ) : loadError ? (
                    <ErrorState message={loadError.message} onRetry={() => { void refetch(); void capabilityQuery.refetch(); }} />
                ) : visibleModels.length > 0 ? (
                    <MotionConfig transition={{ layout: { duration: 0 } }}>
                        <VirtualizedGrid
                            items={visibleModels}
                            layout={isMobile ? 'list' : layout}
                            columns={isMobile ? { default: 1 } : { default: 1, sm: 2, md: 2, lg: 3 }}
                            estimateItemHeight={isMobile || layout === 'compact' ? 88 : 300}
                            getItemKey={(model) => model.name}
                            header={filterHeader}
                            renderItem={(model) =>
                                isMobile || layout === 'compact' ? (
                                    <MobileModelItem model={model} latencyUnit={modelLatencyUnit} />
                                ) : (
                                    <ModelItem model={model} layout={layout} latencyUnit={modelLatencyUnit} />
                                )
                            }
                            bottomPaddingClassName="pb-3 md:pb-4"
                        />
                    </MotionConfig>
                ) : (
                    <div className="flex h-full min-h-0 flex-col overflow-y-auto overscroll-contain rounded-t-xl pb-3 md:pb-4">
                        {filterHeader}
                        <section className="rounded-xl border border-border/35 bg-card p-3 text-card-foreground md:p-4">
                            <div className="relative flex min-h-[18rem] items-center justify-center overflow-hidden rounded-xl border border-dashed border-border/35 bg-card py-6">
                                <div className="relative flex flex-col items-center gap-4 px-6 text-center">
                                    <div className="grid size-14 place-items-center rounded-2xl bg-muted/50"><Boxes className="size-6 text-muted-foreground" /></div>
                                    <p className="text-sm text-muted-foreground">
                                        {hasAnyModel ? t('empty') : t('emptyAll')}
                                    </p>
                                    {activeFilterCount > 0 && <Button variant="outline" size="sm" onClick={clearFilters}>{tFilter('reset')}</Button>}
                                </div>
                            </div>
                        </section>
                    </div>
                )
            )}
        </section>
    );
}
