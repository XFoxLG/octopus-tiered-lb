'use client';

import { useCallback, useMemo, useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { apiClient } from '../client';

export interface AuditLogEntry {
    id: number;
    user_id: number;
    username: string;
    action: string;
    method: string;
    path: string;
    status_code: number;
    target: string;
    created_at: number;
}

const auditLogsInfiniteQueryKey = (pageSize: number) => ['audit', 'infinite', pageSize] as const;

export const DEFAULT_AUDIT_PAGE_SIZE = 12;

export function useAuditLogs(options: { pageSize?: number } = {}) {
    const { pageSize = DEFAULT_AUDIT_PAGE_SIZE } = options;

    const auditLogsQuery = useInfiniteQuery({
        queryKey: auditLogsInfiniteQueryKey(pageSize),
        initialPageParam: 1,
        queryFn: async ({ pageParam }) => {
            const params = new URLSearchParams();
            params.set('page', String(pageParam));
            params.set('page_size', String(pageSize));
            const result = await apiClient.get<AuditLogEntry[] | null>(`/api/v1/audit/list?${params.toString()}`);
            return result ?? [];
        },
        getNextPageParam: (lastPage, allPages) => {
            if (!lastPage || lastPage.length < pageSize) return undefined;
            return allPages.length + 1;
        },
        staleTime: 30000,
    });

    const logs = useMemo(() => {
        const pages = auditLogsQuery.data?.pages ?? [];
        const seen = new Set<number>();
        const merged: AuditLogEntry[] = [];

        for (const page of pages) {
            for (const item of page) {
                if (seen.has(item.id)) continue;
                seen.add(item.id);
                merged.push(item);
            }
        }

        merged.sort((left, right) => {
            if (left.created_at !== right.created_at) {
                return right.created_at - left.created_at;
            }
            return right.id - left.id;
        });
        return merged;
    }, [auditLogsQuery.data]);

    const loadMore = useCallback(async () => {
        if (!auditLogsQuery.hasNextPage || auditLogsQuery.isFetchingNextPage) {
            return;
        }
        await auditLogsQuery.fetchNextPage();
    }, [auditLogsQuery]);

    return {
        logs,
        error: auditLogsQuery.error,
        hasMore: !!auditLogsQuery.hasNextPage,
        isLoading: auditLogsQuery.isLoading,
        isLoadingMore: auditLogsQuery.isFetchingNextPage,
        loadMore,
    };
}

export function useAuditLogDetail() {
    const [detail, setDetail] = useState<AuditLogEntry | null>(null);
    const [isLoading, setIsLoading] = useState(false);

    const fetchDetail = useCallback(async (id: number) => {
        setIsLoading(true);
        try {
            const result = await apiClient.get<AuditLogEntry | null>(`/api/v1/audit/detail?id=${id}`);
            setDetail(result);
        } catch {
            setDetail(null);
        } finally {
            setIsLoading(false);
        }
    }, []);

    const reset = useCallback(() => {
        setDetail(null);
    }, []);

    return { detail, isLoading, fetchDetail, reset };
}
