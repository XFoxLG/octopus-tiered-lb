'use client';

import { memo, useId, useMemo, useState, type ComponentType } from 'react';
import { ChevronDown, CircleCheckBig, Gauge, KeyRound, Pencil, RadioTower, Trash2, Waves } from 'lucide-react';
import { AnimatePresence, motion } from 'motion/react';
import { useTranslations } from 'next-intl';
import type { ModelMarketItem } from '@/api/endpoints/model';
import { getModelIcon } from '@/lib/model-icons';
import { ModelDeleteDialog, ModelEditDialog } from './ItemOverlays';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { CopyIconButton } from '@/components/common/CopyButton';
import { ChannelTagsRow, ModelItemDetail } from './ItemDetail';
import { formatAverageLatency, type LatencyUnitMode } from './latency-format';
import { useModelActions } from './use-model-actions';
import { formatSuccessRate, isPeakBillingSchedule, totalRequests } from './pricing';

interface MobileModelItemProps {
    model: ModelMarketItem;
    latencyUnit?: LatencyUnitMode;
}

/**
 * Compact inline metric: icon + value, no visible label text.
 * Used in collapsed row to save horizontal space; the label is kept for
 * screen readers and the native title tooltip.
 */
function InlineMetric({
    icon: Icon,
    label,
    value,
    color,
}: {
    icon: ComponentType<{ className?: string; style?: { color: string } }>;
    label: string;
    value: string;
    color: string;
}) {
    return (
        <span className="inline-flex shrink-0 items-center gap-0.5 text-[0.65rem] tabular-nums text-muted-foreground" title={label}>
            <Icon className="size-3 shrink-0" style={{ color }} />
            <span className="sr-only">{label}</span>
            <span className="text-foreground">{value}</span>
        </span>
    );
}

export const MobileModelItem = memo(function MobileModelItem({ model, latencyUnit = 'auto' }: MobileModelItemProps) {
    const t = useTranslations('model');
    const [isExpanded, setIsExpanded] = useState(false);
    const instanceId = useId();
    const detailRegionId = `mobile-model-detail-${instanceId}`;

    const {
        isEditOpen,
        isDeleteOpen,
        editValues,
        setEditValues,
        invalidPriceFields,
        openEdit,
        openDelete,
        closeEdit,
        closeDelete,
        saveEdit,
        confirmDelete,
        isUpdatePending,
        isDeletePending,
    } = useModelActions(model);

    const { Avatar: ModelAvatar, color: brandColor, label: providerLabel } = useMemo(() => getModelIcon(model.name), [model.name]);
    const requestCount = totalRequests(model.request_success, model.request_failed);
    const latencyLabel = formatAverageLatency(model.average_latency_ms, requestCount, latencyUnit);
    const successRateLabel = formatSuccessRate(model.success_rate, requestCount);
    const isPeakBilling = isPeakBillingSchedule(model.billing_schedule);

    return (
        <article className="group relative overflow-hidden border border-border/35 bg-card text-card-foreground transition-[border-color] duration-200 first:rounded-t-xl last:rounded-b-xl not-first:-mt-px">
            {/* ── Collapsed header ── */}
            <button
                type="button"
                onClick={() => setIsExpanded((prev) => !prev)}
                className="flex w-full items-center gap-2.5 px-3 py-2.5 text-left"
                aria-expanded={isExpanded}
                aria-controls={detailRegionId}
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
                        {isPeakBilling && (
                            <Badge
                                variant="outline"
                                className="shrink-0 text-[0.58rem] px-1.5 py-px border-amber-400/50 text-amber-500 dark:text-amber-400"
                                title={t('card.billingWindowHint')}
                            >
                                {t('card.billingWindowBadge')}
                            </Badge>
                        )}
                        <InlineMetric icon={Waves} label={t('card.requests')} value={requestCount.toLocaleString()} color={brandColor} />
                        <InlineMetric icon={RadioTower} label={t('card.channels')} value={String(model.channel_count)} color={brandColor} />
                        <InlineMetric icon={KeyRound} label={t('card.keys')} value={String(model.enabled_key_count)} color={brandColor} />
                        <InlineMetric icon={Gauge} label={t('card.averageLatency')} value={latencyLabel} color={brandColor} />
                        <InlineMetric icon={CircleCheckBig} label={t('card.successRate')} value={successRateLabel} color={brandColor} />
                    </div>
                </div>
            </button>

            {/* ── Expanded detail ── */}
            <AnimatePresence initial={false}>
                {isExpanded && (
                    <motion.div
                        id={detailRegionId}
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.22 }}
                        className="overflow-hidden"
                    >
                        <div className="space-y-3 border-t border-border/20 px-3 py-3">
                            {/* Action buttons */}
                            <div className="flex items-center gap-2">
                                <CopyIconButton
                                    text={model.name}
                                    className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:text-foreground"
                                    copyIconClassName="size-3"
                                    checkIconClassName="size-3"
                                />
                                <button
                                    type="button"
                                    onClick={openEdit}
                                    aria-label={t('card.edit')}
                                    className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:text-foreground"
                                >
                                    <Pencil className="size-3" />
                                </button>
                                <button
                                    type="button"
                                    onClick={openDelete}
                                    aria-label={t('card.delete')}
                                    className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-destructive/15 bg-destructive/8 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground"
                                >
                                    <Trash2 className="size-3" />
                                </button>
                            </div>

                            <ModelItemDetail
                                model={model}
                                brandColor={brandColor}
                                latencyUnit={latencyUnit}
                                density="mobile"
                            />

                            <ChannelTagsRow model={model} maxVisible={3} density="mobile" keyPrefix={`m-${model.name}`} />
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>

            <ModelEditDialog
                modelName={model.name}
                brandColor={brandColor}
                open={isEditOpen}
                onOpenChange={(open) => { if (!open) closeEdit(); }}
                editValues={editValues}
                invalidFields={invalidPriceFields}
                isPending={isUpdatePending}
                onChange={setEditValues}
                onSave={saveEdit}
                peakBilling={isPeakBilling}
            />
            <ModelDeleteDialog
                modelName={model.name}
                open={isDeleteOpen}
                isPending={isDeletePending}
                onOpenChange={(open) => { if (!open) closeDelete(); }}
                onConfirm={confirmDelete}
            />
        </article>
    );
});
