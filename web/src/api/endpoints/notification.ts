import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { apiClient, API_BASE_URL } from '../client';
import { REFETCH_INTERVAL_DEFAULT } from '../constants';
import { useAuthStore } from './user';
import { logger } from '@/lib/logger';

export type NotificationType =
    | 'channel_expire'
    | 'system'
    | 'backup'
    | 'key_health'
    | 'alert'
    | 'report'
    | 'site'
    | 'usage';
export type NotificationSeverity = 'info' | 'success' | 'warning' | 'error' | 'critical';

export interface NotificationItem {
    id: number;
    type: NotificationType;
    severity: NotificationSeverity;
    title: string;
    content: string;
    // i18n 键化字段：非空时前端按 UI 语言用 t(key, args) 渲染，否则回退 title/content。
    title_key?: string;
    title_args?: string;
    content_key?: string;
    content_args?: string;
    source?: string;
    source_id?: string;
    dedupe_key?: string;
    metadata_json?: string;
    link?: string;
    read_at?: number;
    archived_at?: number;
    created_at: number;
    updated_at: number;
}

export interface NotificationFilter {
    page?: number;
    page_size?: number;
    type?: string;
    severity?: string;
    source?: string;
    read?: boolean;
    archived?: boolean;
    search?: string;
}

export const notificationQueryKey = (filter: NotificationFilter = {}) => ['notifications', 'list', filter] as const;

export function useNotifications(filter: NotificationFilter = {}) {
    const params: Record<string, string | number | boolean> = {};
    Object.entries(filter).forEach(([key, value]) => {
        if (value !== undefined) {
            params[key] = value;
        }
    });

    return useQuery({
        queryKey: notificationQueryKey(filter),
        queryFn: async () => apiClient.get<NotificationItem[]>('/api/v1/notification/list', params),
        refetchInterval: REFETCH_INTERVAL_DEFAULT,
    });
}

export const NOTIFICATION_PAGE_SIZE = 20;

/**
 * 通知列表无限滚动 hook：按页追加加载，避免一次性渲染全量通知导致页面无限变长。
 * 与 useLogs 同构：hasNextPage 由"末页条数 < pageSize"判定，流式新通知导致的
 * 页间重复 id 在合并时去重。
 */
export function useNotificationsInfinite(filter: NotificationFilter = {}) {
    const query = useInfiniteQuery({
        queryKey: ['notifications', 'list', 'infinite', filter] as const,
        initialPageParam: 1,
        queryFn: async ({ pageParam }) => {
            const params: Record<string, string | number | boolean> = {
                page: pageParam,
                page_size: NOTIFICATION_PAGE_SIZE,
            };
            Object.entries(filter).forEach(([key, value]) => {
                if (value !== undefined) {
                    params[key] = value;
                }
            });
            const result = await apiClient.get<NotificationItem[] | null>('/api/v1/notification/list', params);
            return result ?? [];
        },
        getNextPageParam: (lastPage, allPages) => {
            if (lastPage.length < NOTIFICATION_PAGE_SIZE) return undefined;
            return allPages.length + 1;
        },
        refetchInterval: REFETCH_INTERVAL_DEFAULT,
    });

    const items = useMemo(() => {
        const pages = query.data?.pages ?? [];
        const seen = new Set<number>();
        const merged: NotificationItem[] = [];
        for (const page of pages) {
            for (const item of page) {
                if (seen.has(item.id)) continue;
                seen.add(item.id);
                merged.push(item);
            }
        }
        return merged;
    }, [query.data]);

    const loadMore = useCallback(async () => {
        if (!query.hasNextPage || query.isFetchingNextPage) return;
        try {
            await query.fetchNextPage();
        } catch (error) {
            logger.error('Load more notifications failed:', error);
        }
    }, [query]);

    return {
        items,
        isLoading: query.isLoading,
        isLoadingMore: query.isFetchingNextPage,
        hasMore: Boolean(query.hasNextPage),
        loadMore,
        refetch: query.refetch,
    };
}

