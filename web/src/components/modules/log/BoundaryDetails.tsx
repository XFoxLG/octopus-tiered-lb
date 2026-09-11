'use client';

import { useMemo } from 'react';
import { useTranslations } from 'next-intl';
import { resolveClientName } from './ua';
import { AlertTriangle, Download, Loader2 } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
    type RelayLogContentRef,
    type RelayLogContentState,
    type RelayLogDetail,
    useDownloadRelayLogContent,
} from '@/api/endpoints/log';
import { cn } from '@/lib/utils';

interface BoundaryDetailsProps {
    detail: RelayLogDetail | null;
    isLoading: boolean;
}

const boundaryOrder = ['client_ingress', 'upstream_request', 'upstream_response', 'client_egress'] as const;

function formatBytes(byteCount: number): string {
    if (byteCount < 1024) return `${byteCount} B`;
    if (byteCount < 1024 * 1024) return `${(byteCount / 1024).toFixed(1)} KiB`;
    return `${(byteCount / (1024 * 1024)).toFixed(2)} MiB`;
}

function formatContentText(content: RelayLogContentRef): string | undefined {
    if (!content.text) return undefined;
    try {
        return JSON.stringify(JSON.parse(content.text), null, 2);
    } catch {
        return content.text;
    }
}

function stateClassName(state: RelayLogContentState): string {
    switch (state) {
        case 'ready':
            return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300';
        case 'pending':
            return 'border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300';
        case 'expired':
            return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300';
        case 'disabled':
            return 'border-border bg-muted text-muted-foreground';
        case 'unavailable':
        default:
            return 'border-destructive/30 bg-destructive/10 text-destructive';
    }
}

function ContentRecord({ content }: { content: RelayLogContentRef }) {
    const t = useTranslations('log.card.forensics');
    const downloadContent = useDownloadRelayLogContent();
    const displayText = useMemo(() => formatContentText(content), [content]);
    const canDownload = content.state === 'ready' && content.id > 0;

    return (
        <div className="space-y-2 rounded-xl border border-border/50 bg-card/70 p-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
                <Badge variant="outline" className={stateClassName(content.state)}>
                    {t(`states.${content.state}`)}
                </Badge>
                <Badge variant="secondary">{t(`kinds.${content.kind}`)}</Badge>
                {content.protocol ? <Badge variant="outline">{content.protocol}</Badge> : null}
                {content.http_status ? <Badge variant="outline">HTTP {content.http_status}</Badge> : null}
                <span className="text-muted-foreground">{formatBytes(content.captured_bytes || 0)}</span>
                {!content.complete ? (
                    <Badge variant="outline" className="border-amber-500/30 text-amber-700 dark:text-amber-300">
                        {t('partial')}
                    </Badge>
                ) : null}
                {(content.file_name || content.field_name) ? (
                    <span className="min-w-0 truncate text-muted-foreground" title={content.file_name || content.field_name}>
                        {content.file_name || content.field_name}
                    </span>
                ) : null}
                {canDownload ? (
                    <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="ml-auto h-7 rounded-lg px-2"
                        onClick={() => downloadContent.mutate(content)}
                        disabled={downloadContent.isPending}
                        aria-label={t('downloadAria', { name: content.file_name || content.kind })}
                    >
                        {downloadContent.isPending ? <Loader2 className="mr-1 size-3.5 animate-spin" /> : <Download className="mr-1 size-3.5" />}
                        {t('download')}
                    </Button>
                ) : null}
            </div>
            {displayText ? (
                <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-muted/50 p-3 font-mono text-xs leading-relaxed text-foreground">
                    {displayText}
                </pre>
            ) : null}
            {content.error ? (
                <div className="flex items-start gap-2 text-xs text-muted-foreground">
                    <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
                    <span>{content.error}</span>
                </div>
            ) : null}
        </div>
    );
}

function BoundarySection({ title, records, emptyText }: { title: string; records: RelayLogContentRef[]; emptyText: string }) {
    return (
        <section className="space-y-2 rounded-2xl border border-border/60 bg-muted/20 p-3">
            <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-semibold text-card-foreground">{title}</h3>
                <Badge variant="secondary" className="text-[10px]">{records.length}</Badge>
            </div>
            {records.length > 0 ? records.map((record) => <ContentRecord key={`${record.id}-${record.kind}-${record.slot}`} content={record} />) : (
                <p className="rounded-xl border border-dashed border-border/60 p-3 text-xs text-muted-foreground">{emptyText}</p>
            )}
        </section>
    );
}

