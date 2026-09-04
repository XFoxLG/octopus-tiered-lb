'use client';

import { memo, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { Pencil, Trash2, ArrowDownToLine, ArrowUpFromLine, ChevronDown, CircleCheckBig, Gauge, KeyRound, RadioTower, Waves } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useTranslations } from 'next-intl';
import { useUpdateModel, useDeleteModel, type ModelMarketItem } from '@/api/endpoints/model';
import { getModelIcon } from '@/lib/model-icons';
import { toast } from '@/components/common/Toast';
import { ModelDeleteOverlay, ModelEditOverlay } from './ItemOverlays';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { createPortal } from 'react-dom';
import { CopyIconButton } from '@/components/common/CopyButton';
import { formatAverageLatency, type LatencyUnitMode } from './latency-format';
import { useSettingStore } from '@/stores/setting';

interface MobileModelItemProps {
    model: ModelMarketItem;
    latencyUnit?: LatencyUnitMode;
}

/**
 * Compact inline metric: icon + value, no label text.
 * Used in collapsed row to save horizontal space.
 */
function InlineMetric({ icon: Icon, value, color }: { icon: typeof Waves; value: string; color: string }) {
    return (
        <span className="inline-flex items-center gap-0.5 text-[0.65rem] tabular-nums text-muted-foreground shrink-0">
            <Icon className="size-3 shrink-0" style={{ color }} />
            <span className="text-foreground">{value}</span>
        </span>
    );
}