export function useUnreadNotificationCount() {
    return useQuery({
        queryKey: ['notifications', 'unread-count'],
        queryFn: async () => apiClient.get<{ count: number }>('/api/v1/notification/unread-count'),
        refetchInterval: REFETCH_INTERVAL_DEFAULT,
    });
}

export function useNotificationDetail(id?: number) {
    return useQuery({
        queryKey: ['notifications', 'detail', id],
        queryFn: async () => apiClient.get<NotificationItem>(`/api/v1/notification/detail/${id}`),
        enabled: Boolean(id),
    });
}

function useIDsMutation(path: string, logLabel: string) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (ids: number[]) => apiClient.post<null>(path, { ids }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
        },
        onError: (error) => logger.error(`${logLabel} failed:`, error),
    });
}

export function useMarkNotificationRead() { return useIDsMutation('/api/v1/notification/mark-read', 'Mark notification read'); }
export function useMarkNotificationUnread() { return useIDsMutation('/api/v1/notification/mark-unread', 'Mark notification unread'); }
export function useArchiveNotification() { return useIDsMutation('/api/v1/notification/archive', 'Archive notification'); }
export function useUnarchiveNotification() { return useIDsMutation('/api/v1/notification/unarchive', 'Unarchive notification'); }

export function useMarkAllNotificationsRead() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async () => apiClient.post<null>('/api/v1/notification/mark-all-read'),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
    });
}

export function useDeleteNotification() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (id: number) => apiClient.delete<null>(`/api/v1/notification/delete/${id}`),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
    });
}

export function useNotificationStream() {
    const token = useAuthStore((state) => state.token);
    const queryClient = useQueryClient();
    const abortRef = useRef<AbortController | null>(null);
    const [isConnected, setIsConnected] = useState(false);

    const handleNotification = useCallback((item: NotificationItem) => {
        queryClient.setQueryData<{ count: number }>(['notifications', 'unread-count'], (old) => ({
            count: (old?.count ?? 0) + (item.read_at ? 0 : 1),
        }));
        // 增量写入第一页缓存，避免 invalidate 触发 infinite query 全量重拉。
        queryClient.setQueriesData<{ pages: NotificationItem[][]; pageParams: unknown[] }>(
            { queryKey: ['notifications', 'list'] },
            (old) => {
                if (!old || !('pages' in old) || !Array.isArray(old.pages)) {
                    return old;
                }
                const first = old.pages[0] ?? [];
                if (first.some((n) => n.id === item.id)) {
                    return old;
                }
                const pages = [...old.pages];
                pages[0] = [item, ...first];
                return { ...old, pages };
            },
        );
    }, [queryClient]);

    useEffect(() => {
        if (!token) return;
        let cancelled = false;
        let retryTimer: ReturnType<typeof setTimeout> | null = null;

        async function connect() {
            abortRef.current?.abort();
            const controller = new AbortController();
            abortRef.current = controller;
            try {
                const response = await fetch(`${API_BASE_URL}/api/v1/notification/stream`, {
                    headers: { Authorization: `Bearer ${token}` },
                    signal: controller.signal,
                });
                if (!response.ok || !response.body) throw new Error(`stream failed: ${response.status}`);
                setIsConnected(true);
                const reader = response.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';
                while (!cancelled) {
                    const { value, done } = await reader.read();
                    if (done) break;
                    buffer += decoder.decode(value, { stream: true });
                    const frames = buffer.split('\n\n');
                    buffer = frames.pop() ?? '';
                    for (const frame of frames) {
                        const line = frame.split('\n').find((l) => l.startsWith('data: '));
                        if (!line) continue;
                        try {
                            handleNotification(JSON.parse(line.slice(6)) as NotificationItem);
                        } catch (error) {
                            logger.error('Parse notification stream failed:', error);
                        }
                    }
                }
            } catch (error) {
                if (!cancelled) logger.error('Notification stream error:', error);
            } finally {
                setIsConnected(false);
                if (!cancelled) retryTimer = setTimeout(connect, 5000);
            }
        }

        connect();
        return () => {
            cancelled = true;
            if (retryTimer) clearTimeout(retryTimer);
            abortRef.current?.abort();
        };
    }, [handleNotification, token]);

    return useMemo(() => ({ isConnected }), [isConnected]);
}
