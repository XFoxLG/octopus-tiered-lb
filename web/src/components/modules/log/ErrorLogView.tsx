'use client';

import { useCallback, useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { motion, AnimatePresence } from 'motion/react';
import { AlertTriangle, ChevronDown, ChevronUp, Loader2, Trash2 } from 'lucide-react';
import { useErrorLogs, useClearErrorLogs, type ErrorLog, type ErrorLogFilter } from '@/api/endpoints/error-log';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { formatUnixSeconds } from '@/lib/time';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Button } from '@/components/ui/button';

function formatTime(timestamp: number): string {
    return formatUnixSeconds(timestamp, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
    });
}

/** 单条错误日志卡片：折叠展示摘要，展开显示完整堆栈与上下文。 */
function ErrorLogCard({ entry }: { entry: ErrorLog }) {
    const t = useTranslations('log.errorLog');
    const [expanded, setExpanded] = useState(false);

    const isBackend = entry.source === 'backend';
    const contextLines = useMemo(() => {
        const lines: { label: string; value: string }[] = [];
        if (entry.request_method || entry.request_path) {
            lines.push({
                label: t('context.request'),
                value: `${entry.request_method || 'GET'} ${entry.request_path || ''}`,
            });
        }
        if (entry.page_url) lines.push({ label: t('context.pageUrl'), value: entry.page_url });
        if (entry.route_id) lines.push({ label: t('context.route'), value: entry.route_id });
        if (entry.client_ip) lines.push({ label: t('context.clientIp'), value: entry.client_ip });
        if (entry.version) lines.push({ label: t('context.version'), value: entry.version });
        return lines;
    }, [entry, t]);

    return (
        <div className="rounded-xl border border-border/35 bg-card p-4">
            <button
                type="button"
                className="flex w-full items-start gap-3 text-left"
                onClick={() => setExpanded((v) => !v)}
            >
                <div className={cn(
                    'mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg',
                    entry.level === 'panic' ? 'bg-red-500/15 text-red-500' : 'bg-amber-500/15 text-amber-500'
                )}>
                    <AlertTriangle className="size-4" />
                </div>
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                        <Badge variant={isBackend ? 'default' : 'secondary'} className="px-1.5 py-0 text-[10px] uppercase">
                            {isBackend ? 'backend' : 'frontend'}
                        </Badge>
                        <Badge variant="outline" className="px-1.5 py-0 text-[10px] uppercase">
                            {entry.level}
                        </Badge>
                        <span className="text-[11px] text-muted-foreground">{formatTime(entry.time)}</span>
                        {entry.route_id && <span className="text-[11px] text-muted-foreground/70">· {entry.route_id}</span>}
                        {expanded ? <ChevronUp className="ml-auto size-4 shrink-0 text-muted-foreground" /> : <ChevronDown className="ml-auto size-4 shrink-0 text-muted-foreground" />}
                    </div>
                    <p className="mt-1.5 break-words font-mono text-xs leading-5 text-foreground">{entry.message}</p>
                </div>
            </button>

            <AnimatePresence initial={false}>
                {expanded && (
                    <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.2 }}
                        className="overflow-hidden"
                    >
                        <div className="mt-3 space-y-2 border-t border-border/40 pt-3">
                            {contextLines.map((line) => (
                                <div key={line.label} className="flex flex-wrap items-baseline gap-2 text-xs">
                                    <span className="shrink-0 text-muted-foreground">{line.label}</span>
                                    <code className="break-all font-mono text-foreground">{line.value}</code>
                                </div>
                            ))}
                            {entry.stack && (
                                <pre className="max-h-80 overflow-auto rounded-lg border border-border/40 bg-background p-3 font-mono text-[11px] leading-5 text-foreground/90">
                                    {entry.stack}
                                </pre>
                            )}
                            {entry.user_agent && (
                                <p className="break-all text-[11px] text-muted-foreground/70">{entry.user_agent}</p>
                            )}
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
}

/**
 * 错误日志视图：前后端崩溃/错误的展示与筛选。
 */
export function ErrorLogView() {
    const t = useTranslations('log.errorLog');
    const [filter, setFilter] = useState<ErrorLogFilter>({});
    const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } = useErrorLogs({ filter });
    const clearLogs = useClearErrorLogs();

    const entries = useMemo(() => {
        const pages = data?.pages ?? [];
        return pages.flatMap((page) => page ?? []);
    }, [data]);

    const handleClear = useCallback(() => {
        if (window.confirm(t('clearConfirm'))) {
            clearLogs.mutate();
        }
    }, [clearLogs, t]);

    const loadMore = useCallback(() => {
        if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
    }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
            <div className="flex flex-nowrap items-center gap-2 overflow-x-auto py-1">
                <Select
                    value={filter.source ?? '__all__'}
                    onValueChange={(v) => {
                        const next = { ...filter };
                        if (v && v !== '__all__') {
                            next.source = v as 'backend' | 'frontend';
                        } else {
                            delete next.source;
                        }
                        setFilter(next);
                    }}
                >
                    <SelectTrigger size="sm" className="h-7 min-w-[8rem] text-xs">
                        <SelectValue placeholder={t('filter.allSources')} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="__all__">{t('filter.allSources')}</SelectItem>
                        <SelectItem value="backend">{t('filter.backend')}</SelectItem>
                        <SelectItem value="frontend">{t('filter.frontend')}</SelectItem>
                    </SelectContent>
                </Select>
                <Select
                    value={filter.level ?? '__all__'}
                    onValueChange={(v) => {
                        const next = { ...filter };
                        if (v && v !== '__all__') {
                            next.level = v;
                        } else {
                            delete next.level;
                        }
                        setFilter(next);
                    }}
                >
                    <SelectTrigger size="sm" className="h-7 min-w-[8rem] text-xs">
                        <SelectValue placeholder={t('filter.allLevels')} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="__all__">{t('filter.allLevels')}</SelectItem>
                        <SelectItem value="panic">panic</SelectItem>
                        <SelectItem value="error">error</SelectItem>
                        <SelectItem value="warning">warning</SelectItem>
                        <SelectItem value="unhandledrejection">unhandledrejection</SelectItem>
                        <SelectItem value="uncaught">uncaught</SelectItem>
                    </SelectContent>
                </Select>
                <Button
                    size="sm"
                    variant="ghost"
                    className="ml-auto h-7 gap-1 rounded-md px-2 text-xs text-muted-foreground hover:text-foreground"
                    onClick={handleClear}
                    disabled={clearLogs.isPending}
                >
                    {clearLogs.isPending ? <Loader2 className="size-3 animate-spin" /> : <Trash2 className="size-3" />}
                    {t('clear')}
                </Button>
            </div>

            {isLoading && entries.length === 0 ? (
                <div className="flex min-h-[18rem] items-center justify-center rounded-xl border border-border/35 bg-card">
                    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                </div>
            ) : entries.length === 0 ? (
                <div className="flex min-h-[18rem] items-center justify-center rounded-xl border border-dashed border-border/35 bg-card px-6 py-6 text-center">
                    <p className="text-sm text-muted-foreground">{t('empty')}</p>
                </div>
            ) : (
                <div className="min-h-0 flex-1 space-y-2 overflow-y-auto pb-4">
                    {entries.map((entry) => (
                        <ErrorLogCard key={entry.id} entry={entry} />
                    ))}
                    {hasNextPage && (
                        <div className="flex justify-center py-3">
                            <Button size="sm" variant="outline" className="text-xs" onClick={loadMore} disabled={isFetchingNextPage}>
                                {isFetchingNextPage ? (
                                    <Loader2 className="mr-1 size-3 animate-spin" />
                                ) : null}
                                {t('loadMore')}
                            </Button>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
