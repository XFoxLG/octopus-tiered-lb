'use client';

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import type { LiveRequest } from '@/api/endpoints/log';

function formatBytes(byteCount: number): string {
    if (byteCount < 1024) return `${byteCount} B`;
    if (byteCount < 1024 * 1024) return `${(byteCount / 1024).toFixed(1)} KiB`;
    return `${(byteCount / (1024 * 1024)).toFixed(2)} MiB`;
}

const deliveryOutcomeKeys = new Set(['writer_accepted', 'partial', 'not_started', 'unknown']);

export function LiveRequests({ requests, connected }: { requests: LiveRequest[]; connected: boolean }) {
    const translate = useTranslations('log.live');
    const forensics = useTranslations('log.card.forensics');
    const [now, setNow] = useState(0);
    useEffect(() => {
        if (!requests.length) return;
        const timer = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(timer);
    }, [requests.length]);
    // SSE disconnects must not leave an old request looking live for minutes.
    // The next connection replaces this view with the server's authoritative
    // snapshot, so hiding the stale client-side copy is the honest state.
    if (!connected || !requests.length) return null;
    return (
        <section className="shrink-0 rounded-lg border border-border/50 bg-card p-3" aria-label={translate('title')}>
            <p className="text-sm font-medium">{translate('title')} · {requests.length}</p>
            <p className="mb-2 text-xs text-muted-foreground">{translate(connected ? 'scope' : 'disconnected')}</p>
            <ul className="max-h-48 space-y-2 overflow-y-auto" tabIndex={0} aria-label={translate('title')}>
                {requests.map(entry => (
                    <li key={entry.trace_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 break-all text-xs">
                        <span>{entry.request_model || '—'}</span>
                        <span>{entry.channel_name || '—'}{entry.actual_model ? ' → ' + entry.actual_model : ''}</span>
                        <span>{translate(entry.state)}</span>
                        {entry.attempt > 0 && <span>{translate('attemptCount', { count: entry.attempt })}</span>}
                        <span>{Math.max(0, Math.floor((((entry.state === 'completed' || entry.state === 'unavailable') ? entry.updated_at : (now || entry.updated_at)) - entry.started_at) / 1000))}s</span>
                        <span>
                            {forensics('writerBytes')}: {formatBytes(entry.client_write_bytes || 0)}
                        </span>
                        <span>
                            {forensics('axes.delivery')}: {entry.client_delivery_state
                                ? (deliveryOutcomeKeys.has(entry.client_delivery_state) ? forensics(`outcomes.${entry.client_delivery_state}`) : entry.client_delivery_state)
                                : forensics('outcomes.unknown')}
                        </span>
                        <span className="text-muted-foreground">{translate('instance', { id: entry.instance_id.slice(0, 8) })}</span>
                    </li>
                ))}
            </ul>
            <p className="mt-2 text-xs text-muted-foreground">{forensics('writerCaveat')}</p>
        </section>
    );
}