function StatusAxis({ label, value }: { label: string; value?: string }) {
    const t = useTranslations('log.card.forensics');
    return (
        <div className="min-w-0 rounded-xl border border-border/50 bg-muted/30 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</div>
            <div className="truncate text-xs font-semibold text-card-foreground" title={value || 'unknown'}>
                {t(`outcomes.${value || 'unknown'}`)}
            </div>
        </div>
    );
}

export function BoundaryDetails({ detail, isLoading }: BoundaryDetailsProps) {
    const t = useTranslations('log.card.forensics');
    if (isLoading) {
        return (
            <div className="flex min-h-48 flex-1 items-center justify-center">
                <Loader2 className="size-5 animate-spin text-muted-foreground" />
            </div>
        );
    }
    if (!detail) {
        return <p className="p-4 text-sm text-muted-foreground">{t('detailUnavailable')}</p>;
    }

    const contents = [...(detail.contents ?? [])].sort((left, right) => {
        const boundaryDifference = boundaryOrder.indexOf(left.boundary) - boundaryOrder.indexOf(right.boundary);
        if (boundaryDifference !== 0) return boundaryDifference;
        if (left.attempt_num !== right.attempt_num) return left.attempt_num - right.attempt_num;
        return left.slot - right.slot;
    });
    const recordsFor = (boundary: RelayLogContentRef['boundary'], attemptNumber?: number) => contents.filter((content) => (
        content.boundary === boundary && (attemptNumber === undefined || content.attempt_num === attemptNumber)
    ));
    const forwardedAttempts = (detail.attempts ?? []).filter((attempt) => (
        attempt.send_started || attempt.status === 'success' || attempt.status === 'failed'
    ));
    const clientProtocol = recordsFor('client_ingress')[0]?.protocol || recordsFor('client_egress')[0]?.protocol || detail.endpoint_type || '-';
    const upstreamProtocols = Array.from(new Set(contents
        .filter((content) => content.boundary === 'upstream_request' || content.boundary === 'upstream_response')
        .map((content) => content.protocol)
        .filter(Boolean)));
    const hasBoundaryRecords = contents.length > 0;

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden pb-1">
            <div className="grid shrink-0 grid-cols-2 gap-2 md:grid-cols-5">
                <StatusAxis label={t('axes.generation')} value={detail.generation_outcome} />
                <StatusAxis label={t('axes.upstream')} value={detail.upstream_outcome} />
                <StatusAxis label={t('axes.content')} value={detail.content_state} />
                <StatusAxis label={t('axes.persistence')} value={detail.persistence_state} />
                <StatusAxis label={t('axes.delivery')} value={detail.client_delivery_state} />
            </div>

            <div className="flex shrink-0 flex-wrap gap-x-4 gap-y-1 rounded-xl border border-border/50 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                <span>{t('traceId')}: <span className="font-mono text-foreground">{detail.trace_id || '-'}</span></span>
                <span>{t('protocolA')}: <span className="text-foreground">{clientProtocol}</span></span>
                <span>{t('protocolB')}: <span className="text-foreground">{upstreamProtocols.join(', ') || t('none')}</span></span>
                <span>HTTP: <span className="text-foreground">{detail.http_status || '-'}</span></span>
                <span>{t('writerBytes')}: <span className="text-foreground">{formatBytes(detail.client_write_bytes || 0)}</span></span>
                {detail.termination_cause ? <span>{t('termination')}: <span className="text-foreground">{detail.termination_cause}</span></span> : null}
                {detail.provider_termination_reason ? <span>{t('providerTermination')}: <span className="text-foreground">{detail.provider_termination_reason}</span></span> : null}
            </div>

            {detail.user_agent ? (
                <div className="shrink-0 rounded-xl border border-border/50 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                    <span className="mr-1">{t('userAgent')}:</span>
                    {resolveClientName(detail.user_agent) ? (
                        <span className="mr-1 font-semibold text-foreground">{resolveClientName(detail.user_agent)}</span>
                    ) : null}
                    <span className="break-all">{detail.user_agent}</span>
                </div>
            ) : null}

            {detail.client_delivery_state === 'writer_accepted' ? (
                <p className="shrink-0 rounded-xl border border-blue-500/20 bg-blue-500/5 px-3 py-2 text-xs text-muted-foreground">
                    {t('writerCaveat')}
                </p>
            ) : null}

            <div className="min-h-0 flex-1 space-y-3 overflow-auto pr-1">
                {hasBoundaryRecords ? (
                    <>
                        <BoundarySection
                            title={t('boundaries.clientIngress')}
                            records={recordsFor('client_ingress')}
                            emptyText={t('boundaryMissing')}
                        />
                        {forwardedAttempts.length === 0 ? (
                            <section className="rounded-2xl border border-dashed border-border/60 p-4 text-sm text-muted-foreground">
                                {t('noUpstreamAttempt')}
                            </section>
                        ) : forwardedAttempts.map((attempt) => (
                            <section key={attempt.attempt_num} className="space-y-3 rounded-2xl border border-border/60 bg-muted/10 p-3">
                                <div className="flex flex-wrap items-center gap-2">
                                    <h3 className="text-sm font-semibold">{t('attempt', { number: attempt.attempt_num })}</h3>
                                    <Badge variant="secondary">{attempt.channel_name || `#${attempt.channel_id}`}</Badge>
                                    {attempt.adapter_type ? <Badge variant="outline">{attempt.adapter_type}</Badge> : null}
                                    {attempt.http_status ? <Badge variant="outline">HTTP {attempt.http_status}</Badge> : null}
                                    {attempt.request_prepared ? <Badge variant="outline">{t('phases.prepared')}</Badge> : null}
                                    {attempt.send_started ? <Badge variant="outline">{t('phases.sendStarted')}</Badge> : null}
                                    {attempt.response_received ? <Badge variant="outline">{t('phases.responseReceived')}</Badge> : null}
                                    {attempt.send_started && !attempt.request_complete ? <Badge variant="outline" className="border-amber-500/30 text-amber-700 dark:text-amber-300">{t('phases.requestPartial')}</Badge> : null}
                                    {attempt.response_received && !attempt.response_complete ? <Badge variant="outline" className="border-amber-500/30 text-amber-700 dark:text-amber-300">{t('phases.responsePartial')}</Badge> : null}
                                    <span className="text-xs text-muted-foreground">
                                        {formatBytes(attempt.request_bytes || 0)} / {formatBytes(attempt.response_bytes || 0)}
                                    </span>
                                </div>
                                <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
                                    <BoundarySection
                                        title={t('boundaries.upstreamRequest')}
                                        records={recordsFor('upstream_request', attempt.attempt_num)}
                                        emptyText={t('upstreamContentMissing')}
                                    />
                                    <BoundarySection
                                        title={t('boundaries.upstreamResponse')}
                                        records={recordsFor('upstream_response', attempt.attempt_num)}
                                        emptyText={t('upstreamContentMissing')}
                                    />
                                </div>
                            </section>
                        ))}
                        <BoundarySection
                            title={t('boundaries.clientEgress')}
                            records={recordsFor('client_egress')}
                            emptyText={t('boundaryMissing')}
                        />
                    </>
                ) : (
                    <div className="grid min-h-0 grid-cols-1 gap-3 md:grid-cols-2">
                        <BoundarySection
                            title={t('legacyRequest')}
                            records={detail.request_content ? [{
                                id: 0,
                                relay_log_id: detail.id,
                                attempt_num: 0,
                                boundary: 'client_ingress',
                                slot: 0,
                                kind: 'body',
                                state: 'ready',
                                complete: true,
                                captured_bytes: new TextEncoder().encode(detail.request_content).length,
                                created_at: detail.time,
                                text: detail.request_content,
                            }] : []}
                            emptyText={t('boundaryMissing')}
                        />
                        <BoundarySection
                            title={t('legacyResponse')}
                            records={detail.response_content ? [{
                                id: 0,
                                relay_log_id: detail.id,
                                attempt_num: 0,
                                boundary: 'client_egress',
                                slot: 0,
                                kind: 'body',
                                state: 'ready',
                                complete: true,
                                captured_bytes: new TextEncoder().encode(detail.response_content).length,
                                created_at: detail.time,
                                text: detail.response_content,
                            }] : []}
                            emptyText={t('boundaryMissing')}
                        />
                    </div>
                )}
                {(detail.client_write_error || detail.content_unavailable_error) ? (
                    <div className={cn('rounded-xl border p-3 text-xs', stateClassName('unavailable'))}>
                        {detail.client_write_error || detail.content_unavailable_error}
                    </div>
                ) : null}
            </div>
        </div>
    );
}
