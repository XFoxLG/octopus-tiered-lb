/** Optional fields preserve the distinction between historical unknowns and observed zero. */
export interface NonChatMetrics {
    unattributed_token?: number;
    billed_token?: number;
    usage_reported_requests?: number;
    usage_partial_requests?: number;
    usage_billed_requests?: number;
    usage_invalid_requests?: number;
    embedding_input_items?: number;
    embedding_input_observed?: number;
    embedding_vectors?: number;
    embedding_vectors_observed?: number;
    rerank_documents?: number;
    rerank_documents_observed?: number;
    rerank_results?: number;
    rerank_results_observed?: number;
}

export interface NonChatCounters {
    input_tokens?: number | null;
    output_tokens?: number | null;
    total_tokens?: number | null;
}

export interface NonChatUsageSummary extends NonChatCounters {
    basis: 'processing' | 'billed' | 'missing';
    completeness: 'complete' | 'partial' | 'invalid' | 'missing';
    counts: {
        embedding_input_items?: number | null;
        embedding_vectors?: number | null;
        embedding_dimensions?: number | null;
        rerank_documents?: number | null;
        rerank_top_n?: number | null;
        rerank_results?: number | null;
    };
    first_byte_ms?: number | null;
    response_ms?: number | null;
}

export interface NonChatUsageEvidence {
    processing: NonChatCounters;
    billed: NonChatCounters;
    units: { search_units?: number | null };
    sources?: Array<{ path: string; state: string; value?: number }>;
    issues?: Array<{ path: string; reason: string }>;
    response_state: string;
}

export function totalAccountedTokens(metrics: { input_token?: number; output_token?: number; unattributed_token?: number } | undefined): number {
    return (metrics?.input_token ?? 0) + (metrics?.output_token ?? 0) + (metrics?.unattributed_token ?? 0);
}

/** Wall-clock throughput includes queueing, network and retries; it is not model compute speed. */
export function nonChatRates(summary: NonChatUsageSummary, requestMS: number) {
    const seconds = requestMS > 0 ? requestMS / 1000 : null;
    const count = summary.counts.embedding_input_items ?? summary.counts.rerank_documents;
    return {
        itemsPerSecond: seconds !== null && count != null ? count / seconds : null,
        inputTokensPerSecond: seconds !== null && summary.input_tokens != null && summary.basis === 'processing'
            ? summary.input_tokens / seconds : null,
    };
}
