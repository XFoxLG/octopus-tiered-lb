'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import {
    AlertTriangle,
    CheckCircle2,
    FlaskConical,
    Loader2,
    MinusCircle,
    Play,
    ShieldAlert,
    ShieldCheck,
    Stethoscope,
    XCircle,
} from 'lucide-react';
import {
    ChannelType,
    useApplyChannelProbe,
    useChannelProbe,
    useCheckChannelKeys,
    useTestChannelModel,
    type Channel,
    type ChannelProbeResult,
    type ChannelProbeRun,
    type ProbeVerdict,
    type TestChannelSummary,
} from '@/api/endpoints/channel';
import { useGroupTestProgress } from '@/api/endpoints/group';
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

/** 旧渠道（无 connection_config）按渠道类型推断端点类型；新配置渠道由调用方覆盖。 */
function inferEndpointType(type: ChannelType): string {
    return type === ChannelType.OpenAIEmbedding ? 'embeddings' : '*';
}

export type TestDialogProps = {
    channel: Channel;
    availableModels: string[];
    onClose: () => void;
    /** 逐 Key 检查全部不可用时触发（父级负责弹原有的删除确认对话框）。 */
    onKeysUnavailable: (summary: TestChannelSummary) => void;
};

/**
 * 渠道「测试」统一弹窗：一个模型选择器 + 三段按需执行的检查。
 *
 * ① 模型应答：与分组测试同源，发一次真实请求并轮询进度。
 * ② 协议与能力：逐一验证上游协议与能力项；结果只加不减，需要手动「应用结果」。
 * ③ 逐 Key 检查：base_urls × keys 连通性汇总；全部失败时回调父级弹删除确认。
 *
 * 三段共用一个滚动结果区，各自独立状态；关闭弹窗会中止在途请求与轮询。
 * 刻意不做自动应用、不做定时探测 —— 多数公益站禁止测活，部分还有测活关键词拦截。
 */
