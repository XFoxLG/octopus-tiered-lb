'use client';

import { useTranslations } from 'next-intl';
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
        ['embeddingInputs', counts.embedding_input_items], ['vectors', counts.embedding_vectors],
        ['dimensions', counts.embedding_dimensions], ['documents', counts.rerank_documents],
        ['topN', counts.rerank_top_n], ['results', counts.rerank_results],
    ] as const;
    const counters = (values: NonChatCounters) => <span>
        {t('input')}: {countText(values.input_tokens, missing)} · {t('output')}: {countText(values.output_tokens, missing)} · {t('total')}: {countText(values.total_tokens, missing)}
    </span>;
    return <div className={details ? 'space-y-3 rounded-xl border border-border/50 p-3 text-xs' : 'space-y-1.5 text-xs text-muted-foreground'} data-testid="nonchat-usage">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
            <span className="rounded border border-border/60 px-1.5 py-0.5">{t(`basis.${summary.basis}`)}</span>
            {summary.completeness !== 'complete' && <span className={summary.completeness === 'invalid' ? 'text-amber-600 dark:text-amber-400' : ''}>{t(`completeness.${summary.completeness}`)}</span>}
            {summary.total_tokens != null && summary.input_tokens == null && summary.output_tokens == null && <span>{t('totalOnly')}</span>}
            {gap > 0 && <span>{t('unattributed')}: {gap.toLocaleString()}</span>}
            {countRows.filter(([, value]) => value != null).map(([label, value]) => <span key={label}>{t(label)}: {countText(value, missing)}</span>)}
        </div>
        <div className="flex flex-wrap gap-x-3 gap-y-1.5" title={t('timingHint')}>
            <span>{t('firstByte')}: {duration(summary.first_byte_ms, t('notObserved'))}</span>
            <span>{t('responseTime')}: {duration(summary.response_ms, t('notObserved'))}</span>
            {rates.itemsPerSecond != null && <span title={t('rateHint')}>{t(isEmbedding ? 'itemsPerSecond' : 'documentsPerSecond')}: {rates.itemsPerSecond.toFixed(2)}</span>}
            {details && rates.inputTokensPerSecond != null && <span title={t('rateHint')}>{t('inputTokensPerSecond')}: {rates.inputTokensPerSecond.toFixed(2)}</span>}
        </div>
        {details && <>
            <p className="text-muted-foreground">{t('timingHint')} {t('rateHint')}</p>
            <p>{t('selectedUsage')}: {counters(summary)}</p>
            {evidence && <>
                <p>{t('processingUsage')}: {counters(evidence.processing)}</p>
                <p>{t('billedUsage')}: {counters(evidence.billed)}</p>
                {evidence.units.search_units != null && <p>{t('searchUnits')}: {evidence.units.search_units.toLocaleString()} · {t('unitsHint')}</p>}
                {(evidence.issues?.length ?? 0) > 0 && <ul className="space-y-1 break-words text-amber-600 dark:text-amber-400">
                    {evidence.issues?.map((issue, i) => <li key={i}><code>{issue.path}</code>: {issue.reason}</li>)}
                </ul>}
                <details className="rounded-lg border border-border/40 p-2">
                    <summary className="cursor-pointer">{t('sourceDetails')}</summary>
                    <div className="mt-2 max-h-52 space-y-1 overflow-y-auto break-words font-mono text-[11px]">
                        <p>{t('responseState')}: {evidence.response_state}</p>
                        {evidence.sources?.map((source, i) => <p key={i}>{source.path}: {source.state}{source.value != null ? ` (${source.value})` : ''}</p>)}
                    </div>
                </details>
            </>}
        </>}
    </div>;
}
