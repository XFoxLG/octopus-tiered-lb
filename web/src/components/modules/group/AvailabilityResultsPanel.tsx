'use client';

import { AlertTriangle, CheckCircle2, Clock3, Loader2 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { GroupTestResult } from '@/api/endpoints/group';
import { Progress } from '@/components/ui/progress';
import { cn } from '@/lib/utils';
import type { SelectedMember } from './ItemList';
import { buildAvailabilityRows } from './availability-results';

type AvailabilityResultsPanelProps = {
    isTesting: boolean;
    members: SelectedMember[];
    completed: number;
    total: number;
    progressValue: number;
    results: GroupTestResult[];
    error?: string;
    summary?: {
        unavailableCount: number;
        availableCount: number;
        allAvailable: boolean;
        fullyMatched: boolean;
    };
};

export function AvailabilityResultsPanel({
    isTesting, members, completed, total, progressValue, results, summary, error,
}: AvailabilityResultsPanelProps) {
    const t = useTranslations('group');
    const rows = buildAvailabilityRows(members, results, isTesting);
    const passedCount = results.filter((result) => result.passed).length;
    const failedCount = results.length - passedCount;

    return (
        <section aria-label={t('detail.availability.title')} className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
            <div className="shrink-0 space-y-3 border-b border-border pb-4">
                <div className="flex flex-wrap items-start justify-between gap-2">
                    <h3 className="text-sm font-semibold">{t('detail.availability.title')}</h3>
                    <span role="status" className="text-xs text-muted-foreground">
                        {t('card.testProgressCount', { completed, total })}
                    </span>
                </div>
                <Progress value={progressValue} className="h-1.5" />
                <div className="flex flex-wrap items-center gap-3 text-xs">
                    <span className="text-emerald-600 dark:text-emerald-400">{t('detail.availability.passedCount', { count: passedCount })}</span>
                    <span className="text-destructive">{t('detail.availability.failedCount', { count: failedCount })}</span>
                    {summary?.fullyMatched ? (
                        <span className="text-muted-foreground">{summary.allAvailable ? t('toast.testAllPassed') : t('toast.testPartialFailed')}</span>
                    ) : null}
                </div>
                {error ? <p role="alert" className="break-all text-xs text-destructive">{error}</p> : null}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain py-4 pr-1">
                {rows.length ? (
                    <div className="grid content-start items-start gap-3 lg:grid-cols-2">
                        {rows.map(({ id, modelName, channelName, status, result }) => (
                            <article key={id} className={cn(
                                'min-w-0 rounded-lg border p-3 sm:p-4',
                                status === 'failed' ? 'border-destructive/25 bg-destructive/5' : 'border-border bg-card',
                            )}>
                                <div className="flex min-w-0 items-start gap-3">
                                    {status === 'passed' ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
                                        : status === 'failed' ? <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
                                            : status === 'testing' ? <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin text-muted-foreground" />
                                                : <Clock3 className="mt-0.5 size-4 shrink-0 text-muted-foreground" />}
                                    <div className="min-w-0 flex-1">
                                        <div className="flex flex-wrap items-start justify-between gap-2">
                                            <span className="min-w-0 break-all text-sm font-medium">{modelName}</span>
                                            <span className={cn('shrink-0 text-xs', status === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>
                                                {status === 'passed' ? t('detail.availability.resultPassed')
                                                    : status === 'failed' ? t('detail.availability.resultFailed')
                                                        : status === 'testing' ? t('detail.availability.testing') : t('card.testPending')}
                                            </span>
                                        </div>
                                        <p className="mt-1 break-all text-xs text-muted-foreground">{channelName}</p>
                                        {result ? (
                                            <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                                                {result.status_code > 0 ? <span>HTTP {result.status_code}</span> : null}
                                                <span>{t('detail.availability.resultAttempts', { count: result.attempts })}</span>
                                            </div>
                                        ) : null}
                                    </div>
                                </div>
                                {result && (result.message || (!result.passed && result.response_text)) ? (
                                    <details className="mt-3 border-t border-border/50 pt-2 text-xs">
                                        <summary className="min-h-8 cursor-pointer content-center rounded-sm text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                            {t('detail.availability.toggleDetails')}
                                        </summary>
                                        <div className="mt-2 max-h-52 overflow-y-auto overscroll-contain rounded-md bg-muted/40 p-3 leading-5">
                                            {result.message ? <p className="whitespace-pre-wrap break-all">{result.message}</p> : null}
                                            {!result.passed && result.response_text ? <pre className="mt-2 whitespace-pre-wrap break-all font-mono">{result.response_text}</pre> : null}
                                        </div>
                                    </details>
                                ) : null}
                            </article>
                        ))}
                    </div>
                ) : (
                    <div className="flex min-h-32 items-center justify-center text-sm text-muted-foreground">
                        {isTesting ? t('detail.availability.waitingResults') : t('detail.availability.noResults')}
                    </div>
                )}
            </div>
        </section>
    );
}