export const MobileModelItem = memo(function MobileModelItem({ model, latencyUnit = 'auto' }: MobileModelItemProps) {
    const t = useTranslations('model');
    const { chinaMode, exchangeRate } = useSettingStore();
    const [isExpanded, setIsExpanded] = useState(false);
    const [isEditOpen, setIsEditOpen] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [overlayRect, setOverlayRect] = useState<{ top: number; left: number; width: number } | null>(null);
    const instanceId = useId();
    const editLayoutId = `m-edit-btn-${model.name}-${instanceId}`;
    const deleteLayoutId = `m-delete-btn-${model.name}-${instanceId}`;
    const cardRef = useRef<HTMLElement | null>(null);
    const editButtonRef = useRef<HTMLButtonElement | null>(null);
    const editOverlayRef = useRef<HTMLDivElement | null>(null);
    const [editValues, setEditValues] = useState(() => ({
        input: model.input.toString(),
        output: model.output.toString(),
        cache_read: model.cache_read.toString(),
        cache_write: model.cache_write.toString(),
    }));

    const updateModel = useUpdateModel();
    const deleteModel = useDeleteModel();

    const { Avatar: ModelAvatar, color: brandColor, label: providerLabel } = useMemo(() => getModelIcon(model.name), [model.name]);
    const requestCount = model.request_success + model.request_failed;
    const visibleChannelTags = useMemo(() => model.channels.slice(0, 3), [model.channels]);
    const hiddenChannelTagCount = Math.max(0, model.channels.length - visibleChannelTags.length);
    const successRateLabel = requestCount > 0 ? `${(model.success_rate * 100).toFixed(1)}%` : '—';
    const latencyLabel = formatAverageLatency(model.average_latency_ms, requestCount, latencyUnit);

    const updateOverlayRect = useCallback(() => {
        const card = cardRef.current;
        if (!card) return;
        const rect = card.getBoundingClientRect();
        setOverlayRect((prev) => {
            if (prev && prev.top === rect.top && prev.left === rect.left && prev.width === rect.width) {
                return prev;
            }
            return { top: rect.top, left: rect.left, width: rect.width };
        });
    }, []);

    const closeEdit = useCallback(() => setIsEditOpen(false), []);

    const handleEditClick = () => {
        setConfirmDelete(false);
        setEditValues({
            input: model.input.toString(),
            output: model.output.toString(),
            cache_read: model.cache_read.toString(),
            cache_write: model.cache_write.toString(),
        });
        updateOverlayRect();
        setIsEditOpen(true);
    };

    const handleCancelEdit = () => closeEdit();

    const handleSaveEdit = () => {
        updateModel.mutate({
            name: model.name,
            input: parseFloat(editValues.input) || 0,
            output: parseFloat(editValues.output) || 0,
            cache_read: parseFloat(editValues.cache_read) || 0,
            cache_write: parseFloat(editValues.cache_write) || 0,
        }, {
            onSuccess: () => { closeEdit(); toast.success(t('toast.updated')); },
            onError: (error) => { toast.error(t('toast.updateFailed'), { description: error.message }); },
        });
    };

    const handleDeleteClick = () => { closeEdit(); setConfirmDelete(true); };
    const handleCancelDelete = () => setConfirmDelete(false);
    const handleConfirmDelete = () => {
        deleteModel.mutate(model.name, {
            onSuccess: () => { setConfirmDelete(false); toast.success(t('toast.deleted')); },
            onError: (error) => { setConfirmDelete(false); toast.error(t('toast.deleteFailed'), { description: error.message }); },
        });
    };

    useEffect(() => {
        if (!isEditOpen) return;
        const handlePointerDown = (event: PointerEvent) => {
            const target = event.target as Node | null;
            if (!target) return;
            if (editOverlayRef.current?.contains(target)) return;
            if (editButtonRef.current?.contains(target)) return;
            closeEdit();
        };
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape') closeEdit();
        };
        updateOverlayRect();
        window.addEventListener('resize', updateOverlayRect);
        window.addEventListener('scroll', updateOverlayRect, true);
        document.addEventListener('pointerdown', handlePointerDown);
        document.addEventListener('keydown', handleKeyDown);
        return () => {
            window.removeEventListener('resize', updateOverlayRect);
            window.removeEventListener('scroll', updateOverlayRect, true);
            document.removeEventListener('pointerdown', handlePointerDown);
            document.removeEventListener('keydown', handleKeyDown);
        };
    }, [isEditOpen, updateOverlayRect, closeEdit]);

    const shouldRenderEditPortal = isEditOpen || overlayRect !== null;

    return (
        <article
            ref={cardRef}
            className={cn(
                'group relative overflow-hidden border border-border/35 bg-card text-card-foreground transition-[border-color] duration-200',
                'first:rounded-t-xl last:rounded-b-xl',
                'not-first:-mt-px', // collapse adjacent borders
                (isEditOpen || confirmDelete) && 'z-50',
            )}
        >
            {/* ── Collapsed header ── */}
            <button
                type="button"
                onClick={() => setIsExpanded((prev) => !prev)}
                className="flex w-full items-center gap-2.5 px-3 py-2.5 text-left"
                aria-expanded={isExpanded}
            >
                {/* Left: icon, vertically centered across both rows */}
                <div className="grid h-9 w-9 shrink-0 place-items-center self-center rounded-lg border border-border/25 bg-card">
                    <div className="[&>svg]:!h-6 [&>svg]:!w-6">
                        <ModelAvatar size={24} />
                    </div>
                </div>
                {/* Right: name + metrics stacked */}
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                    {/* Row 1: name + chevron */}
                    <div className="flex items-center gap-2">
                        <span className="min-w-0 flex-1 truncate text-sm font-semibold leading-tight text-card-foreground">
                            {model.name}
                        </span>
                        <ChevronDown
                            className={cn('size-4 shrink-0 text-muted-foreground transition-transform duration-200', isExpanded && 'rotate-180')}
                        />
                    </div>
                    {/* Row 2: provider badge + metric icons */}
                    <div className="flex items-center gap-1.5 overflow-x-auto no-scrollbar">
                        <span className="inline-flex shrink-0 items-center rounded-full border border-primary/12 bg-card px-1.5 py-px text-[0.58rem] font-semibold text-primary">
                            {providerLabel}
                        </span>
                        {model.billing_schedule === 'deepseek_v4' && (
                            <Badge
                                variant="outline"
                                className="shrink-0 text-[0.58rem] px-1.5 py-px border-amber-400/50 text-amber-500 dark:text-amber-400"
                                title={t('card.billingWindowHint')}
                            >
                                {t('card.billingWindowBadge')}
                            </Badge>
                        )}
                        <InlineMetric icon={Waves} value={requestCount.toLocaleString()} color={brandColor} />
                        <InlineMetric icon={RadioTower} value={String(model.channel_count)} color={brandColor} />
                        <InlineMetric icon={KeyRound} value={String(model.enabled_key_count)} color={brandColor} />
                        <InlineMetric icon={Gauge} value={latencyLabel} color={brandColor} />
                        <InlineMetric icon={CircleCheckBig} value={successRateLabel} color={brandColor} />
                    </div>
                </div>
            </button>

            {/* ── Expanded detail ── */}
            <AnimatePresence initial={false}>
                {isExpanded && (
                    <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.22 }}
                        className="overflow-hidden"
                    >
                        <div className="space-y-3 border-t border-border/20 px-3 pt-3 pb-3">
                            {/* Action buttons */}
                            <div className="flex items-center gap-2">
                                <CopyIconButton
                                    text={model.name}
                                    className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:text-foreground"
                                    copyIconClassName="size-3"
                                    checkIconClassName="size-3"
                                />
                                <motion.button
                                    ref={editButtonRef}
                                    layoutId={editLayoutId}
                                    type="button"
                                    onClick={(e) => { e.stopPropagation(); handleEditClick(); }}
                                    aria-label={t('card.edit')}
                                    disabled={isEditOpen || confirmDelete}
                                    className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
                                >
                                    <Pencil className="size-3" />
                                </motion.button>
                                <motion.button
                                    layoutId={deleteLayoutId}
                                    type="button"
                                    onClick={(e) => { e.stopPropagation(); handleDeleteClick(); }}
                                    aria-label={t('card.delete')}
                                    disabled={isEditOpen || confirmDelete}
                                    className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-destructive/15 bg-destructive/8 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground disabled:opacity-50"
                                >
                                    <Trash2 className="size-3" />
                                </motion.button>
                            </div>

                            {/* Pricing */}
                            <div className="rounded-lg border border-border/25 bg-card p-2.5">
                                <h4 className="text-xs font-medium text-foreground">{t('detail.pricing')}</h4>
                                <div className="mt-2 space-y-1.5 text-xs text-muted-foreground">
                                    <div className="flex items-center justify-between rounded-md bg-card px-2 py-1.5">
                                        <span className="inline-flex shrink-0 items-center gap-1">
                                            <ArrowDownToLine className="size-3" style={{ color: brandColor }} />
                                            {t('card.inputCache')}
                                        </span>
                                        <span className="tabular-nums text-foreground">
                                            {chinaMode
                                                ? `${(model.input * exchangeRate).toFixed(2)}/${(model.cache_read * exchangeRate).toFixed(2)}¥`
                                                : `${model.input.toFixed(2)}/${model.cache_read.toFixed(2)}$`
                                            }
                                        </span>
                                    </div>
                                    <div className="flex items-center justify-between rounded-md bg-card px-2 py-1.5">
                                        <span className="inline-flex shrink-0 items-center gap-1">
                                            <ArrowUpFromLine className="size-3" style={{ color: brandColor }} />
                                            {t('card.outputCache')}
                                        </span>
                                        <span className="tabular-nums text-foreground">
                                            {chinaMode
                                                ? `${(model.output * exchangeRate).toFixed(2)}/${(model.cache_write * exchangeRate).toFixed(2)}¥`
                                                : `${model.output.toFixed(2)}/${model.cache_write.toFixed(2)}$`
                                            }
                                        </span>
                                    </div>
                                </div>
                            </div>

                            {/* Runtime */}
                            <div className="rounded-lg border border-border/25 bg-card p-2.5">
                                <h4 className="text-xs font-medium text-foreground">{t('detail.runtime')}</h4>
                                <div className="mt-2 grid grid-cols-2 gap-1.5 text-xs text-muted-foreground">
                                    <div className="rounded-md bg-card px-2 py-1.5">
                                        <div>{t('detail.requestSuccess')}</div>
                                        <div className="mt-0.5 tabular-nums text-sm font-medium text-foreground">{model.request_success.toLocaleString()}</div>
                                    </div>
                                    <div className="rounded-md bg-card px-2 py-1.5">
                                        <div>{t('detail.requestFailed')}</div>
                                        <div className="mt-0.5 tabular-nums text-sm font-medium text-foreground">{model.request_failed.toLocaleString()}</div>
                                    </div>
                                    <div className="rounded-md bg-card px-2 py-1.5">
                                        <div>{t('card.averageLatency')}</div>
                                        <div className="mt-0.5 tabular-nums text-sm font-medium text-foreground">{latencyLabel}</div>
                                    </div>
                                    <div className="rounded-md bg-card px-2 py-1.5">
                                        <div>{t('card.successRate')}</div>
                                        <div className="mt-0.5 tabular-nums text-sm font-medium text-foreground">{successRateLabel}</div>
                                    </div>
                                </div>
                            </div>

                            {/* Channels */}
                            <div className="rounded-lg border border-border/25 bg-card p-2.5">
                                <div className="flex items-center justify-between">
                                    <h4 className="text-xs font-medium text-foreground">{t('detail.channels')}</h4>
                                    <span className="text-[0.65rem] text-muted-foreground">{model.channels.length} {t('card.channels')}</span>
                                </div>
                                <div className="mt-2 space-y-1.5">
                                    {model.channels.length === 0 ? (
                                        <div className="rounded-md bg-card px-2 py-1.5 text-xs text-muted-foreground">{t('detail.noChannels')}</div>
                                    ) : (
                                        model.channels.map((channel) => (
                                            <div key={`${model.name}-m-detail-${channel.channel_id}`} className="flex items-center justify-between rounded-md bg-card px-2 py-1.5">
                                                <div className="min-w-0">
                                                    <div className="truncate text-xs font-medium text-foreground">{channel.channel_name}</div>
                                                    <div className="text-[0.65rem] text-muted-foreground">ID {channel.channel_id}</div>
                                                </div>
                                                <div className="flex shrink-0 items-center gap-1.5">
                                                    <span className={cn(
                                                        'rounded-full border px-1.5 py-px text-[0.6rem]',
                                                        channel.enabled
                                                            ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-700'
                                                            : 'border-border/25 bg-card text-muted-foreground',
                                                    )}>
                                                        {channel.enabled ? t('detail.enabled') : t('detail.disabled')}
                                                    </span>
                                                    <span className="text-[0.6rem] text-muted-foreground">{channel.enabled_key_count}k</span>
                                                </div>
                                            </div>
                                        ))
                                    )}
                                </div>
                            </div>

                            {/* Channel tags */}
                            <div className="flex flex-wrap gap-1">
                                {visibleChannelTags.map((channel) => (
                                    <span
                                        key={`${model.name}-m-tag-${channel.channel_id}`}
                                        className="rounded-full border border-border/25 bg-card px-1.5 py-px text-[0.6rem] text-muted-foreground"
                                    >
                                        {channel.channel_name}
                                    </span>
                                ))}
                                {hiddenChannelTagCount > 0 && (
                                    <span className="rounded-full border border-border/25 bg-card px-1.5 py-px text-[0.6rem] text-muted-foreground">
                                        +{hiddenChannelTagCount}
                                    </span>
                                )}
                            </div>
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>

            {/* Delete confirm overlay */}
            <AnimatePresence>
                {confirmDelete && (
                    <ModelDeleteOverlay
                        layoutId={deleteLayoutId}
                        isPending={deleteModel.isPending}
                        onCancel={handleCancelDelete}
                        onConfirm={handleConfirmDelete}
                    />
                )}
            </AnimatePresence>

            {/* Edit overlay portal */}
            {shouldRenderEditPortal && typeof document !== 'undefined'
                ? createPortal(
                    <AnimatePresence onExitComplete={() => setOverlayRect(null)}>
                        {isEditOpen && overlayRect && (
                            <div
                                ref={editOverlayRef}
                                className="fixed z-[90]"
                                style={{ top: `${overlayRect.top}px`, left: `${overlayRect.left}px`, width: `${overlayRect.width}px` }}
                            >
                                <div className="relative">
                                    <ModelEditOverlay
                                        layoutId={editLayoutId}
                                        modelName={model.name}
                                        brandColor={brandColor}
                                        editValues={editValues}
                                        isPending={updateModel.isPending}
                                        onChange={setEditValues}
                                        onCancel={handleCancelEdit}
                                        onSave={handleSaveEdit}
                                        peakBilling={model.billing_schedule === 'deepseek_v4'}
                                    />
                                </div>
                            </div>
                        )}
                    </AnimatePresence>,
                    document.body,
                )
                : null}
        </article>
    );
});
