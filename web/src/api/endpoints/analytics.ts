'use client';

import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../client';
import { REFETCH_INTERVAL_CONFIG } from '../constants';
import {
    type GenerateAIRouteProgress,
    type GroupTestProgress,
    useGenerateAIRouteProgress,
    useGroupList,
    useGroupTestProgress,
} from './group';
import {
    clearStoredAIRouteTask,
    clearStoredGroupTestTask,
    readStoredAIRouteTask,
    readStoredGroupTestTask,
    type StoredAIRouteTask,
    type StoredGroupTestTask,
} from '@/components/modules/group/task-storage';

export type AnalyticsRange = '1d' | '7d' | '30d' | '90d' | 'ytd' | 'all';

/** 分析中心查询结果缓存 TTL。off=禁用（每次直查 DB）。 */
export type AnalyticsCacheTtl = '10s' | '30s' | '1m' | 'off';

/** 缓存 TTL 对应的毫秒数，用于设置 TanStack Query 的 refetchInterval。off=0。 */
const CACHE_TTL_MS: Record<AnalyticsCacheTtl, number> = {
    '10s': 10_000,
    '30s': 30_000,
    '1m': 60_000,
    off: 0,
};

/** 缓存 TTL 对应的后端 query 参数值。 */
function cacheTtlParam(ttl: AnalyticsCacheTtl): Record<string, string> {
    return { cache_ttl: ttl };
}

export interface AnalyticsMetrics {
    request_count: number;
    total_tokens: number;
    input_tokens: number;
    output_tokens: number;
    total_cost: number;
    success_rate: number;
}

export interface AnalyticsOverview extends AnalyticsMetrics {
    provider_count: number;
    api_key_count: number;
    model_count: number;
    fallback_rate: number;
    enabled_provider_count: number;
    enabled_model_count: number;
    enabled_api_key_count: number;
}

export interface AnalyticsProviderBreakdownItem extends AnalyticsMetrics {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
}

export interface AnalyticsModelBreakdownItem extends AnalyticsMetrics {
    model_name: string;
}

export interface AnalyticsAPIKeyBreakdownItem extends AnalyticsMetrics {
    api_key_id?: number;
    name: string;
}

export interface AnalyticsUtilization {
    provider_breakdown: AnalyticsProviderBreakdownItem[];
    model_breakdown: AnalyticsModelBreakdownItem[];
    apikey_breakdown: AnalyticsAPIKeyBreakdownItem[];
}

export interface AnalyticsGroupHealthItem {
    group_id: number;
    group_name: string;
    endpoint_type: string;
    item_count: number;
    enabled_item_count: number;
    disabled_item_count: number;
    failure_count: number;
    last_failure_at: number;
    health_score: number;
    status: 'healthy' | 'warning' | 'degraded' | 'down' | 'empty';
    failing_channels: FailingChannelItem[];
    mode: number;
    channel_ids: number[];
    // 后端按本组 (channel_id, model_name) 精确过滤后的 Auto 策略快照（仅 Auto 组有值）。
    // 由后端 buildGroupHealth 组装，替代前端按 channel_ids 客户端过滤（issue #87 Bug 修复）。
    auto_items?: AutoStrategySnapshotItem[];
}

export interface FailingChannelItem {
    channel_id: number;
    channel_name: string;
    model_name: string;
    failure_count: number;
    last_failure_at: number;
}

export interface AnalyticsChannelModelItem extends AnalyticsMetrics {
    channel_id: number;
    channel_name: string;
    model_name: string;
    enabled: boolean;
}

export interface AutoStrategySnapshotItem {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    model_name: string;
    success_rate: number;
    sample_count: number;
    avg_latency_ms: number;
    last_active_at: number;
    min_samples_met: boolean;
}

export interface AnalyticsEvaluationRuntime {
    groupCount: number;
    hasGroups: boolean;
    aiRouteTask: StoredAIRouteTask | null;
    groupTestTask: StoredGroupTestTask | null;
    aiRouteProgress: GenerateAIRouteProgress | null;
    groupTestProgress: GroupTestProgress | null;
    aiRouteError: unknown;
    groupTestError: unknown;
    isLoading: boolean;
}

function getErrorStatusCode(error: unknown) {
    if (error && typeof error === 'object' && 'code' in error && typeof error.code === 'number') {
        return error.code;
    }
    return undefined;
}

