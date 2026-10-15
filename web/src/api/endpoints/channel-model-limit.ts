import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';

/**
 * v2.9.0：渠道×模型上下文上限。
 * source=manual 为人工声明（永不被自动学习覆盖）；source=learned 为累计多次
 * 独立超限后写入的学习值（evidence_count 为独立证据数）。
 */
export interface ChannelModelLimit {
    channel_id: number;
    model_name: string;
    max_context_tokens: number;
    source: 'manual' | 'learned' | string;
    evidence_count: number;
    updated_at: string;
}

const channelModelLimitKey = (channelId: number) => ['channel-model-limit', channelId] as const;

export function useChannelModelLimits(channelId?: number) {
    return useQuery({
        queryKey: channelModelLimitKey(channelId ?? 0),
        enabled: typeof channelId === 'number' && channelId > 0,
        queryFn: () => apiClient.get<ChannelModelLimit[]>('/api/v1/channel-model-limit', { channel_id: channelId as number }),
    });
}

export function useSetChannelModelLimit(channelId: number) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: { model_name: string; max_context_tokens: number }) =>
            apiClient.put<void>('/api/v1/channel-model-limit', { channel_id: channelId, ...data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: channelModelLimitKey(channelId) });
        },
    });
}

export function useDeleteChannelModelLimit(channelId: number) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (modelName: string) =>
            apiClient.delete<void>('/api/v1/channel-model-limit', { channel_id: channelId, model_name: modelName }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: channelModelLimitKey(channelId) });
        },
    });
}