export function TestDialog({ channel, availableModels, onClose, onKeysUnavailable }: TestDialogProps) {
    const t = useTranslations('channel.test');
    const tProbe = useTranslations('channel.probe');
    const tModel = useTranslations('channel.detail.testModel');
    const tActions = useTranslations('channel.detail.actions');

    const [modelName, setModelName] = useState(availableModels[0] ?? '');
    const lastChannelId = useRef(channel.id);
    useEffect(() => {
        if (lastChannelId.current !== channel.id) {
            lastChannelId.current = channel.id;
            setModelName(availableModels[0] ?? '');
        }
    }, [channel.id, availableModels]);

    // ---- ① 模型应答 ----
    const testChannelModel = useTestChannelModel();
    const [currentTestId, setCurrentTestId] = useState<string | null>(null);
    const [modelCancelled, setModelCancelled] = useState(false);
    const testProgressQuery = useGroupTestProgress(currentTestId);
    const testProgress = testProgressQuery.data;
    const isTestingModel = testChannelModel.isPending
        || (currentTestId !== null && testProgress !== undefined && !testProgress.done);
    const modelResult = testProgress?.results?.[0];

    const startModelTest = () => {
        if (!modelName.trim() || isTestingModel) {
            if (!modelName.trim()) toast.error(tProbe('modelRequired'));
            return;
        }
        setModelCancelled(false);
        setCurrentTestId(null);
        testChannelModel.mutate({
            channel_id: channel.id,
            model_name: modelName.trim(),
            endpoint_type: channel.connection_config
                ? (channel.connection_config.endpoints.every((e) => e.protocol === 'embeddings') ? 'embeddings' : '*')
                : inferEndpointType(channel.type),
        }, {
            onSuccess: (progress) => setCurrentTestId(progress.id),
        });
    };

    // 模型应答是「上游异步任务 + 前端轮询」，中止等待只停止轮询，不假装取消上游请求。
    const cancelModelTest = () => {
        setCurrentTestId(null);
        setModelCancelled(true);
    };

    // ---- ② 协议与能力 ----
    const [allowSkipModelTest, setAllowSkipModelTest] = useState(false);
    const [probeRun, setProbeRun] = useState<ChannelProbeRun | null>(null);
    const [needsSkipConfirmation, setNeedsSkipConfirmation] = useState(false);
    const [probeCancelled, setProbeCancelled] = useState(false);
    const probeController = useRef<AbortController | null>(null);
    const probe = useChannelProbe();
    const apply = useApplyChannelProbe();

    const probeResults = useMemo(() => probeRun?.results ?? [], [probeRun]);
    const protocolRows = useMemo(() => probeResults.filter((row) => row.kind === 'protocol'), [probeResults]);
    const capabilityRows = useMemo(() => probeResults.filter((row) => row.kind === 'capability'), [probeResults]);
    const isProbing = probe.isPending;

    const startProbe = (skipConfirmed: boolean) => {
        if (!modelName.trim()) {
            toast.error(tProbe('modelRequired'));
            return;
        }
        setNeedsSkipConfirmation(false);
        setProbeCancelled(false);
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
                onSuccess: (data) => setProbeRun(data),
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

    const applyProbeResults = () => {
        if (!probeRun?.id) return;
        apply.mutate(
            { channelId: channel.id, runId: probeRun.id },
            {
                onSuccess: (result) => {
                    setProbeRun({ ...probeRun, applied: true });
                    if (result.added_protocols.length === 0) {
                        toast.success(tProbe('applyNoChange'));
                    } else {
                        toast.success(tProbe('applyAdded', {
                            protocols: result.added_protocols.join(', '),
                        }));
                    }
                },
            },
        );
    };

    // ---- ③ 逐 Key 检查 ----
    const checkChannelKeys = useCheckChannelKeys();
    const [checkResult, setCheckResult] = useState<TestChannelSummary | null>(null);
    const [checkCancelled, setCheckCancelled] = useState(false);
    const checkController = useRef<AbortController | null>(null);

    const startCheckKeys = () => {
        if (checkChannelKeys.isPending || channel.keys.length === 0) return;
        setCheckResult(null);
        setCheckCancelled(false);
        const controller = new AbortController();
        checkController.current = controller;
        checkChannelKeys.mutate({ id: channel.id, signal: controller.signal }, {
            onSuccess: (summary) => {
                setCheckResult(summary);
                if (!summary.passed) onKeysUnavailable(summary);
            },
            onError: (error) => {
                if (controller.signal.aborted) return;
                toast.error(tActions('checkFailed'), {
                    description: error instanceof Error ? error.message : String(error),
                });
            },
        });
    };

    // 关闭弹窗（或组件卸载）时中止在途请求与轮询。
    useEffect(() => () => {
        probeController.current?.abort();
        checkController.current?.abort();
    }, []);

    const closeDialog = () => {
        probeController.current?.abort();
        checkController.current?.abort();
        setCurrentTestId(null);
        onClose();
    };

    const renderProbeRow = (row: ChannelProbeResult) => {
        const { Icon, className } = verdictPresentation(row.verdict);
        const latency = formatLatency(row.latency_ms);
        return (
            <div key={`${row.endpoint_id || 'legacy'}-${row.kind}-${row.item}-${row.id ?? 'pending'}`} className="flex items-start gap-2 py-1.5">
                <Icon className={cn('mt-0.5 h-4 w-4 shrink-0', className)} aria-hidden />
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                        <span className="text-sm text-foreground">
                            {row.endpoint_id ? `${channel.connection_config?.endpoints.findIndex((e) => e.id === row.endpoint_id) !== -1 ? (channel.connection_config?.endpoints.findIndex((e) => e.id === row.endpoint_id) ?? 0) + 1 : row.endpoint_id} · ` : ''}
                            {row.item === 'protocol_embeddings' ? 'Embeddings' : tProbe(`item.${row.item}` as never)}
                        </span>
                        <span className={cn('text-xs font-medium', className)}>
                            {tProbe(`verdict.${row.verdict}` as never)}
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

    // 后端 summary.passed = 「至少一项通过」：全通过、部分通过、全部失败要分开说，
    // 否则部分可用也会被读成「全部不可用，可删除该渠道」。
    const checkPassedCount = checkResult?.results.filter((r) => r.passed).length ?? 0;
    const checkTotal = checkResult?.results.length ?? 0;
    const checkSummary = !checkResult
        ? ''
        : !checkResult.passed
            ? tActions('checkAllFailed')
            : checkPassedCount === checkTotal
                ? tActions('checkAllPassed', { passed: checkPassedCount, total: checkTotal })
                : tActions('checkPartialPassed', { passed: checkPassedCount, total: checkTotal });

    const anyRunning = isTestingModel || isProbing || checkChannelKeys.isPending;

    return (
        <div className="space-y-3">
            <div className="space-y-1.5">
                <label htmlFor="channel-test-model" className="text-sm font-medium text-card-foreground">
                    {tProbe('modelLabel')}
                </label>
                <Input
                    id="channel-test-model"
                    list="channel-test-model-options"
                    value={modelName}
                    onChange={(event) => setModelName(event.target.value)}
                    placeholder={tProbe('modelPlaceholder')}
                    disabled={anyRunning}
                    className="rounded-lg"
                />
                <datalist id="channel-test-model-options">
                    {availableModels.map((model) => (
                        <option key={model} value={model} />
                    ))}
                </datalist>
                <p className="text-xs leading-5 text-muted-foreground">{tProbe('modelHint')}</p>
            </div>

            {/* 三段共用的滚动结果区 */}
            <div
                className="min-h-40 max-h-80 space-y-3 overflow-y-auto rounded-lg border border-border/40 bg-muted/20 p-3"
                role="region"
                aria-label={t('resultsLabel')}
                aria-busy={anyRunning}
                tabIndex={0}
            >
                {/* ① 模型应答 */}
                <section>
                    <div className="mb-1 flex items-center gap-2">
                        <FlaskConical className="size-3.5 text-muted-foreground" aria-hidden />
                        <p className="text-xs font-medium text-muted-foreground">{t('sections.model')}</p>
                        {isTestingModel && <Loader2 className="size-3.5 animate-spin text-muted-foreground" aria-hidden />}
                    </div>
                    {modelCancelled && <p role="status" className="text-xs text-muted-foreground">{t('modelCancelled')}</p>}
                    {modelResult && testProgress?.done ? (
                        <div className={cn(
                            'flex items-start gap-2 rounded-lg border px-2.5 py-2 text-xs',
                            modelResult.passed
                                ? 'border-emerald-500/30 bg-emerald-500/8 text-emerald-700 dark:text-emerald-300'
                                : 'border-destructive/30 bg-destructive/8 text-destructive',
                        )}>
                            {modelResult.passed
                                ? <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden />
                                : <XCircle className="mt-0.5 size-4 shrink-0" aria-hidden />}
                            <div className="min-w-0 flex-1">
                                <div className="flex flex-wrap items-center gap-2">
                                    <span className="font-medium">{modelResult.passed ? tModel('passed') : tModel('failed')}</span>
                                    {modelResult.status_code !== 0 && <span className="text-muted-foreground">HTTP {modelResult.status_code}</span>}
                                    {modelResult.attempts > 1 && <span className="text-muted-foreground">{tModel('attempts', { count: modelResult.attempts })}</span>}
                                </div>
                                {modelResult.message && modelResult.message !== 'ok' && (
                                    <p className="mt-1 break-words opacity-80">{modelResult.message}</p>
                                )}
                            </div>
                        </div>
                    ) : null}
                    {!modelResult && !isTestingModel && !modelCancelled && (
                        <p className="text-xs leading-5 text-muted-foreground">{t('modelIdle')}</p>
                    )}
                </section>

                {/* ② 协议与能力 */}
                <section>
                    <div className="mb-1 flex items-center gap-2">
                        <ShieldCheck className="size-3.5 text-muted-foreground" aria-hidden />
                        <p className="text-xs font-medium text-muted-foreground">{t('sections.probe')}</p>
                        {isProbing && <Loader2 className="size-3.5 animate-spin text-muted-foreground" aria-hidden />}
                    </div>
                    {probeCancelled && <p role="status" className="text-xs text-muted-foreground">{tProbe('cancelled')}</p>}
                    {probeResults.length === 0 && !isProbing && !probeCancelled ? (
                        <p className="text-xs leading-5 text-muted-foreground">{tProbe('idleHint')}</p>
                    ) : null}
                    {isProbing ? (
                        <p className="flex items-center gap-2 text-xs text-muted-foreground">
                            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
                            {tProbe('running')}
                        </p>
                    ) : null}
                    {protocolRows.length > 0 ? (
                        <div className="mb-2">
                            <p className="mb-1 text-xs font-medium text-muted-foreground">{tProbe('sectionProtocol')}</p>
                            {protocolRows.map(renderProbeRow)}
                        </div>
                    ) : null}
                    {capabilityRows.length > 0 ? (
                        <div>
                            <p className="mb-1 text-xs font-medium text-muted-foreground">{tProbe('sectionCapability')}</p>
                            {capabilityRows.map(renderProbeRow)}
                        </div>
                    ) : null}
                    {probeRun?.summary ? (
                        <p className="text-xs text-muted-foreground">{tProbe('summary', { summary: probeRun.summary })}</p>
                    ) : null}
                    {channel.connection_config && (
                        <p className="mt-1 text-xs leading-5 text-muted-foreground">{t('diagnosticOnly')}</p>
                    )}
                </section>

                {/* ③ 逐 Key 检查 */}
                <section>
                    <div className="mb-1 flex items-center gap-2">
                        <Stethoscope className="size-3.5 text-muted-foreground" aria-hidden />
                        <p className="text-xs font-medium text-muted-foreground">{t('sections.keys')}</p>
                        {checkChannelKeys.isPending && <Loader2 className="size-3.5 animate-spin text-muted-foreground" aria-hidden />}
                    </div>
                    {channel.keys.length === 0 ? (
                        <p className="text-xs leading-5 text-muted-foreground">{t('noKeys')}</p>
                    ) : null}
                    {checkCancelled && <p role="status" className="text-xs text-muted-foreground">{t('keysCancelled')}</p>}
                    {checkResult && (
                        <div className="space-y-1">
                            <p className={cn('text-xs font-medium', checkResult.passed ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive')}>
                                {checkSummary}
                            </p>
                            {checkResult.results.map((row, index) => (
                                <div key={`${row.base_url}-${index}`} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs">
                                    {row.passed
                                        ? <CheckCircle2 className="size-3.5 shrink-0 text-emerald-500" aria-hidden />
                                        : <XCircle className="size-3.5 shrink-0 text-destructive" aria-hidden />}
                                    <span className="break-all text-muted-foreground">{row.base_url}</span>
                                    {(row.key_remark || row.key_masked) && (
                                        <span className="text-muted-foreground">{row.key_remark ? `${row.key_remark} / ` : ''}{row.key_masked}</span>
                                    )}
                                    {row.status_code ? <span className="text-muted-foreground">HTTP {row.status_code}</span> : null}
                                    {formatLatency(row.latency_ms) ? <span className="text-muted-foreground">{formatLatency(row.latency_ms)}</span> : null}
                                    {row.message ? <span className="break-words text-muted-foreground">{row.message}</span> : null}
                                </div>
                            ))}
                        </div>
                    )}
                    {!checkResult && !checkChannelKeys.isPending && !checkCancelled && channel.keys.length > 0 ? (
                        <p className="text-xs leading-5 text-muted-foreground">{t('keysIdle')}</p>
                    ) : null}
                </section>
            </div>

            {channel.skip_model_test ? (
                <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 p-3">
                    <p className="flex items-start gap-2 text-xs leading-5 text-amber-600 dark:text-amber-500">
                        <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
                        {tProbe('skipModelTestWarning')}
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
                            disabled={isProbing}
                        >
                            {tProbe('skipModelTestConfirm')}
                        </Button>
                    ) : null}
                </div>
            ) : null}

            <div className="flex flex-wrap items-center justify-end gap-2">
                {isTestingModel && (
                    <Button type="button" variant="ghost" size="sm" onClick={cancelModelTest}>{t('cancelWait')}</Button>
                )}
                {isProbing && (
                    <Button type="button" variant="ghost" size="sm" onClick={() => { probeController.current?.abort(); setProbeCancelled(true); }}>{tProbe('cancel')}</Button>
                )}
                {checkChannelKeys.isPending && (
                    <Button type="button" variant="ghost" size="sm" onClick={() => { checkController.current?.abort(); setCheckCancelled(true); }}>{t('cancelWait')}</Button>
                )}
                <Button
                    type="button"
                    variant="outline"
                    className="rounded-lg"
                    onClick={startModelTest}
                    disabled={anyRunning || !modelName.trim()}
                >
                    <Play className="mr-1 h-4 w-4" aria-hidden />
                    {modelResult && testProgress?.done ? t('rerunSection', { section: t('sections.model') }) : t('runSection', { section: t('sections.model') })}
                </Button>
                <Button
                    type="button"
                    variant="outline"
                    className="rounded-lg"
                    onClick={() => { setProbeRun(null); startProbe(allowSkipModelTest); }}
                    disabled={anyRunning || !modelName.trim()}
                >
                    <Play className="mr-1 h-4 w-4" aria-hidden />
                    {probeRun ? t('rerunSection', { section: t('sections.probe') }) : t('runSection', { section: t('sections.probe') })}
                </Button>
                <Button
                    type="button"
                    variant="outline"
                    className="rounded-lg"
                    onClick={startCheckKeys}
                    disabled={anyRunning || channel.keys.length === 0}
                >
                    <Play className="mr-1 h-4 w-4" aria-hidden />
                    {checkResult ? t('rerunSection', { section: t('sections.keys') }) : t('runSection', { section: t('sections.keys') })}
                </Button>
                {!channel.connection_config && (
                    <Button
                        type="button"
                        className="rounded-lg"
                        onClick={applyProbeResults}
                        disabled={anyRunning || !probeRun?.id || probeRun.applied || apply.isPending}
                    >
                        {apply.isPending ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden /> : null}
                        {probeRun?.applied ? tProbe('applied') : tProbe('apply')}
                    </Button>
                )}
                <Button type="button" variant="ghost" className="rounded-lg" onClick={closeDialog}>
                    {tProbe('close')}
                </Button>
            </div>
            <p className="text-right text-xs leading-5 text-muted-foreground">{t('footerHint')}</p>
        </div>
    );
}
