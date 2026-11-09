'use client';

import { useTranslations } from 'next-intl';
import { useStatsDaily } from '@/api/endpoints/stats';
import { NonChatStats } from '@/components/common/NonChatStats';
import { useHomeStatsRefreshMs } from './store';

export function NonChatDaily() {
    const t = useTranslations('nonChat');
    const refreshMS = useHomeStatsRefreshMs();
    const { data } = useStatsDaily({ refetchIntervalMs: refreshMS });
    const rows = data?.filter(({ non_chat: m }) => m && (
        (m.embedding_input_observed ?? 0) + (m.rerank_documents_observed ?? 0) + (m.usage_reported_requests ?? 0) + (m.usage_invalid_requests ?? 0) > 0
    )).sort((a, b) => b.date.localeCompare(a.date));
    if (!rows?.length) return null;
    return <details className="rounded-xl border border-border bg-card p-4">
        <summary className="cursor-pointer text-sm font-medium">{t('daily')}</summary>
        <p className="mt-2 text-xs text-muted-foreground">{t('coverageHint')}</p>
        <div className="mt-2 max-h-80 space-y-3 overflow-y-auto">
            {rows.map(row => <div key={row.date} className="rounded-lg border border-border/40 p-3">
                <p className="text-xs font-medium">{row.date}</p><NonChatStats metrics={row.non_chat} />
            </div>)}
        </div>
    </details>;
}
