import { totalAccountedTokens, type NonChatMetrics } from '../nonchat';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../client';
import { REFETCH_INTERVAL_CONFIG, REFETCH_INTERVAL_SLOW } from '../constants';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';

/**
 * 统计数据
 */
interface StatsMetrics extends NonChatMetrics {
    input_token: number;
    output_token: number;
    input_cost: number;
    output_cost: number;
    wait_time: number;
    request_success: number;
    request_failed: number;

    // Latency percentiles (ms)
    latency_p50: number;
    latency_p95: number;
    latency_p99: number;

    // Time to first token (ms)
    ftut_avg: number;
    ftut_p50: number;
    ftut_p95: number;
    ftut_p99: number;

    // Latency histogram (request counts)
    histogram_lt_100: number;
    histogram_100_500: number;
    histogram_500_1k: number;
    histogram_1k_5k: number;
    histogram_gt_5k: number;
}

export interface StatsMetricsFormatted {
    non_chat?: NonChatMetrics;
    input_token: ReturnType<typeof formatCount>;
    output_token: ReturnType<typeof formatCount>;
    input_cost: ReturnType<typeof formatMoney>;
    output_cost: ReturnType<typeof formatMoney>;
    wait_time: ReturnType<typeof formatTime>;
    request_success: ReturnType<typeof formatCount>;
    request_failed: ReturnType<typeof formatCount>;

    // Latency percentiles (formatted as time)
    latency_p50: ReturnType<typeof formatTime>;
    latency_p95: ReturnType<typeof formatTime>;
    latency_p99: ReturnType<typeof formatTime>;

    // Time to first token (formatted as time)
    ftut_avg: ReturnType<typeof formatTime>;
    ftut_p50: ReturnType<typeof formatTime>;
    ftut_p95: ReturnType<typeof formatTime>;
    ftut_p99: ReturnType<typeof formatTime>;

    // Latency histogram (formatted as count)
    histogram_lt_100: ReturnType<typeof formatCount>;
    histogram_100_500: ReturnType<typeof formatCount>;
    histogram_500_1k: ReturnType<typeof formatCount>;
    histogram_1k_5k: ReturnType<typeof formatCount>;
    histogram_gt_5k: ReturnType<typeof formatCount>;

    request_count: ReturnType<typeof formatCount>;
    total_token: ReturnType<typeof formatCount>;
    total_cost: ReturnType<typeof formatMoney>;
}

export interface StatsChannel extends StatsMetrics {
    channel_id: number;
}

export interface StatsChannelItem extends StatsMetrics {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
}

export interface StatsChannelItemFormatted extends StatsMetricsFormatted {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
}

export interface StatsDaily extends StatsMetrics {
    date: string;
}
export interface StatsDailyFormatted extends StatsMetricsFormatted {
    date: string;
}

export interface StatsTotal extends StatsMetrics {
    id: number;
}
export type StatsTotalFormatted = StatsMetricsFormatted;

export interface StatsHourly extends StatsMetrics {
    hour: number;
    date: string;
}
export interface StatsHourlyFormatted extends StatsMetricsFormatted {
    hour: number;
    date: string;
}
/**
 * API Key 统计数据
 */
export interface StatsAPIKey extends StatsMetrics {
    api_key_id: number;
    name?: string;
}

export interface StatsAPIKeyFormatted extends StatsMetricsFormatted {
    api_key_id: number;
    name?: string;
}
/**
 * 获取今日统计数据 Hook
 */
export function useStatsToday(options?: { refetchIntervalMs?: number | false }) {
    const { refetchIntervalMs = REFETCH_INTERVAL_CONFIG } = options ?? {};
    return useQuery({
        queryKey: ['stats', 'today'],
        queryFn: async () => {
            return apiClient.get<StatsDaily>('/api/v1/stats/today');
        },
        refetchInterval: refetchIntervalMs,
    });
}

/**
 * 获取每日统计数据 Hook
 */
