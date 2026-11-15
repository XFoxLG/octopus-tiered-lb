'use client';

import { useTranslations } from 'next-intl';
import { Clock, Network, Route, Timer } from 'lucide-react';
import type { RelayLogDetail, RelayNetworkDiagnostics, RelayTaskDiagnostics } from '@/api/endpoints/log';

const phases = ['preparation_at', 'dispatch_at', 'dns_start_at', 'dns_end_at', 'dial_start_at', 'dial_end_at', 'tls_start_at', 'tls_end_at', 'header_first_byte_at', 'response_headers_at', 'first_body_byte_at', 'first_reasoning_at', 'first_effective_output_at', 'terminal_at', 'cancelled_at'] as const;

function TaskStatus({ task }: { task: RelayTaskDiagnostics }) {
    const t = useTranslations('log.card.forensics.transport');
    const metrics = [
        [t('status'), task.status ? t(`statuses.${task.status}`) : t('submissionStates.' + (task.submission_state || 'not_started'))],
        [t('cursor'), task.cursor ?? 0],
        [t('polls'), task.poll_count ?? 0],
        [t('retries'), task.query_retry_count ?? 0],
        [t('cancel'), t('cancelStates.' + (task.cancel_state || 'not_requested'))],
        ...(task.deadline_at ? [[t('deadline'), new Date(task.deadline_at).toLocaleString()]] : []),
    ];
    return <div className="space-y-2">
        <div className="flex items-center gap-2 text-sm font-medium text-violet-600 dark:text-violet-300"><Route className="size-4" aria-hidden="true" />Catie Tasks</div>
        <dl className="flex flex-wrap gap-x-5 gap-y-2 text-xs">{metrics.map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{label}</dt><dd className="break-words font-medium text-foreground">{value}</dd></div>)}</dl>
        <p className="text-xs text-muted-foreground">{t('taskHint')}</p>
    </div>;
}

function NetworkTimes({ network }: { network: RelayNetworkDiagnostics }) {
    const t = useTranslations('log.card.forensics.transport');
    const origin = network.preparation_at;
    return <div className="space-y-2">
        <div className="flex items-center gap-2 text-sm font-medium text-sky-600 dark:text-sky-300"><Network className="size-4" aria-hidden="true" />{t(network.timing_source === 'task_submission' ? 'submissionNetwork' : 'network')}</div>
        <dl className="grid min-w-0 grid-cols-2 gap-x-4 gap-y-2 text-xs sm:grid-cols-3">{phases.filter((phase) => network[phase] != null && network[phase] !== 0).map((phase) => {
            const timestamp = network[phase]!;
            return <div key={phase} className="min-w-0"><dt className="flex items-center gap-1 text-muted-foreground"><Clock className="size-3 shrink-0" aria-hidden="true" />{t('phases.' + phase)}</dt><dd className="font-medium tabular-nums text-sky-600 dark:text-sky-300" title={new Date(timestamp).toLocaleString()}>{origin ? `${((timestamp - origin) / 1000).toFixed(3)}s` : new Date(timestamp).toLocaleTimeString()}</dd></div>;
        })}</dl>
        {network.connection_reused != null && <p className="text-xs text-muted-foreground">{t(network.connection_reused ? 'connectionReused' : 'newConnection')}</p>}
        <p className="text-xs text-muted-foreground">{t('networkHint')}</p>
        {(network.cf_ray || network.rndr_id || network.request_id) && <p className="break-all text-xs text-muted-foreground">{network.request_id && <span className="mr-3">Request ID: {network.request_id}</span>}{network.cf_ray && <span className="mr-3">CF-Ray: {network.cf_ray}</span>}{network.rndr_id && <span>Rndr-Id: {network.rndr_id}</span>}</p>}
    </div>;
}

export function TransportDetails({ diagnostics }: { diagnostics: RelayLogDetail['diagnostics'] }) {
    const t = useTranslations('log.card.forensics.transport');
    if (!diagnostics?.network && !diagnostics?.task && !diagnostics?.attempts?.some((attempt) => attempt.network || attempt.task)) return null;
    return <section className="min-w-0 space-y-3 rounded-xl border border-border/50 bg-muted/20 p-3">
        <div className="flex items-center gap-2 text-sm font-semibold"><Timer className="size-4 text-sky-500" aria-hidden="true" />{t('title')}</div>
        {diagnostics.task && <TaskStatus task={diagnostics.task} />}
        {diagnostics.network && <NetworkTimes network={diagnostics.network} />}
        {diagnostics.first_write_at ? <p className="text-xs text-emerald-600 dark:text-emerald-300">{t('firstWrite')}: {new Date(diagnostics.first_write_at).toLocaleString()}</p> : null}
        {diagnostics.attempts?.filter((attempt) => attempt.network || attempt.task).map((attempt) => <details key={attempt.attempt} className="min-w-0 border-t border-border/40 pt-2">
            <summary className="cursor-pointer break-words text-xs text-muted-foreground">{t('attempt', {number:attempt.attempt})}{attempt.attempt === diagnostics.selected_attempt ? ` (${t('selected')})` : ''} · {attempt.protocol} · {attempt.model} · {t('key')}: {attempt.key_id ?? '—'}{attempt.endpoint_id ? ` · ${attempt.endpoint_id}` : ''}{attempt.hedge_lane ? ` · ${attempt.hedge_lane}` : ''}</summary>
            <div className="mt-3 space-y-3">{attempt.task && <TaskStatus task={attempt.task} />}{attempt.network && <NetworkTimes network={attempt.network} />}</div>
        </details>)}
    </section>;
}
