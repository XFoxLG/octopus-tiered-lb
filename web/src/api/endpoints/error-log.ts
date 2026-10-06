import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';
import { withExactId } from '@/lib/exact-id';

/**
 * 错误日志条目（前后端崩溃/错误，与转发日志 relay_logs 分离）。
 */
export interface ErrorLog {
    id: string | number;
    id_str?: string;
    time: number;              // 时间戳（秒）
    source: 'backend' | 'frontend'; // 来源
    level: string;             // panic | error | unhandledrejection | uncaught
    message: string;           // 错误信息
    stack: string;             // 完整堆栈
    request_method?: string;   // 后端请求方法
    request_path?: string;     // 后端请求路径
    client_ip?: string;
    user_agent?: string;
    page_url?: string;         // 前端页面 URL
    route_id?: string;         // 前端路由/模块 id
    version?: string;          // 上报方版本
}

export interface ErrorLogFilter {
    source?: 'backend' | 'frontend';
    level?: string;
    start_time?: number;
    end_time?: number;
}

export const DEFAULT_ERROR_LOG_PAGE_SIZE = 20;

const errorLogsQueryKey = (pageSize: number, filter: ErrorLogFilter) => ['error-logs', 'infinite', pageSize, filter] as const;

/**
 * 错误日志分页查询 Hook（滚动加载）。
 */
export function useErrorLogs(options: { pageSize?: number; filter?: ErrorLogFilter } = {}) {
    const { pageSize = DEFAULT_ERROR_LOG_PAGE_SIZE, filter = {} } = options;
    return useInfiniteQuery({
        queryKey: errorLogsQueryKey(pageSize, filter),
        initialPageParam: 1,
        queryFn: async ({ pageParam }) => {
            const params = new URLSearchParams();
            params.set('page', String(pageParam));
            params.set('page_size', String(pageSize));
            if (filter.source) params.set('source', filter.source);
            if (filter.level) params.set('level', filter.level);
            if (filter.start_time != null) params.set('start_time', String(filter.start_time));
            if (filter.end_time != null) params.set('end_time', String(filter.end_time));
            const result = await apiClient.get<ErrorLog[] | null>(`/api/v1/error-log/list?${params.toString()}`);
            return (result ?? []).map(withExactId);
        },
        getNextPageParam: (lastPage, allPages) => {
            if (!lastPage || lastPage.length < pageSize) return undefined;
            return allPages.length + 1;
        },
        staleTime: 30_000,
    });
}

/**
 * 清空错误日志。
 */
export function useClearErrorLogs() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async () => {
            return apiClient.delete<null>('/api/v1/error-log/clear');
        },
        onSuccess: () => {
            logger.log('错误日志清空成功');
            queryClient.invalidateQueries({ queryKey: ['error-logs'] });
        },
        onError: (error) => {
            logger.error('错误日志清空失败:', error);
        },
    });
}
