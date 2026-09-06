'use client';

import { useEffect, useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { PageWrapper } from '@/components/common/PageWrapper';
import { Tabs, TabsContents, TabsContent, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    type AnalyticsRange,
    type AnalyticsCacheTtl,
    useAnalyticsOverview,
    useAnalyticsEvaluationSummary,
    useAnalyticsUtilization,
    useAnalyticsLatencyDistribution,
    useAnalyticsGroupHealth,
} from '@/api/endpoints/analytics';
import { Utilization } from './Utilization';
import { GroupHealth } from './GroupHealth';
import { ChannelModel } from './ChannelModel';
import { Evaluation } from './Evaluation';
import { LatencyDistribution } from './LatencyDistribution';
import { ShareSnapshot, type SnapshotSection } from './ShareSnapshot';
import { formatCount, formatMoney } from '@/lib/utils';
import { formatPercent } from './shared';
import { AnalyticsCacheTtlProvider } from './cache-context';

import { useSubTabStore, type AnalyticsTab } from '@/components/modules/navbar/sub-tab-store';

/** 各子标签的标签文案翻译键。 */
const TAB_LABEL_KEY: Record<AnalyticsTab, string> = {
    utilization: 'cards.utilization.title',
    'route-health': 'cards.routeHealth.title',
    'channel-model': 'cards.channelModel.title',
    evaluation: 'evaluation.title',
    latency: 'latency.title',
};

/** 常用时间范围按钮（1d/7d/30d）。 */
const PRIMARY_RANGES: AnalyticsRange[] = ['1d', '7d', '30d'];
/** 折叠进"更多"下拉的时间范围。 */
const MORE_RANGES: AnalyticsRange[] = ['90d', 'ytd', 'all'];
/** 缓存 TTL 可选项。 */
const CACHE_TTL_OPTIONS: AnalyticsCacheTtl[] = ['10s', '30s', '1m', 'off'];

export function Analytics() {
    const t = useTranslations('analytics');
    const { orderedTabs, visibleTabs } = useSubTabStore((s) => s.analytics);
    // 默认显示显示顺序中的第一个子标签（用户可在外观设置中拖拽排序/隐藏）
    const [activeTab, setActiveTab] = useState<AnalyticsTab>(() => (visibleTabs[0] as AnalyticsTab) ?? 'channel-model');

    useEffect(() => {
        if (visibleTabs.length > 0 && !visibleTabs.includes(activeTab)) {
            setActiveTab(visibleTabs[0] as AnalyticsTab);
        }
    }, [visibleTabs, activeTab]);

    const visibleSet = new Set(visibleTabs);
    const orderedVisible = orderedTabs.filter((tab) => visibleSet.has(tab));
    const [range, setRange] = useState<AnalyticsRange>('7d');
    const [cacheTtl, setCacheTtl] = useState<AnalyticsCacheTtl>('30s');
    const { data: overview } = useAnalyticsOverview(range, cacheTtl);
    const { data: evaluationData } = useAnalyticsEvaluationSummary();
    const { data: utilizationData } = useAnalyticsUtilization(range, cacheTtl);
    const { data: latencyData } = useAnalyticsLatencyDistribution(range, cacheTtl);
    const { data: groupHealthData } = useAnalyticsGroupHealth(cacheTtl);

    const isMoreRange = MORE_RANGES.includes(range);

    const snapshotSections = useMemo<SnapshotSection[]>(() => {
        const sections: SnapshotSection[] = [];

        // Overview metrics (default selected)
        if (overview) {
            const fmt = formatCount(overview.request_count).formatted;
            const tokens = formatCount(overview.total_tokens).formatted;
            const cost = formatMoney(overview.total_cost).formatted;
            sections.push({
                id: 'overview',
                label: t('share.section.overview'),
                type: 'metrics',
                defaultSelected: true,
                items: [
                    { id: 'requests', label: t('metrics.requestCount'), value: fmt.value + fmt.unit },
                    { id: 'totalTokens', label: t('metrics.totalTokens'), value: tokens.value + tokens.unit },
                    { id: 'totalCost', label: t('metrics.totalCost'), value: cost.value + cost.unit },
                    { id: 'successRate', label: t('metrics.successRate'), value: `${formatPercent(overview.success_rate).formatted.value}%` },
                    { id: 'fallbackRate', label: t('metrics.fallbackRate'), value: `${formatPercent(overview.fallback_rate).formatted.value}%` },
                    { id: 'providerCount', label: t('metrics.providerCount'), value: `${overview.provider_count}` },
                    { id: 'apiKeyCount', label: t('metrics.apiKeyCount'), value: `${overview.api_key_count}` },
                    { id: 'modelCount', label: t('metrics.modelCount'), value: `${overview.model_count}` },
                ],
            });
        }

        // Latency metrics
        if (latencyData) {
            sections.push({
                id: 'latency',
                label: t('share.section.latency'),
                type: 'metrics',
                defaultSelected: false,
                items: [
                    { id: 'latAvg', label: `${t('latency.useTime')} ${t('latency.avg')}`, value: `${latencyData.avg_ms}ms` },
                    { id: 'latP50', label: `${t('latency.useTime')} P50`, value: `${latencyData.p50_ms}ms` },
                    { id: 'latP95', label: `${t('latency.useTime')} P95`, value: `${latencyData.p95_ms}ms` },
                    { id: 'latP99', label: `${t('latency.useTime')} P99`, value: `${latencyData.p99_ms}ms` },
                    { id: 'ftutAvg', label: `${t('latency.ftut')} ${t('latency.avg')}`, value: `${latencyData.ftut_avg_ms}ms` },
                    { id: 'ftutP50', label: `${t('latency.ftut')} P50`, value: `${latencyData.ftut_p50_ms}ms` },
                    { id: 'ftutP95', label: `${t('latency.ftut')} P95`, value: `${latencyData.ftut_p95_ms}ms` },
                    { id: 'ftutP99', label: `${t('latency.ftut')} P99`, value: `${latencyData.ftut_p99_ms}ms` },
                ],
            });
        }

        // Semantic cache metrics
        if (evaluationData?.semantic_cache.enabled) {
            const sc = evaluationData.semantic_cache;
            sections.push({
                id: 'cache',
                label: t('share.section.cache'),
                type: 'metrics',
                defaultSelected: true,
                items: [
                    { id: 'cacheHitRate', label: t('cache.metrics.hitRate'), value: `${formatPercent(sc.hit_rate).formatted.value}%` },
                    { id: 'cacheEntries', label: t('cache.metrics.entries'), value: `${sc.current_entries}` },
                    { id: 'cacheHits', label: t('share.metric.cacheHits'), value: `${sc.hits}` },
                    { id: 'cacheMisses', label: t('share.metric.cacheMisses'), value: `${sc.misses}` },
                ],
            });
        }

        // Top providers by requests
        if (utilizationData && utilizationData.provider_breakdown.length > 0) {
            const top = [...utilizationData.provider_breakdown]
                .sort((a, b) => b.request_count - a.request_count)
                .slice(0, 5);
            sections.push({
                id: 'topProviders',
                label: t('share.section.topProviders'),
                type: 'list',
                defaultSelected: false,
                rows: top.map((p) => ({
                    label: p.channel_name,
                    value: formatCount(p.request_count).formatted.value + formatCount(p.request_count).formatted.unit,
                    meta: formatMoney(p.total_cost).formatted.value + formatMoney(p.total_cost).formatted.unit,
                })),
            });
        }

        // Top models by requests
        if (utilizationData && utilizationData.model_breakdown.length > 0) {
            const top = [...utilizationData.model_breakdown]
                .sort((a, b) => b.request_count - a.request_count)
                .slice(0, 5);
            sections.push({
                id: 'topModels',
                label: t('share.section.topModels'),
                type: 'list',
                defaultSelected: false,
                rows: top.map((m) => ({
                    label: m.model_name,
                    value: formatCount(m.request_count).formatted.value + formatCount(m.request_count).formatted.unit,
                    meta: formatMoney(m.total_cost).formatted.value + formatMoney(m.total_cost).formatted.unit,
                })),
            });
        }

        // Top API keys by requests
        if (utilizationData && utilizationData.apikey_breakdown.length > 0) {
            const top = [...utilizationData.apikey_breakdown]
                .sort((a, b) => b.request_count - a.request_count)
                .slice(0, 5);
            sections.push({
                id: 'topApiKeys',
                label: t('share.section.topApiKeys'),
                type: 'list',
                defaultSelected: false,
                rows: top.map((k) => ({
                    label: k.name,
                    value: formatCount(k.request_count).formatted.value + formatCount(k.request_count).formatted.unit,
                    meta: formatMoney(k.total_cost).formatted.value + formatMoney(k.total_cost).formatted.unit,
                })),
            });
        }

        // Route health summary
        if (groupHealthData && groupHealthData.length > 0) {
            const healthy = groupHealthData.filter((g) => g.status === 'healthy').length;
            const warning = groupHealthData.filter((g) => g.status === 'warning').length;
            const degraded = groupHealthData.filter((g) => g.status === 'degraded').length;
            const down = groupHealthData.filter((g) => g.status === 'down').length;
            sections.push({
                id: 'routeHealth',
                label: t('share.section.routeHealth'),
                type: 'metrics',
                defaultSelected: false,
                items: [
                    { id: 'rhTotal', label: t('share.metric.totalGroups'), value: `${groupHealthData.length}` },
                    { id: 'rhHealthy', label: t('routeHealth.statuses.healthy'), value: `${healthy}` },
                    { id: 'rhWarning', label: t('routeHealth.statuses.warning'), value: `${warning}` },
                    { id: 'rhDegraded', label: t('routeHealth.statuses.degraded'), value: `${degraded}` },
                    { id: 'rhDown', label: t('routeHealth.statuses.down'), value: `${down}` },
                ],
            });
        }

        return sections;
    }, [overview, latencyData, evaluationData, utilizationData, groupHealthData, t]);

    return (
        <PageWrapper className="h-full min-h-0 overflow-y-auto overscroll-contain space-y-6 rounded-t-xl pb-3 md:pb-4">
            <AnalyticsCacheTtlProvider value={cacheTtl}>
                <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as AnalyticsTab)}>
                    <section className="relative overflow-hidden rounded-xl border border-border/35 bg-card p-4 text-card-foreground md:p-5">
                        <div className="relative flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
                            <div className="-mx-1 overflow-x-auto overscroll-x-contain scroll-smooth px-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                                <TabsList className="flex w-max min-w-max flex-nowrap rounded-lg border-border/30 bg-card p-1 xl:min-w-0 xl:flex-wrap">
                                    {orderedVisible.map((tab) => (
                                        <TabsTrigger key={tab} value={tab}>
                                            {t(TAB_LABEL_KEY[tab as AnalyticsTab])}
                                        </TabsTrigger>
                                    ))}
                                </TabsList>
                            </div>
                            <div className="flex flex-wrap items-center gap-2">
                                {/* 时间范围：常用按钮 + "更多"下拉 */}
                                <Tabs value={range} onValueChange={(value) => setRange(value as AnalyticsRange)}>
                                    <div className="-mx-1 overflow-x-auto overscroll-x-contain scroll-smooth px-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                                        <TabsList className="flex w-max min-w-max flex-nowrap rounded-lg border-border/30 bg-card p-1 xl:min-w-0 xl:flex-wrap">
                                            {PRIMARY_RANGES.map((option) => (
                                                <TabsTrigger key={option} value={option}>
                                                    {t(`range.${option}`)}
                                                </TabsTrigger>
                                            ))}
                                        </TabsList>
                                    </div>
                                </Tabs>
                                <Select value={isMoreRange ? range : '__primary__'} onValueChange={(value) => setRange(value as AnalyticsRange)}>
                                    <SelectTrigger size="sm" className="h-8 w-auto gap-1 rounded-lg border-border/30 bg-card">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="__primary__">{t('range.more')}</SelectItem>
                                        {MORE_RANGES.map((option) => (
                                            <SelectItem key={option} value={option}>
                                                {t(`range.${option}`)}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>

                                {/* 缓存 TTL 切换器 */}
                                <Select value={cacheTtl} onValueChange={(value) => setCacheTtl(value as AnalyticsCacheTtl)}>
                                    <SelectTrigger size="sm" className="h-8 w-auto gap-1 rounded-lg border-border/30 bg-card">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {CACHE_TTL_OPTIONS.map((option) => (
                                            <SelectItem key={option} value={option}>
                                                {t(`cacheTtl.${option}`)}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>

                                <ShareSnapshot
                                    data={{
                                        title: t('title'),
                                        subtitle: t('subtitle'),
                                        timestamp: new Date().toLocaleString(),
                                        sections: snapshotSections,
                                    }}
                                />
                            </div>
                        </div>
                    </section>

                    <TabsContents>
                        <TabsContent value="utilization">
                            <Utilization range={range} />
                        </TabsContent>
                        <TabsContent value="route-health">
                            <GroupHealth />
                        </TabsContent>
                        <TabsContent value="channel-model">
                            <ChannelModel range={range} />
                        </TabsContent>
                        <TabsContent value="evaluation">
                            <Evaluation />
                        </TabsContent>
                        <TabsContent value="latency">
                            <LatencyDistribution range={range} />
                        </TabsContent>
                    </TabsContents>
                </Tabs>
            </AnalyticsCacheTtlProvider>
        </PageWrapper>
    );
}
