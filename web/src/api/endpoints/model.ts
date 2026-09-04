import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { REFETCH_INTERVAL_CONFIG } from '../constants';
import { logger } from '@/lib/logger';

/**
 * LLM 价格信息
 */
export interface LLMPrice {
    input: number;
    output: number;
    cache_read: number;
    cache_write: number;
}

/**
 * LLM 模型信息
 */
export interface LLMInfo extends LLMPrice {
    name: string;
    price_manual?: boolean;      // 价格是否手动设置（同步刷新时保留）
    billing_schedule?: string;   // 峰谷计费标识（"deepseek_v4" 或空，只读展示用）
}

/**
 * 投影渠道从上游站点同步到的展示用定价（不参与本地计费）。
 * billing_mode: token = $/M；per_call = $/次
 */
export interface ChannelUpstreamPrice {
    billing_mode: 'token' | 'per_call' | string;
    input: number;
    output: number;
    cache_read: number;
    cache_write: number;
}

/**
 * 上游模型广场性能指标（NewAPI /api/perf-metrics/summary）。
 * success_rate: 0-1
 */
export interface ChannelUpstreamMetrics {
    latency_ms: number;
    avg_tps: number;
    success_rate: number;
}

/**
 * LLM 渠道关联信息
 */
export interface LLMChannel {
    name: string;
    enabled: boolean;
    channel_id: number;
    channel_name: string;
    upstream_price?: ChannelUpstreamPrice | null;
    upstream_metrics?: ChannelUpstreamMetrics | null;
    channel_balance?: number | null;
    channel_today_income?: number | null;
}

export interface ModelMarketChannel {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    enabled_key_count: number;
}

export interface ModelMarketItem extends LLMInfo {
    channel_count: number;
    enabled_key_count: number;
    average_latency_ms: number;
    success_rate: number;
    request_success: number;
    request_failed: number;
    channels: ModelMarketChannel[];
}

export interface ModelMarketSummary {
    model_count: number;
    coverage_count: number;
    unique_channel_count: number;
    average_latency_ms: number;
    last_update_time?: string;
}

export interface ModelMarketResponse {
    summary: ModelMarketSummary;
    items: ModelMarketItem[];
}

const EMPTY_MODEL_MARKET_SUMMARY: ModelMarketSummary = {
    model_count: 0,
    coverage_count: 0,
    unique_channel_count: 0,
    average_latency_ms: 0,
    last_update_time: '',
};

function normalizeModelMarketResponse(response: Partial<ModelMarketResponse> | null | undefined): ModelMarketResponse {
    const items = Array.isArray(response?.items) ? response.items : [];

    return {
        summary: response?.summary ?? EMPTY_MODEL_MARKET_SUMMARY,
        items: items.map((item) => ({
            ...item,
            channels: Array.isArray(item.channels) ? item.channels : [],
        })),
    };
}

/**
 * 获取 LLM 模型列表 Hook
 * 
 * @example
 * const { data: models, isLoading, error } = useModelList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * models?.forEach(model => console.log(model.name, model.input));
 */
