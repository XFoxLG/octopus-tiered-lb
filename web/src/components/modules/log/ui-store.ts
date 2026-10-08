'use client';

import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export type LogFieldName =
    | 'endpointType'
    | 'channelName'
    | 'actualModel'
    | 'apiKeyName'
    | 'clientIP'
    | 'cost'
    | 'tps'
    | 'cacheHitRate'
    | 'reasoningEffort'
    | 'reasoningTokens';

export type LogFieldVisibility = Record<LogFieldName, boolean>;

export const DEFAULT_LOG_FIELD_VISIBILITY: LogFieldVisibility = {
    endpointType: true,
    channelName: true,
    actualModel: true,
    apiKeyName: true,
    clientIP: true,
    cost: true,
    tps: true,
    cacheHitRate: true,
    reasoningEffort: true,
    reasoningTokens: true,
};

export const COMPACT_LOG_FIELD_VISIBILITY: LogFieldVisibility = {
    ...DEFAULT_LOG_FIELD_VISIBILITY,
    endpointType: false, apiKeyName: false, clientIP: false, tps: false,
    cacheHitRate: false, reasoningEffort: false, reasoningTokens: false,
};

export const LOG_FIELD_VISIBILITY_STORAGE_VERSION = 1;

/**
 * 旧版（version 0）默认开启「简洁」模式；自 version 1 起默认完整显示。
 * 迁移只把旧的 compact 默认值关掉一次，用户之后手动开启仍会按新版本持久化。
 * 用户手动改过的字段可见性保留，只重置 compact，不覆盖 visibility。
 */
export function migrateLogFieldVisibility(
    persisted: unknown,
    version: number,
): Partial<LogFieldVisibilityState> {
    const state = (persisted ?? {}) as Partial<LogFieldVisibilityState>;
    if (version >= LOG_FIELD_VISIBILITY_STORAGE_VERSION) return state;
    return {
        ...state,
        compact: false,
        visibility: { ...DEFAULT_LOG_FIELD_VISIBILITY, ...(state.visibility ?? {}) },
    };
}

type LogFieldVisibilityState = {
    compact: boolean;
    toggleCompact: () => void;
    visibility: LogFieldVisibility;
    toggleField: (field: LogFieldName) => void;
    resetFields: () => void;
};

export const useLogFieldVisibilityStore = create<LogFieldVisibilityState>()(
    persist(
        (set) => ({
            compact: false,
            toggleCompact: () => set(state => ({ compact: !state.compact })),
            visibility: { ...DEFAULT_LOG_FIELD_VISIBILITY },
            toggleField: (field) =>
                set((state) => ({
                    compact: false,
                    visibility: {
                        ...(state.compact ? COMPACT_LOG_FIELD_VISIBILITY : state.visibility),
                        [field]: !(state.compact ? COMPACT_LOG_FIELD_VISIBILITY : state.visibility)[field],
                    },
                })),
            resetFields: () =>
                set({ compact: false, visibility: { ...DEFAULT_LOG_FIELD_VISIBILITY } }),
        }),
        {
            name: 'log-field-visibility-storage',
            version: LOG_FIELD_VISIBILITY_STORAGE_VERSION,
            migrate: (persisted, version) => migrateLogFieldVisibility(persisted, version) as LogFieldVisibilityState,
            partialize: (state) => ({
                compact: state.compact,
                visibility: state.visibility,
            }),
        },
    ),
);

export function useLogFieldVisibility() {
    return useLogFieldVisibilityStore((s) => s.compact ? COMPACT_LOG_FIELD_VISIBILITY : s.visibility);
}

/**
 * 日志模型搜索文本，在标题栏搜索框与 Log 组件筛选逻辑之间共享。
 */
export const useLogModelSearchStore = create<{
    modelSearch: string;
    setModelSearch: (value: string) => void;
}>()((set) => ({
    modelSearch: '',
    setModelSearch: (value) => set({ modelSearch: value }),
}));

/**
 * 日志列表自动刷新间隔（秒）。0 表示关闭。
 * 属于浏览器本地偏好（不同客户端可各自设置），持久化到 localStorage。
 */
export const LOG_AUTO_REFRESH_OPTIONS = [0, 5, 10, 30] as const;
export type LogAutoRefreshInterval = (typeof LOG_AUTO_REFRESH_OPTIONS)[number];

export const useLogAutoRefreshStore = create<{
    interval: LogAutoRefreshInterval;
    setInterval: (value: LogAutoRefreshInterval) => void;
}>()(
    persist(
        (set) => ({
            interval: 0 as LogAutoRefreshInterval,
            setInterval: (value) => set({ interval: value }),
        }),
        {
            name: 'log-auto-refresh-storage',
            partialize: (state) => ({ interval: state.interval }),
        },
    ),
);