export function useStatsDaily(options?: { refetchIntervalMs?: number | false }) {
    const { refetchIntervalMs = REFETCH_INTERVAL_SLOW } = options ?? {};
    return useQuery({
        queryKey: ['stats', 'daily'],
        queryFn: async () => {
            return apiClient.get<StatsDaily[]>('/api/v1/stats/daily');
        },
        select: (data) => data.map((item): StatsDailyFormatted => ({
            non_chat: item,
            input_token: formatCount(item.input_token),
            output_token: formatCount(item.output_token),
            total_token: formatCount(totalAccountedTokens(item)),
            input_cost: formatMoney(item.input_cost),
            output_cost: formatMoney(item.output_cost),
            total_cost: formatMoney(item.input_cost + item.output_cost),
            wait_time: formatTime(item.wait_time),
            request_success: formatCount(item.request_success),
            request_failed: formatCount(item.request_failed),
            latency_p50: formatTime(item.latency_p50),
            latency_p95: formatTime(item.latency_p95),
            latency_p99: formatTime(item.latency_p99),
            ftut_avg: formatTime(item.ftut_avg),
            ftut_p50: formatTime(item.ftut_p50),
            ftut_p95: formatTime(item.ftut_p95),
            ftut_p99: formatTime(item.ftut_p99),
            histogram_lt_100: formatCount(item.histogram_lt_100),
            histogram_100_500: formatCount(item.histogram_100_500),
            histogram_500_1k: formatCount(item.histogram_500_1k),
            histogram_1k_5k: formatCount(item.histogram_1k_5k),
            histogram_gt_5k: formatCount(item.histogram_gt_5k),
            request_count: formatCount(item.request_success + item.request_failed),
            date: item.date,
        })),
        refetchInterval: refetchIntervalMs,
    });
}
/**
 * 获取总统计数据 Hook
 */
export function useStatsHourly(options?: { refetchIntervalMs?: number | false }) {
    const { refetchIntervalMs = REFETCH_INTERVAL_CONFIG } = options ?? {};
    return useQuery({
        queryKey: ['stats', 'hourly'],
        queryFn: async () => {
            return apiClient.get<StatsHourly[]>('/api/v1/stats/hourly');
        },
        select: (data) => data.map((item): StatsHourlyFormatted => ({
            hour: item.hour,
            date: item.date,
            non_chat: item,
            input_token: formatCount(item.input_token),
            output_token: formatCount(item.output_token),
            total_token: formatCount(totalAccountedTokens(item)),
            input_cost: formatMoney(item.input_cost),
            output_cost: formatMoney(item.output_cost),
            total_cost: formatMoney(item.input_cost + item.output_cost),
            wait_time: formatTime(item.wait_time),
            request_success: formatCount(item.request_success),
            request_failed: formatCount(item.request_failed),
            latency_p50: formatTime(item.latency_p50),
            latency_p95: formatTime(item.latency_p95),
            latency_p99: formatTime(item.latency_p99),
            ftut_avg: formatTime(item.ftut_avg),
            ftut_p50: formatTime(item.ftut_p50),
            ftut_p95: formatTime(item.ftut_p95),
            ftut_p99: formatTime(item.ftut_p99),
            histogram_lt_100: formatCount(item.histogram_lt_100),
            histogram_100_500: formatCount(item.histogram_100_500),
            histogram_500_1k: formatCount(item.histogram_500_1k),
            histogram_1k_5k: formatCount(item.histogram_1k_5k),
            histogram_gt_5k: formatCount(item.histogram_gt_5k),
            request_count: formatCount(item.request_success + item.request_failed),
        })),
        refetchInterval: refetchIntervalMs,
    });
}

export function useStatsTotal(options?: { refetchIntervalMs?: number | false }) {
    const { refetchIntervalMs = REFETCH_INTERVAL_CONFIG } = options ?? {};
    return useQuery({
        queryKey: ['stats', 'total'],
        queryFn: async () => {
            return apiClient.get<StatsTotal>('/api/v1/stats/total');
        },
        select: (data) => ({
            non_chat: data,
            input_token: formatCount(data.input_token),
            output_token: formatCount(data.output_token),
            total_token: formatCount(totalAccountedTokens(data)),
            input_cost: formatMoney(data.input_cost),
            output_cost: formatMoney(data.output_cost),
            total_cost: formatMoney(data.input_cost + data.output_cost),
            wait_time: formatTime(data.wait_time),
            request_success: formatCount(data.request_success),
            request_failed: formatCount(data.request_failed),
            latency_p50: formatTime(data.latency_p50),
            latency_p95: formatTime(data.latency_p95),
            latency_p99: formatTime(data.latency_p99),
            ftut_avg: formatTime(data.ftut_avg),
            ftut_p50: formatTime(data.ftut_p50),
            ftut_p95: formatTime(data.ftut_p95),
            ftut_p99: formatTime(data.ftut_p99),
            histogram_lt_100: formatCount(data.histogram_lt_100),
            histogram_100_500: formatCount(data.histogram_100_500),
            histogram_500_1k: formatCount(data.histogram_500_1k),
            histogram_1k_5k: formatCount(data.histogram_1k_5k),
            histogram_gt_5k: formatCount(data.histogram_gt_5k),
            request_count: formatCount(data.request_success + data.request_failed),
        }),
        refetchInterval: refetchIntervalMs,
    });
}



