'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { AlertTriangle, CheckCircle2, Loader2, MinusCircle, Play, ShieldAlert, XCircle } from 'lucide-react';
import {
    useApplyChannelProbe,
    useChannelProbe,
    type Channel,
    type ChannelProbeResult,
    type ChannelProbeRun,
    type ProbeVerdict,
} from '@/api/endpoints/channel';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { toast } from '@/components/common/Toast';
import { cn } from '@/lib/utils';

/** 判定 → 图标与配色。三态之外还有 unknown（证据不足，不写入）。 */
function verdictPresentation(verdict: ProbeVerdict) {
    switch (verdict) {
        case 'pass':
            return { Icon: CheckCircle2, className: 'text-emerald-500' };
        case 'fail':
            return { Icon: XCircle, className: 'text-destructive' };
        case 'unsupported':
            return { Icon: MinusCircle, className: 'text-muted-foreground' };
        default:
            return { Icon: AlertTriangle, className: 'text-amber-500' };
    }
}

function formatLatency(latencyMs?: number) {
    if (!latencyMs || latencyMs <= 0) return null;
    return `${latencyMs}ms`;
}

export type ProbeDialogProps = {
    channel: Channel;
    availableModels: string[];
    onClose: () => void;
};

/**
 * 渠道能力探测弹窗：一个滚动日志框里依次展示协议层与能力层的逐行结果，
 * 底部提供「应用 / 关闭」。与 Sub2API 的测试连接弹窗同形。
 *
 * 刻意不做自动应用、不做定时探测 —— 多数公益站禁止测活，部分还有测活关键词
 * 拦截，所以探测必须是"用户点了才开始"，且结果要不要写回配置由用户决定。
 */
