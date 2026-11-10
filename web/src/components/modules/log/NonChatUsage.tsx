'use client';

import { useTranslations } from 'next-intl';
import { ArrowDown, ArrowUp, Cpu, Files, Gauge, ListFilter, ListOrdered, Ruler, Zap, type LucideIcon } from 'lucide-react';
import { nonChatRates, type NonChatCounters, type NonChatUsageEvidence, type NonChatUsageSummary } from '@/api/nonchat';

const countText = (value: number | null | undefined, missing: string) => value == null ? missing : value.toLocaleString();
const duration = (ms: number | null | undefined, missing: string) => ms == null ? missing : ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(2)}s`;

export function NonChatUsage({ summary, requestMS, evidence, details = false }: {
    summary?: NonChatUsageSummary;
    requestMS: number;
    evidence?: NonChatUsageEvidence;
    details?: boolean;
}) {
    const t = useTranslations('nonChat');
    if (!summary) return null;
    const missing = t('notReported');
    const counts = summary.counts;
    const rates = nonChatRates(summary, requestMS);
    const isEmbedding = counts.embedding_input_items != null || counts.embedding_vectors != null;
    const gap = summary.total_tokens == null ? 0 : summary.total_tokens - (summary.input_tokens ?? 0) - (summary.output_tokens ?? 0);
    const countRows = [
        ['embeddingInputs', counts.embedding_input_items, ArrowDown, 'text-emerald-600 dark:text-emerald-400'],
        ['vectors', counts.embedding_vectors, ArrowUp, 'text-violet-600 dark:text-violet-400'],
        ['dimensions', counts.embedding_dimensions, Ruler, 'text-cyan-700 dark:text-cyan-400'],
        ['documents', counts.rerank_documents, Files, 'text-emerald-600 dark:text-emerald-400'],
        ['topN', counts.rerank_top_n, ListFilter, 'text-sky-600 dark:text-sky-400'],
        ['results', counts.rerank_results, ListOrdered, 'text-violet-600 dark:text-violet-400'],
    ] as const;
    const metrics: Array<{ label: string; value: string; icon: LucideIcon; color: string; hint?: string }> = [
        ...countRows.filter(([, value]) => value != null).map(([label, value, icon, color]) => ({
            label: t(label), value: countText(value, missing), icon, color,
        })),
        ...(details || summary.first_byte_ms != null ? [{
            label: t('firstByte'), value: duration(summary.first_byte_ms, '—'),
            icon: Zap, color: 'text-amber-600 dark:text-amber-400',
            hint: summary.first_byte_ms == null ? t('notObserved') : t('timingHint'),
        }] : []),
        ...(details || summary.response_ms != null ? [{
            label: t('responseTime'), value: duration(summary.response_ms, '—'),
            icon: Cpu, color: 'text-blue-600 dark:text-blue-400',
            hint: summary.response_ms == null ? t('notObserved') : t('timingHint'),
        }] : []),
        ...(rates.itemsPerSecond != null ? [{
            label: t('throughput'), value: `${rates.itemsPerSecond.toFixed(2)} ${t(isEmbedding ? 'itemsPerSecond' : 'documentsPerSecond')}`,
            icon: Gauge, color: 'text-lime-700 dark:text-lime-400',
            hint: t('rateHint'),
        }] : []),
        ...(rates.inputTokensPerSecond != null ? [{
            label: t('inputTokensPerSecond'), value: rates.inputTokensPerSecond.toFixed(2), hint: t('rateHint'),
            icon: Gauge, color: 'text-teal-700 dark:text-teal-400',
        }] : []),
    ];
    const status = summary.completeness !== 'complete' ? t(`completeness.${summary.completeness}`) : null;
    const totalOnly = summary.total_tokens != null && summary.input_tokens == null && summary.output_tokens == null;
    const counters = (values: NonChatCounters) => <span>
        {t('input')}: {countText(values.input_tokens, missing)} · {t('output')}: {countText(values.output_tokens, missing)} · {t('total')}: {countText(values.total_tokens, missing)}
    </span>;
    return <div className={details ? 'space-y-2 text-xs' : 'flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-border/40 pt-2 text-xs'} data-testid="nonchat-usage">
        <dl className="flex flex-wrap gap-x-4 gap-y-2">
            {metrics.map(({ label, value, hint, icon: Icon, color }) => <div key={label} className="inline-flex items-center gap-1.5" title={hint}>
                <dt className="inline-flex items-center gap-1.5 text-muted-foreground"><Icon className={`size-3.5 shrink-0 ${color}`} aria-hidden="true" />{label}</dt>
                <dd className={`font-medium tabular-nums ${value === '—' ? 'text-muted-foreground' : color}`} aria-label={value === '—' ? hint : undefined}>{value}</dd>
            </div>)}
        </dl>
        {(status || totalOnly || gap > 0 || summary.basis === 'billed') && <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
            {summary.basis === 'billed' && <span className="rounded-md bg-muted px-1.5 py-0.5">{t('basis.billed')}</span>}
            {status && <span className={summary.completeness === 'invalid' ? 'text-amber-700 dark:text-amber-400' : ''}>{status}</span>}
            {totalOnly && <span>{t('totalOnly')}</span>}
            {gap > 0 && <span>{t('unattributed')}: <span className="tabular-nums">{gap.toLocaleString()}</span></span>}
        </div>}
        {details && <details>
            <summary className="cursor-pointer rounded-sm text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring">{t('sourceDetails')}</summary>
            <div className="mt-3 space-y-3 leading-relaxed text-muted-foreground">
                <div className="space-y-1.5">
                    <div className="flex flex-wrap items-baseline justify-between gap-2 text-muted-foreground">
                        <span>{t('selectedUsage')}</span>
                        {summary.basis !== 'missing' && <span>{t(`basis.${summary.basis}`)}</span>}
                    </div>
                    <p className="leading-relaxed tabular-nums">{counters(summary)}</p>
                </div>
                <p>{t('timingHint')}</p>
                <p>{t('rateHint')}</p>
                {evidence && <>
                    <p>{t('processingUsage')}: {counters(evidence.processing)}</p>
                    <p>{t('billedUsage')}: {counters(evidence.billed)}</p>
                    {evidence.units.search_units != null && <p>{t('searchUnits')}: {evidence.units.search_units.toLocaleString()} · {t('unitsHint')}</p>}
                    {(evidence.issues?.length ?? 0) > 0 && <ul className="space-y-1 break-words text-amber-700 dark:text-amber-400">
                        {evidence.issues?.map((issue, i) => <li key={i}><code>{issue.path}</code>: {issue.reason}</li>)}
                    </ul>}
                    <div className="max-h-52 space-y-1 overflow-y-auto break-words font-mono text-[11px]">
                        <p>{t('responseState')}: {evidence.response_state}</p>
                        {evidence.sources?.map((source, i) => <p key={i}>{source.path}: {source.state}{source.value != null ? ` (${source.value})` : ''}</p>)}
                    </div>
                </>}
            </div>
        </details>}
    </div>;
}