export function useAnalyticsEvaluationRuntime(): AnalyticsEvaluationRuntime {
    const { data: groups = [], isLoading: isGroupsLoading } = useGroupList();
    const [aiRouteTask, setAiRouteTask] = useState<StoredAIRouteTask | null>(() => readStoredAIRouteTask());
    const [groupTestTask, setGroupTestTask] = useState<StoredGroupTestTask | null>(() => readStoredGroupTestTask());

    useEffect(() => {
        const syncFromStorage = () => {
            setAiRouteTask(readStoredAIRouteTask());
            setGroupTestTask(readStoredGroupTestTask());
        };

        window.addEventListener('focus', syncFromStorage);
        return () => window.removeEventListener('focus', syncFromStorage);
    }, []);

    const aiRouteProgressQuery = useGenerateAIRouteProgress(aiRouteTask?.id ?? null);
    const groupTestProgressQuery = useGroupTestProgress(groupTestTask?.id ?? null);

    useEffect(() => {
        if (!aiRouteTask?.id || getErrorStatusCode(aiRouteProgressQuery.error) !== 404) {
            return;
        }

        clearStoredAIRouteTask(aiRouteTask.id);
        queueMicrotask(() => setAiRouteTask((current) => (current?.id === aiRouteTask.id ? null : current)));
    }, [aiRouteProgressQuery.error, aiRouteTask]);

    useEffect(() => {
        if (!groupTestTask?.id || getErrorStatusCode(groupTestProgressQuery.error) !== 404) {
            return;
        }

        clearStoredGroupTestTask(groupTestTask.id);
        queueMicrotask(() => setGroupTestTask((current) => (current?.id === groupTestTask.id ? null : current)));
    }, [groupTestProgressQuery.error, groupTestTask]);

    const aiRouteError =
        aiRouteTask?.id && getErrorStatusCode(aiRouteProgressQuery.error) !== 404
            ? aiRouteProgressQuery.error
            : null;
    const groupTestError =
        groupTestTask?.id && getErrorStatusCode(groupTestProgressQuery.error) !== 404
            ? groupTestProgressQuery.error
            : null;

    return {
        groupCount: groups.length,
        hasGroups: groups.length > 0,
        aiRouteTask,
        groupTestTask,
        aiRouteProgress: aiRouteProgressQuery.data ?? null,
        groupTestProgress: groupTestProgressQuery.data ?? null,
        aiRouteError,
        groupTestError,
        isLoading: isGroupsLoading || aiRouteProgressQuery.isLoading || groupTestProgressQuery.isLoading,
    };
}

export function useAnalyticsOverview(
    range: AnalyticsRange,
    cacheTtl: AnalyticsCacheTtl = '30s',
    refetchIntervalMs?: number | false,
) {
    return useQuery({
        queryKey: ['analytics', 'overview', range, cacheTtl],
        queryFn: async () => apiClient.get<AnalyticsOverview>('/api/v1/analytics/overview', { range, ...cacheTtlParam(cacheTtl) }),
        refetchInterval: refetchIntervalMs ?? CACHE_TTL_MS[cacheTtl],
    });
}

export function useAnalyticsUtilization(range: AnalyticsRange, cacheTtl: AnalyticsCacheTtl = '30s') {
    return useQuery({
        queryKey: ['analytics', 'utilization', range, cacheTtl],
        queryFn: async () => apiClient.get<AnalyticsUtilization>('/api/v1/analytics/utilization', { range, ...cacheTtlParam(cacheTtl) }),
        refetchInterval: CACHE_TTL_MS[cacheTtl],
    });
}

export function useAnalyticsGroupHealth(cacheTtl: AnalyticsCacheTtl = '30s') {
    return useQuery({
        queryKey: ['analytics', 'group-health', cacheTtl],
        queryFn: async () => apiClient.get<AnalyticsGroupHealthItem[]>('/api/v1/analytics/group-health', cacheTtlParam(cacheTtl)),
        refetchInterval: CACHE_TTL_MS[cacheTtl],
    });
}

export interface HistogramBucket {
    label: string;
    count: number;
}

export interface LatencyDistribution {
    total_requests: number;
    avg_ms: number;
    p50_ms: number;
    p95_ms: number;
    p99_ms: number;
    ftut_avg_ms: number;
    ftut_p50_ms: number;
    ftut_p95_ms: number;
    ftut_p99_ms: number;
    buckets: HistogramBucket[];
}

export function useAnalyticsLatencyDistribution(range: AnalyticsRange, cacheTtl: AnalyticsCacheTtl = '30s') {
    return useQuery({
        queryKey: ['analytics', 'latency-distribution', range, cacheTtl],
        queryFn: async () => apiClient.get<LatencyDistribution>('/api/v1/analytics/latency-distribution', { range, ...cacheTtlParam(cacheTtl) }),
        refetchInterval: CACHE_TTL_MS[cacheTtl],
    });
}

export function useAnalyticsChannelModel(range: AnalyticsRange, groupId: number | undefined, cacheTtl: AnalyticsCacheTtl = '30s') {
    return useQuery({
        queryKey: ['analytics', 'channel-model', range, groupId ?? null, cacheTtl],
        queryFn: async () =>
            apiClient.get<AnalyticsChannelModelItem[]>('/api/v1/analytics/channel-model', {
                range,
                ...(groupId != null ? { group_id: groupId } : {}),
                ...cacheTtlParam(cacheTtl),
            }),
        refetchInterval: CACHE_TTL_MS[cacheTtl],
    });
}

export function useAnalyticsAutoStrategy(groupId?: number) {
    return useQuery({
        queryKey: ['analytics', 'auto-strategy', groupId ?? null],
        queryFn: async () =>
            apiClient.get<AutoStrategySnapshotItem[]>('/api/v1/analytics/auto-strategy', {
                ...(groupId != null ? { group_id: groupId } : {}),
            }),
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}
