'use client';

import { useTranslations } from 'next-intl';
import type { NonChatMetrics } from '@/api/nonchat';

export function NonChatStats({ metrics }: { metrics?: NonChatMetrics }) {
    const t = useTranslations('nonChat');
    if (!metrics) return null;
    const rows = [
        ['embeddingInputs', metrics.embedding_input_items, metrics.embedding_input_observed],
        ['vectors', metrics.embedding_vectors, metrics.embedding_vectors_observed],
        ['documents', metrics.rerank_documents, metrics.rerank_documents_observed],
        ['results', metrics.rerank_results, metrics.rerank_results_observed],
    ] as const;
    const observed = rows.filter(([, , coverage]) => (coverage ?? 0) > 0);
    const hasTokenEvidence = (metrics.usage_reported_requests ?? 0) + (metrics.usage_invalid_requests ?? 0) > 0;
    if (!observed.length && !hasTokenEvidence) return null;
    return <div className="mt-3 space-y-1.5 border-t border-border/40 pt-3 text-xs text-muted-foreground" data-testid="nonchat-stats">
        <div className="flex flex-wrap gap-x-4 gap-y-2">
            {observed.map(([label, value, coverage]) => <span key={label}>
                {t(label)} <strong className="font-medium text-foreground">{(value ?? 0).toLocaleString()}</strong>
                <span className="ml-1">{t('coverage', { count: coverage ?? 0 })}</span>
            </span>)}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1">
            {(metrics.unattributed_token ?? 0) > 0 && <span>{t('unattributed')}: {metrics.unattributed_token?.toLocaleString()}</span>}
            {(metrics.usage_reported_requests ?? 0) > 0 && <span>{t('reportedRequests', { count: metrics.usage_reported_requests ?? 0 })}</span>}
            {(metrics.usage_partial_requests ?? 0) > 0 && <span>{t('partialRequests', { count: metrics.usage_partial_requests ?? 0 })}</span>}
            {(metrics.usage_billed_requests ?? 0) > 0 && <span>{t('billedSubset', { count: metrics.usage_billed_requests ?? 0, tokens: (metrics.billed_token ?? 0).toLocaleString() })}</span>}
            {(metrics.usage_invalid_requests ?? 0) > 0 && <span className="text-amber-600 dark:text-amber-400">{t('invalidRequests', { count: metrics.usage_invalid_requests ?? 0 })}</span>}
        </div>
    </div>;
}
