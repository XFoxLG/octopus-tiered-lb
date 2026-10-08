'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useChannelList } from '@/api/endpoints/channel';
import { useAPIKeyList } from '@/api/endpoints/apikey';
import { useLogs, type LogFilter } from '@/api/endpoints/log';
import { useModelList } from '@/api/endpoints/model';
import { LogCard } from './Item';
import { ErrorLogView } from './ErrorLogView';
import { LiveRequests } from './LiveRequests';
import { Loader2, X, Columns3, Check, ChevronsUpDown, Search } from 'lucide-react';
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover';
import { useLogFieldVisibility, useLogFieldVisibilityStore, useLogModelSearchStore, useLogAutoRefreshStore, type LogFieldName } from './ui-store';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { useNavHandoff } from '@/lib/nav-handoff';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { ENDPOINT_TYPE_OPTIONS } from '@/components/modules/group/utils';
import { maskChannelKeySecret } from '@/components/modules/channel/key-model-fill';

const EMPTY_FILTER: LogFilter = {};

/**
 * 日志筛选栏
 */
function LogFilterBar({
    filter,
    onChange,
}: {
    filter: LogFilter;
    onChange: (f: LogFilter) => void;
}) {
    const t = useTranslations('log.filter');
    const tGroup = useTranslations('group');
    const tView = useTranslations('log.viewOptions');
    const { data: channels = [] } = useChannelList();
    const { data: apiKeys = [] } = useAPIKeyList();
    const { data: models = [] } = useModelList();
    const visibility = useLogFieldVisibility();
    const compact = useLogFieldVisibilityStore((s) => s.compact);

    const hasFilter = !!(
        filter.channel_id != null ||
        filter.api_key_id != null ||
        filter.endpoint_type ||
        filter.status ||
        filter.is_test != null ||
        (filter.models && filter.models.length > 0)
    );

    const selectedModels = useMemo(() => new Set(filter.models ?? []), [filter.models]);
    const [modelSearchTerm, setModelSearchTerm] = useState('');
    const modelSearchInputRef = useRef<HTMLInputElement>(null);

    const filteredModelOptions = useMemo(() => {
        const term = modelSearchTerm.trim().toLowerCase();
        const sorted = [...models].sort((a, b) => a.name.localeCompare(b.name));
        if (!term) return sorted;
        return sorted.filter((m) => m.name.toLowerCase().includes(term));
    }, [models, modelSearchTerm]);

    const toggleModel = useCallback((name: string) => {
        const next = { ...filter };
        const set = new Set(next.models ?? []);
        if (set.has(name)) {
            set.delete(name);
        } else {
            set.add(name);
        }
        if (set.size > 0) {
            next.models = Array.from(set);
        } else {
            delete next.models;
        }
        onChange(next);
    }, [filter, onChange]);

    const setModelSearch = useLogModelSearchStore((s) => s.setModelSearch);
    const handleClear = useCallback(() => {
        onChange(EMPTY_FILTER);
        setModelSearch('');
    }, [onChange, setModelSearch]);

    return (
        <div className="flex flex-nowrap items-center gap-2 overflow-x-auto py-1">
            <Select
                value={filter.channel_id != null ? String(filter.channel_id) : ''}
                onValueChange={(v) => {
                    const next = { ...filter };
                    if (v && v !== '' && v !== '__all__') {
                        next.channel_id = Number(v);
                    } else {
                        delete next.channel_id;
                    }
                    onChange(next);
                }}
            >
                <SelectTrigger size="sm" className="h-7 text-xs min-w-[7rem]">
                    <SelectValue placeholder={t('allChannels')} />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="__all__">{t('allChannels')}</SelectItem>
                    {channels.map((ch) => (
                        <SelectItem key={ch.raw.id} value={String(ch.raw.id)}>
                            {ch.raw.name}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>

            <Select
                value={filter.api_key_id != null ? String(filter.api_key_id) : ''}
                onValueChange={(v) => {
                    const next = { ...filter };
                    if (v && v !== '' && v !== '__all__') {
                        next.api_key_id = Number(v);
                    } else {
                        delete next.api_key_id;
                    }
                    onChange(next);
                }}
            >
                <SelectTrigger size="sm" className="h-7 text-xs min-w-[7rem]">
                    <SelectValue placeholder={t('allKeys')} />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="__all__">{t('allKeys')}</SelectItem>
                    {apiKeys.map((key) => (
                        <SelectItem key={key.id} value={String(key.id)}>
                            {key.name}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>

            <Select
                value={filter.endpoint_type ?? ''}
                onValueChange={(v) => {
                    const next = { ...filter };
                    if (v && v !== '' && v !== '__all__') {
                        next.endpoint_type = v;
                    } else {
                        delete next.endpoint_type;
                    }
                    onChange(next);
                }}
            >
                <SelectTrigger size="sm" className="h-7 text-xs min-w-[7rem]">
                    <SelectValue placeholder={t('allTypes')} />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="__all__">{t('allTypes')}</SelectItem>
                    {ENDPOINT_TYPE_OPTIONS.map((opt) => (
                        <SelectItem key={opt.value} value={opt.value}>
                            {tGroup(opt.labelKey)}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>

            <Select
                value={filter.status ?? ''}
                onValueChange={(v) => {
                    const next = { ...filter };
                    if (v && v !== '' && v !== '__all__') {
                        next.status = v as 'success' | 'error';
                    } else {
                        delete next.status;
                    }
                    onChange(next);
                }}
            >
                <SelectTrigger size="sm" className="h-7 text-xs min-w-[6rem]">
                    <SelectValue placeholder={t('allStatuses')} />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="__all__">{t('allStatuses')}</SelectItem>
                    <SelectItem value="success">{t('statusSuccess')}</SelectItem>
                    <SelectItem value="error">{t('statusError')}</SelectItem>
                </SelectContent>
            </Select>

            <Select
                value={filter.is_test != null ? String(filter.is_test) : ''}
                onValueChange={(v) => {
                    const next = { ...filter };
                    if (v === 'true') {
                        next.is_test = true;
                    } else if (v === 'false') {
                        next.is_test = false;
                    } else {
                        delete next.is_test;
                    }
                    onChange(next);
                }}
            >
                <SelectTrigger size="sm" className="h-7 text-xs min-w-[6rem]">
                    <SelectValue placeholder={t('allLogs')} />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="__all__">{t('allLogs')}</SelectItem>
                    <SelectItem value="true">{t('testOnly')}</SelectItem>
                    <SelectItem value="false">{t('nonTest')}</SelectItem>
                </SelectContent>
            </Select>
            <Popover
                onOpenChange={(open) => {
                    if (open) {
                        setModelSearchTerm('');
                        requestAnimationFrame(() => modelSearchInputRef.current?.focus());
                    }
                }}
            >
                <PopoverTrigger asChild>
                    <button
                        type="button"
                        className="flex h-7 items-center gap-1 rounded-md border border-border/50 bg-background px-2 text-xs hover:bg-muted transition-colors min-w-[7rem]"
                    >
                        <span className="truncate">
                            {selectedModels.size > 0
                                ? `${t('modelsSelected', { count: selectedModels.size })}`
                                : t('allModels')}
                        </span>
                        <ChevronsUpDown className="size-3 shrink-0 opacity-50" />
                    </button>
                </PopoverTrigger>
                <PopoverContent align="start" className="w-64 p-0">
                    <div className="flex items-center gap-1.5 border-b border-border/50 px-2.5 py-2">
                        <Search className="size-3.5 shrink-0 text-muted-foreground" />
                        <input
                            ref={modelSearchInputRef}
                            value={modelSearchTerm}
                            onChange={(e) => setModelSearchTerm(e.target.value)}
                            placeholder={t('searchModel')}
                            className="h-5 min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground/50"
                        />
                        {selectedModels.size > 0 && (
                            <button
                                type="button"
                                onClick={() => {
                                    const next = { ...filter };
                                    delete next.models;
                                    onChange(next);
                                }}
                                className="shrink-0 text-xs text-muted-foreground hover:text-foreground"
                            >
                                {t('clearModels')}
                            </button>
                        )}
                    </div>
                    <div className="max-h-60 overflow-y-auto py-1">
                        {filteredModelOptions.length === 0 ? (
                            <p className="px-2.5 py-2 text-xs text-muted-foreground">{t('noModels')}</p>
                        ) : (
                            filteredModelOptions.map((m) => {
                                const checked = selectedModels.has(m.name);
                                return (
                                    <button
                                        key={m.name}
                                        type="button"
                                        onClick={() => toggleModel(m.name)}
                                        className="flex w-full items-center gap-2 px-2.5 py-1 text-xs hover:bg-muted transition-colors"
                                    >
                                        <span className={`flex size-3.5 shrink-0 items-center justify-center rounded border ${checked ? 'border-primary bg-primary text-primary-foreground' : 'border-border/60'}`}>
                                            {checked && <Check className="size-3" />}
                                        </span>
                                        <span className="truncate">{m.name}</span>
                                    </button>
                                    );
                                })
                        )}
                    </div>
                </PopoverContent>
            </Popover>

            {hasFilter && (
                <button
                    type="button"
                    onClick={handleClear}
                    className="flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                >
                    <X className="size-3" />
                    {t('clear')}
                </button>
            )}

            <Popover>
                <PopoverTrigger asChild>
                    <button
                        type="button"
                        className="flex items-center gap-1 rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                        title={tView('title')}
                        aria-label={tView('title')}
                    >
                        <Columns3 className="size-3.5" />
                    </button>
                </PopoverTrigger>
                <PopoverContent align="end" className="w-52 p-3">
                    <p className="text-xs font-medium text-muted-foreground mb-2">{tView('title')}</p>
                    <label className="mb-2 flex items-center gap-2 text-sm">
                        <input type="checkbox" checked={compact} onChange={() => useLogFieldVisibilityStore.getState().toggleCompact()} />
                        {tView('compact')}
                    </label>
                    <div className="flex flex-col gap-1">
                        {(['endpointType', 'channelName', 'actualModel', 'apiKeyName', 'clientIP', 'cost', 'tps', 'cacheHitRate', 'reasoningEffort', 'reasoningTokens'] as LogFieldName[]).map((field) => (
                            <label key={field} className="flex items-center gap-2 cursor-pointer rounded px-1.5 py-1 text-xs hover:bg-muted transition-colors">
                                <input
                                    type="checkbox"
                                    checked={visibility[field]}
                                    onChange={() => useLogFieldVisibilityStore.getState().toggleField(field)}
                                    className="size-3 rounded"
                                />
                                {tView(field)}
                            </label>
                        ))}
                    </div>
                    <button
                        type="button"
                        onClick={() => useLogFieldVisibilityStore.getState().resetFields()}
                        className="mt-2 w-full rounded-md border border-border/50 px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                    >
                        {tView('reset')}
                    </button>
                </PopoverContent>
            </Popover>
        </div>
    );
}

/**
 * 日志页面组件
 * - 初始加载 pageSize 条历史日志
 * - SSE 实时推送新日志
 * - 滚动自动加载更多
 * - 筛选（模型、渠道、密钥、端点类型、状态）
 */
export function Log() {
    const t = useTranslations('log');
    const [view, setView] = useState<'relay' | 'error'>('relay');
    const [filter, setFilter] = useState<LogFilter>(EMPTY_FILTER);
    const modelSearch = useLogModelSearchStore((s) => s.modelSearch);
    const setModelSearch = useLogModelSearchStore((s) => s.setModelSearch);
    const combinedFilter = useMemo<LogFilter>(
        () => (modelSearch ? { ...filter, model: modelSearch } : filter),
        [filter, modelSearch],
    );
    const { logs, liveRequests, isConnected, hasMore, isLoading, isLoadingMore, loadMore, refresh } = useLogs({ filter: combinedFilter });
    const { data: channels = [] } = useChannelList();

    // 自动刷新：按用户在设置中选择的间隔轮询日志列表（0 = 关闭）。
    // 页面不可见时暂停轮询，避免后台标签页空转。
    const autoRefreshInterval = useLogAutoRefreshStore((s) => s.interval);
    useEffect(() => {
        if (!autoRefreshInterval) return;
        const timer = window.setInterval(() => {
            if (document.visibilityState !== 'visible') return;
            void refresh();
        }, autoRefreshInterval * 1000);
        return () => window.clearInterval(timer);
    }, [autoRefreshInterval, refresh]);

    // 消费来自其它模块（分析/分组健康）的待处理筛选，实现"点击失败渠道 → 跳转日志并预填"。
    const pendingLogFilter = useNavHandoff((s) => s.pendingLogFilter);
    const consumePendingLogFilter = useNavHandoff((s) => s.consumePendingLogFilter);
    useEffect(() => {
        const pending = consumePendingLogFilter();
        if (pending) {
            const { model, ...rest } = pending;
            setFilter(rest);
            if (model) setModelSearch(model);
        }
    }, [pendingLogFilter, consumePendingLogFilter, setModelSearch]);

    const channelNameById = useMemo(() => {
        const map = new Map<number, string>();
        for (const item of channels) {
            map.set(item.raw.id, item.raw.name);
        }
        return map;
    }, [channels]);

    // 日志详情里的 channel_key_id → 「备注 / 脱敏 Key」。只用已加载的渠道列表建索引，
    // 不新增后端字段；映射不到时由 Item 回退显示 Key #id。
    const channelKeyLabelById = useMemo(() => {
        const map = new Map<number, string>();
        for (const item of channels) {
            for (const key of item.raw.keys ?? []) {
                if (typeof key.id !== 'number' || key.id <= 0) continue;
                const masked = maskChannelKeySecret(key.channel_key);
                const remark = key.remark?.trim();
                const label = remark && masked ? `${remark} / ${masked}` : (remark || masked);
                if (label) map.set(key.id, label);
            }
        }
        return map;
    }, [channels]);

    const canLoadMore = hasMore && !isLoading && !isLoadingMore && logs.length > 0;
    const handleReachEnd = useCallback(() => {
        if (!canLoadMore) return;
        void loadMore();
    }, [canLoadMore, loadMore]);

    const footer = useMemo(() => {
        if (hasMore && (isLoading || isLoadingMore)) {
            return (
                <div className="flex justify-center py-6">
                    <div className="flex items-center gap-2 rounded-full border border-border/50 bg-card/80 px-4 py-2 shadow-sm backdrop-blur">
                        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                        <span className="text-xs text-muted-foreground">{t('list.loadingMore')}</span>
                    </div>
                </div>
            );
        }
        if (!hasMore && logs.length > 0) {
            return (
                <div className="flex justify-center py-6">
                    <span className="text-xs text-muted-foreground/60">{t('list.noMore')}</span>
                </div>
            );
        }
        return null;
    }, [hasMore, isLoading, isLoadingMore, logs.length, t]);

    return (
        <div className="flex h-full min-h-0 flex-col gap-2 overflow-hidden">
            {/* 视图切换：转发日志 / 错误日志 */}
            <div className="flex shrink-0 items-center gap-1 rounded-lg border border-border/35 bg-card p-1">
                {(['relay', 'error'] as const).map((v) => (
                    <button
                        key={v}
                        type="button"
                        onClick={() => setView(v)}
                        aria-pressed={view === v}
                        className={cn(
                            'rounded-md px-3 py-1 text-xs transition-colors',
                            view === v
                                ? 'bg-foreground text-background'
                                : 'text-muted-foreground hover:text-foreground'
                        )}
                    >
                        {v === 'relay' ? t('view.relay') : t('view.error')}
                    </button>
                ))}
            </div>
            {view === 'error' ? (
                <ErrorLogView />
            ) : (
                <>
                    <LogFilterBar filter={filter} onChange={setFilter} />
                    <LiveRequests requests={liveRequests} connected={isConnected} />
                    {isLoading && logs.length === 0 ? (
                        <div className="flex min-h-[18rem] items-center justify-center rounded-xl border border-border/35 bg-card">
                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                        </div>
                    ) : logs.length === 0 ? (
                        <div className="flex min-h-[18rem] items-center justify-center rounded-xl border border-dashed border-border/35 bg-card px-6 py-6 text-center">
                            <p className="text-sm text-muted-foreground">{t('list.empty')}</p>
                        </div>
                    ) : (
                        <div className="min-h-0 flex-1">
                            <VirtualizedGrid
                                items={logs}
                                layout="list"
                                columns={{ default: 1 }}
                                estimateItemHeight={180}
                                overscan={8}
                                getItemKey={(log) => `log-${log.id}`}
                                renderItem={(log) => <LogCard log={log} channelNameById={channelNameById} channelKeyLabelById={channelKeyLabelById} />}
                                footer={footer}
                                onReachEnd={handleReachEnd}
                                reachEndEnabled={canLoadMore}
                                reachEndOffset={2}
                                bottomPaddingClassName="pb-16 md:pb-4"
                            />
                        </div>
                    )}
                </>
            )}
        </div>
    );
}