export function useModelList() {
    return useQuery({
        queryKey: ['models', 'list'],
        queryFn: async () => {
            return apiClient.get<LLMInfo[]>('/api/v1/model/list');
        },
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}

/**
 * 获取 LLM 模型与渠道关联列表 Hook
 * 
 * @example
 * const { data: channelModels, isLoading, error } = useModelChannelList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * channelModels?.forEach(item => console.log(item.name, item.channel_name));
 */
export function useModelChannelList() {
    return useQuery({
        queryKey: ['models', 'channel'],
        queryFn: async () => {
            return apiClient.get<LLMChannel[]>('/api/v1/model/channel');
        },
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}

export function useModelMarket() {
    return useQuery({
        queryKey: ['models', 'market'],
        queryFn: async () => {
            const response = await apiClient.get<ModelMarketResponse>('/api/v1/model/market');
            return normalizeModelMarketResponse(response);
        },
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}

/**
 * 更新 LLM 模型 Hook
 * 
 * @example
 * const updateModel = useUpdateModel();
 * 
 * updateModel.mutate({
 *   name: 'gpt-4',
 *   input: 0.03,
 *   output: 0.06,
 *   cache_read: 0.015,
 *   cache_write: 0.03,
 * });
 */
export function useUpdateModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (data: LLMInfo) => {
            return apiClient.post<LLMInfo>('/api/v1/model/update', data);
        },
        onSuccess: (data) => {
            logger.log('模型更新成功:', data);
            queryClient.invalidateQueries({ queryKey: ['models', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
        onError: (error) => {
            logger.error('模型更新失败:', error);
        },
    });
}

/**
 * 创建 LLM 模型 Hook
 * 
 * @example
 * const createModel = useCreateModel();
 * 
 * createModel.mutate({
 *   name: 'gpt-4',
 *   input: 0.03,
 *   output: 0.06,
 *   cache_read: 0.015,
 *   cache_write: 0.03,
 * });
 */
export function useCreateModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (data: LLMInfo) => {
            return apiClient.post<LLMInfo>('/api/v1/model/create', data);
        },
        onSuccess: (data) => {
            logger.log('模型创建成功:', data);
            queryClient.invalidateQueries({ queryKey: ['models', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
        onError: (error) => {
            logger.error('模型创建失败:', error);
        },
    });
}

/**
 * 删除 LLM 模型 Hook
 * 
 * @example
 * const deleteModel = useDeleteModel();
 * 
 * deleteModel.mutate('gpt-4'); // 删除名称为 'gpt-4' 的模型
 */
export function useDeleteModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (name: string) => {
            return apiClient.post<null>('/api/v1/model/delete', { name });
        },
        onSuccess: () => {
            logger.log('模型删除成功');
            queryClient.invalidateQueries({ queryKey: ['models', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
        onError: (error) => {
            logger.error('模型删除失败:', error);
        },
    });
}

/**
 * 更新 LLM 模型价格 Hook
 * 
 * @example
 * const updatePrice = useUpdateModelPrice();
 * 
 * updatePrice.mutate(); // 触发价格更新
 */
export function useUpdateModelPrice() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async () => {
            return apiClient.post<null>('/api/v1/model/update-price', {});
        },
        onSuccess: () => {
            logger.log('模型价格更新成功');
            queryClient.invalidateQueries({ queryKey: ['models', 'last-update-time'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
        onError: (error) => {
            logger.error('模型价格更新失败:', error);
        },
    });
}

/**
 * 获取 LLM 模型价格最后更新时间 Hook
 * 
 * @example
 * const { data: lastUpdateTime } = useLastUpdateTime();
 * 
 * if (lastUpdateTime) {
 *   console.log('最后更新:', new Date(lastUpdateTime).toLocaleString());
 * }
 */
export function useLastUpdateTime() {
    return useQuery({
        queryKey: ['models', 'last-update-time'],
        queryFn: async () => {
            return apiClient.get<string>('/api/v1/model/last-update-time');
        },
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}

/**
 * 模型能力信息（来自 GET /api/v1/model/capabilities）
 */
export interface ModelCapability {
    name: string;
    endpoints: string[];
    conversation: boolean;
    available: boolean;
}

/**
 * 获取模型能力列表 Hook
 *
 * @example
 * const { data: capabilities, isLoading, error } = useModelCapabilities();
 *
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 *
 * capabilities?.forEach(cap => {
 *     console.log(cap.name, cap.endpoints, cap.conversation);
 * });
 */
export function useModelCapabilities() {
    return useQuery({
        queryKey: ['models', 'capabilities'],
        queryFn: async () => {
            return apiClient.get<ModelCapability[]>('/api/v1/model/capabilities');
        },
        refetchInterval: 60_000,
    });
}

/**
 * 模型价格分类（兜底定价）。
 */
export interface ModelPriceCategory extends LLMPrice {
    id: number;
    name: string;
    rule_type: 'exact' | 'prefix' | 'contains' | string;
    rule_value: string;
    sort_order: number;
    enabled: boolean;
    created_at?: string;
    updated_at?: string;
}

/**
 * 获取价格分类列表 Hook
 */
export function usePriceCategoryList() {
    return useQuery({
        queryKey: ['models', 'price-category', 'list'],
        queryFn: async () => {
            return apiClient.get<ModelPriceCategory[]>('/api/v1/model/price-category/list');
        },
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}

/**
 * 创建价格分类
 */
export function useCreatePriceCategory() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (data: Omit<ModelPriceCategory, 'id'>) => {
            return apiClient.post<ModelPriceCategory>('/api/v1/model/price-category/create', data);
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'price-category', 'list'] });
            // 兜底定价改动需让价格缓存失效。
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
    });
}

/**
 * 更新价格分类
 */
export function useUpdatePriceCategory() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (data: ModelPriceCategory) => {
            return apiClient.post<ModelPriceCategory>('/api/v1/model/price-category/update', data);
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'price-category', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
    });
}

/**
 * 删除价格分类
 */
export function useDeletePriceCategory() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (id: number) => {
            return apiClient.post<null>('/api/v1/model/price-category/delete', { id });
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'price-category', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
        },
    });
}

/**
 * 峰谷计费规则（按时段缩放定价）。LLMPrice 为高峰价（USD/1M），
 * 空闲价 = 高峰价 × off_peak_mul；窗口为北京时间分钟（0-1440）。
 * weekend_off_peak = true 时，北京时间周六/周日全天按空闲价计费。
 */
export interface ModelPriceSchedule extends LLMPrice {
    id: number;
    name: string;
    rule_type: 'exact' | 'prefix' | 'contains' | string;
    rule_value: string;
    off_peak_mul: number;
    weekend_off_peak: boolean;
    window1_start: number;
    window1_end: number;
    window2_start: number;
    window2_end: number;
    sort_order: number;
    enabled: boolean;
    created_at?: string;
    updated_at?: string;
}

/**
 * 获取峰谷计费规则列表 Hook
 */
export function usePriceScheduleList() {
    return useQuery({
        queryKey: ['models', 'price-schedule', 'list'],
        queryFn: async () => {
            return apiClient.get<ModelPriceSchedule[]>('/api/v1/model/price-schedule/list');
        },
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });
}

/**
 * 创建峰谷计费规则
 */
export function useCreatePriceSchedule() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (data: Omit<ModelPriceSchedule, 'id'>) => {
            return apiClient.post<ModelPriceSchedule>('/api/v1/model/price-schedule/create', data);
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'price-schedule', 'list'] });
            // 峰谷规则改动需让模型列表峰谷标注与价格缓存失效。
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'list'] });
        },
    });
}

/**
 * 更新峰谷计费规则
 */
export function useUpdatePriceSchedule() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (data: ModelPriceSchedule) => {
            return apiClient.post<ModelPriceSchedule>('/api/v1/model/price-schedule/update', data);
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'price-schedule', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'list'] });
        },
    });
}

/**
 * 删除峰谷计费规则
 */
export function useDeletePriceSchedule() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (id: number) => {
            return apiClient.post<null>('/api/v1/model/price-schedule/delete', { id });
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'price-schedule', 'list'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'market'] });
            queryClient.invalidateQueries({ queryKey: ['models', 'list'] });
        },
    });
}