/**
 * 获取 API Key 统计数据列表 Hook
 */
export function useStatsAPIKey(options?: { enabled?: boolean; refetchIntervalMs?: number | false }) {
    const { enabled = true, refetchIntervalMs = REFETCH_INTERVAL_CONFIG } = options ?? {};

    return useQuery({
        queryKey: ['stats', 'apikey'],
        queryFn: async () => {
            return apiClient.get<StatsAPIKey[]>('/api/v1/stats/apikey');
        },
        select: (data) => data.map((item): StatsAPIKeyFormatted => ({
            api_key_id: item.api_key_id,
            name: item.name,
            non_chat: item,
            input_token: formatCount(item.input_token),
            output_token: formatCount(item.output_token),
            total_token: formatCount(totalAccountedTokens(item)),
            input_cost: formatMoney(item.input_cost),
            output_cost: formatMoney(item.output_cost),
            total_cost: formatMoney(item.input_cost + item.output_cost),
            wait_time: formatTime(item.wait_time),
            request_success: formatCount(item.request_success),
            request_failed: formatCount(item.request_failed),
            latency_p50: formatTime(item.latency_p50),
            latency_p95: formatTime(item.latency_p95),
            latency_p99: formatTime(item.latency_p99),
            ftut_avg: formatTime(item.ftut_avg),
            ftut_p50: formatTime(item.ftut_p50),
            ftut_p95: formatTime(item.ftut_p95),
            ftut_p99: formatTime(item.ftut_p99),
            histogram_lt_100: formatCount(item.histogram_lt_100),
            histogram_100_500: formatCount(item.histogram_100_500),
            histogram_500_1k: formatCount(item.histogram_500_1k),
            histogram_1k_5k: formatCount(item.histogram_1k_5k),
            histogram_gt_5k: formatCount(item.histogram_gt_5k),
            request_count: formatCount(item.request_success + item.request_failed),
        })),
        enabled,
        refetchInterval: refetchIntervalMs,
    });
}

export function useStatsChannel(options?: { enabled?: boolean; refetchIntervalMs?: number | false }) {
    const { enabled = true, refetchIntervalMs = REFETCH_INTERVAL_CONFIG } = options ?? {};

    return useQuery({
        queryKey: ['stats', 'channel'],
        queryFn: async () => {
            return apiClient.get<StatsChannelItem[]>('/api/v1/stats/channel');
        },
        select: (data) => data.map((item): StatsChannelItemFormatted => ({
            channel_id: item.channel_id,
            channel_name: item.channel_name,
            enabled: item.enabled,
            non_chat: item,
            input_token: formatCount(item.input_token),
            output_token: formatCount(item.output_token),
            total_token: formatCount(totalAccountedTokens(item)),
            input_cost: formatMoney(item.input_cost),
            output_cost: formatMoney(item.output_cost),
            total_cost: formatMoney(item.input_cost + item.output_cost),
            wait_time: formatTime(item.wait_time),
            request_success: formatCount(item.request_success),
            request_failed: formatCount(item.request_failed),
            latency_p50: formatTime(item.latency_p50),
            latency_p95: formatTime(item.latency_p95),
            latency_p99: formatTime(item.latency_p99),
            ftut_avg: formatTime(item.ftut_avg),
            ftut_p50: formatTime(item.ftut_p50),
            ftut_p95: formatTime(item.ftut_p95),
            ftut_p99: formatTime(item.ftut_p99),
            histogram_lt_100: formatCount(item.histogram_lt_100),
            histogram_100_500: formatCount(item.histogram_100_500),
            histogram_500_1k: formatCount(item.histogram_500_1k),
            histogram_1k_5k: formatCount(item.histogram_1k_5k),
            histogram_gt_5k: formatCount(item.histogram_gt_5k),
            request_count: formatCount(item.request_success + item.request_failed),
        })),
        enabled,
        refetchInterval: refetchIntervalMs,
    });
}