export function ProbeDialog({ channel, availableModels, onClose }: ProbeDialogProps) {
    const t = useTranslations('channel.probe');
    const [modelName, setModelName] = useState(availableModels[0] ?? '');
    const [allowSkipModelTest, setAllowSkipModelTest] = useState(false);
    const [run, setRun] = useState<ChannelProbeRun | null>(null);
    const [needsSkipConfirmation, setNeedsSkipConfirmation] = useState(false);
    const probeController = useRef<AbortController | null>(null);
    const [cancelled, setCancelled] = useState(false);
    useEffect(() => () => probeController.current?.abort(), []);

    const probe = useChannelProbe();
    const apply = useApplyChannelProbe();

    // results 必须先 memo 再派生：直接写 `run?.results ?? []` 会让空数组
    // 每次渲染都是新引用，下面两个 useMemo 每帧都会重算。
    const results = useMemo(() => run?.results ?? [], [run]);
    const protocolRows = useMemo(() => results.filter((row) => row.kind === 'protocol'), [results]);
    const capabilityRows = useMemo(() => results.filter((row) => row.kind === 'capability'), [results]);
    const running = probe.isPending;

    const startProbe = (skipConfirmed: boolean) => {
        if (!modelName.trim()) {
            toast.error(t('modelRequired'));
            return;
        }
        setNeedsSkipConfirmation(false);
        setCancelled(false);
        const controller = new AbortController();
        probeController.current = controller;
        probe.mutate(
            {
                channelId: channel.id,
                signal: controller.signal,
                request: {
                    model_name: modelName.trim(),
                    key_index: -1,
                    allow_skip_model_test: skipConfirmed,
                },
            },
            {
                onSuccess: (data) => setRun(data),
                onError: (error) => {
                    if (controller.signal.aborted) return;
                    // 后端用 409 表达"该渠道禁止测活，需确认后重试"。
                    const message = error instanceof Error ? error.message : String(error);
                    if (message.includes('skipped model test') || message.includes('409')) {
                        setNeedsSkipConfirmation(true);
                        return;
                    }
                    toast.error(message);
                },
            },
        );
    };

    const applyResults = () => {
        if (!run?.id) return;
        apply.mutate(
            { channelId: channel.id, runId: run.id },
            {
                onSuccess: (result) => {
                    setRun({ ...run, applied: true });
                    if (result.added_protocols.length === 0) {
                        toast.success(t('applyNoChange'));
                    } else {
                        toast.success(t('applyAdded', {
                            protocols: result.added_protocols.join(', '),
                        }));
                    }
                },
            },
        );
    };

    const renderRow = (row: ChannelProbeResult) => {
        const { Icon, className } = verdictPresentation(row.verdict);
        const latency = formatLatency(row.latency_ms);
        return (
            <div key={`${row.endpoint_id || 'legacy'}-${row.kind}-${row.item}-${row.id ?? 'pending'}`} className="flex items-start gap-2 py-1.5">
                <Icon className={cn('mt-0.5 h-4 w-4 shrink-0', className)} aria-hidden />
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                        <span className="text-sm text-foreground">
                            {row.endpoint_id ? `${channel.connection_config?.endpoints.findIndex((e) => e.id === row.endpoint_id) !== -1 ? (channel.connection_config?.endpoints.findIndex((e) => e.id === row.endpoint_id) ?? 0) + 1 : row.endpoint_id} · ` : ''}
                            {row.item === 'protocol_embeddings' ? 'Embeddings' : t(`item.${row.item}` as never)}
                        </span>
                        <span className={cn('text-xs font-medium', className)}>
                            {t(`verdict.${row.verdict}` as never)}
                        </span>
                        {latency ? <span className="text-xs text-muted-foreground">{latency}</span> : null}
                        {row.status_code ? <span className="text-xs text-muted-foreground">HTTP {row.status_code}</span> : null}
                    </div>
                    {row.summary ? (
                        <p className="mt-0.5 break-words text-xs leading-5 text-muted-foreground">{row.summary}</p>
                    ) : null}
                    {row.detail ? (
                        <pre className="mt-1 max-h-24 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/50 p-2 text-[11px] leading-4 text-muted-foreground">
                            {row.detail}
                        </pre>
                    ) : null}
                </div>
            </div>
        );
    };

    return (
        <div className="space-y-3">
            <div className="space-y-1.5">
                <label htmlFor="channel-probe-model" className="text-sm font-medium text-card-foreground">
                    {t('modelLabel')}
                </label>
                <Input
                    id="channel-probe-model"
                    list="channel-probe-model-options"
                    value={modelName}
                    onChange={(event) => setModelName(event.target.value)}
                    placeholder={t('modelPlaceholder')}
                    disabled={running}
                    className="rounded-lg"
                />
                <datalist id="channel-probe-model-options">
                    {availableModels.map((model) => (
                        <option key={model} value={model} />
                    ))}
                </datalist>
                <p className="text-xs leading-5 text-muted-foreground">{t('modelHint')}</p>
            </div>

            {channel.skip_model_test ? (
                <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 p-3">
                    <p className="flex items-start gap-2 text-xs leading-5 text-amber-600 dark:text-amber-500">
                        <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
                        {t('skipModelTestWarning')}
                    </p>
                    {needsSkipConfirmation ? (
                        <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            className="mt-2 rounded-lg"
                            onClick={() => {
                                setAllowSkipModelTest(true);
                                startProbe(true);
                            }}
                            disabled={running}
                        >
                            {t('skipModelTestConfirm')}
                        </Button>
                    ) : null}
                </div>
            ) : null}

            {/* 日志区：协议层在前、能力层在后，全部堆在同一个滚动框里 */}
            <div className="min-h-40 max-h-80 overflow-y-auto rounded-lg border border-border/40 bg-muted/20 p-3" aria-busy={running}>
                {cancelled && <p role="status" className="text-xs text-muted-foreground">{t('cancelled')}</p>}
                {results.length === 0 && !running ? (
                    <p className="text-xs leading-5 text-muted-foreground">{t('idleHint')}</p>
                ) : null}
                {running ? (
                    <p className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
                        {t('running')}
                    </p>
                ) : null}
                {protocolRows.length > 0 ? (
                    <div className="mb-2">
                        <p className="mb-1 text-xs font-medium text-muted-foreground">{t('sectionProtocol')}</p>
                        {protocolRows.map(renderRow)}
                    </div>
                ) : null}
                {capabilityRows.length > 0 ? (
                    <div>
                        <p className="mb-1 text-xs font-medium text-muted-foreground">{t('sectionCapability')}</p>
                        {capabilityRows.map(renderRow)}
                    </div>
                ) : null}
            </div>

            {run?.summary ? (
                <p className="text-xs text-muted-foreground">{t('summary', { summary: run.summary })}</p>
            ) : null}

            <div className="flex flex-wrap items-center justify-end gap-2">
                {running && <Button type="button" variant="outline" onClick={() => { probeController.current?.abort(); setCancelled(true); }}>{t('cancel')}</Button>}
                <Button type="button" variant="ghost" className="rounded-lg" onClick={() => { probeController.current?.abort(); onClose(); }}>
                    {t('close')}
                </Button>
                <Button
                    type="button"
                    variant="outline"
                    className="rounded-lg"
                    onClick={() => {
                        setRun(null);
                        startProbe(allowSkipModelTest);
                    }}
                    disabled={running || !modelName.trim()}
                >
                    {running ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden /> : <Play className="mr-1 h-4 w-4" aria-hidden />}
                    {run ? t('rerun') : t('start')}
                </Button>
                {!channel.connection_config && <Button
                    type="button"
                    className="rounded-lg"
                    onClick={applyResults}
                    disabled={running || !run?.id || run.applied || apply.isPending}
                >
                    {apply.isPending ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden /> : null}
                    {run?.applied ? t('applied') : t('apply')}
                </Button>}
            </div>
            <p className="text-right text-xs leading-5 text-muted-foreground">{t('applyHint')}</p>
        </div>
    );
}
