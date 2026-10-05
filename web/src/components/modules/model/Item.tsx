'use client';

import { memo, useId, useMemo, useState, type ComponentType } from 'react';
import { ChevronDown, CircleCheckBig, Gauge, KeyRound, Orbit, Pencil, RadioTower, Trash2, Waves } from 'lucide-react';
import { AnimatePresence, motion } from 'motion/react';
import { useTranslations } from 'next-intl';
import type { ModelMarketItem } from '@/api/endpoints/model';
import { getModelIcon } from '@/lib/model-icons';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';
import { Badge } from '@/components/ui/badge';
import { CopyIconButton } from '@/components/common/CopyButton';
import { cn } from '@/lib/utils';
import { ChannelTagsRow, ModelItemDetail } from './ItemDetail';
import { ModelDeleteDialog, ModelEditDialog } from './ItemOverlays';
import { useModelActions } from './use-model-actions';
import { formatAverageLatency, type LatencyUnitMode } from './latency-format';
import { formatSuccessRate, isPeakBillingSchedule, totalRequests } from './pricing';

interface ModelItemProps {
    model: ModelMarketItem;
    layout?: 'grid' | 'list' | 'compact';
    latencyUnit?: LatencyUnitMode;
}

const METRIC_TILE_CLASS =
    'inline-flex min-w-0 items-center gap-1.5 rounded-lg border border-border/25 bg-card px-2 py-1.5 text-xs sm:gap-2 sm:px-3 sm:py-2 sm:text-sm';

/** 卡片中部「图标 + 标签 + 数值」指标块。list 布局下拉伸占满一行。 */
function MetricTile({
    icon: Icon,
    label,
    value,
    color,
    wide = false,
}: {
    icon: ComponentType<{ className?: string; style?: { color: string } }>;
    label: string;
    value: string;
    color: string;
    wide?: boolean;
}) {
    return (
        <div className={cn(METRIC_TILE_CLASS, wide && 'min-w-[10rem] flex-1')}>
            <Icon className="size-3.5 shrink-0 sm:size-4" style={{ color }} />
            <span className="min-w-0 truncate">{label}</span>
            <span className="ml-auto shrink-0 tabular-nums text-foreground">{value}</span>
        </div>
    );
}

export const ModelItem = memo(function ModelItem({ model, layout = 'grid', latencyUnit = 'auto' }: ModelItemProps) {
    const t = useTranslations('model');
    const isListLayout = layout === 'list';
    const [isExpanded, setIsExpanded] = useState(false);
    const instanceId = useId();
    const detailRegionId = `model-detail-${instanceId}`;

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
        <article className="group relative overflow-hidden rounded-xl border border-border/35 bg-card p-3 text-card-foreground transition-[border-color] duration-300 hover:border-primary/18 sm:p-4 md:hover:-translate-y-0.5 md:p-5">
            <div className="relative flex flex-col gap-3 sm:gap-4">
                <div className="flex items-start gap-3 sm:gap-4">
                    <div className="grid h-12 w-12 shrink-0 place-items-center rounded-lg border border-border/25 bg-card sm:h-16 sm:w-16">
                        <div className="[&>svg]:!h-9 [&>svg]:!w-9 sm:[&>svg]:!h-12 sm:[&>svg]:!w-12">
                            <ModelAvatar size={48} />
                        </div>
                    </div>

                    <div className="min-w-0 flex-1 space-y-2 sm:space-y-3">
                        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-3">
                            <div className="min-w-0 space-y-1.5 sm:space-y-2">
                                <div className="inline-flex items-center gap-1.5 rounded-full border border-primary/12 bg-card px-2.5 py-0.5 text-[0.62rem] font-semibold text-primary sm:gap-2 sm:px-3 sm:py-1 sm:text-[0.68rem]">
                                    <Orbit className="size-3 sm:size-3.5" />
                                    {providerLabel}
                                </div>
                                <Tooltip side="top" sideOffset={10} align="start">
                                    <TooltipTrigger className="block max-w-full truncate text-left text-base font-semibold leading-tight text-card-foreground sm:text-lg">
                                        {model.name}
                                    </TooltipTrigger>
                                    <TooltipContent key={model.name}>{model.name}</TooltipContent>
                                </Tooltip>
                                {isPeakBilling && (
                                    <Badge
                                        variant="outline"
                                        className="shrink-0 text-[0.62rem] px-1.5 py-0 border-amber-400/50 text-amber-500 dark:text-amber-400"
                                        title={t('card.billingWindowHint')}
                                    >
                                        {t('card.billingWindowBadge')}
                                    </Badge>
                                )}
                                <div className="flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground sm:gap-2">
                                    <span className="inline-flex items-center gap-1.5 rounded-full border border-border/25 bg-card px-2.5 py-0.5 text-[0.68rem] sm:gap-2 sm:px-3 sm:py-1 sm:text-xs">
                                        <Waves className="size-3 text-primary sm:size-3.5" />
                                        {t('card.requests')}: {requestCount.toLocaleString()}
                                    </span>
                                </div>
                            </div>

                            <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
                                <CopyIconButton
                                    text={model.name}
                                    className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:bg-card hover:text-foreground sm:h-10 sm:w-10"
                                    copyIconClassName="size-3.5 sm:size-4"
                                    checkIconClassName="size-3.5 sm:size-4"
                                />
                                <button
                                    type="button"
                                    onClick={() => setIsExpanded((prev) => !prev)}
                                    aria-label={isExpanded ? t('card.collapse') : t('card.expand')}
                                    aria-expanded={isExpanded}
                                    aria-controls={detailRegionId}
                                    className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:bg-card hover:text-foreground sm:h-10 sm:w-10"
                                    title={isExpanded ? t('card.collapse') : t('card.expand')}
                                >
                                    <ChevronDown className={cn('size-3.5 transition-transform sm:size-4', isExpanded && 'rotate-180')} />
                                </button>
                                <button
                                    type="button"
                                    onClick={openEdit}
                                    aria-label={t('card.edit')}
                                    className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-border/25 bg-card text-muted-foreground transition-colors hover:bg-card hover:text-foreground sm:h-10 sm:w-10"
                                    title={t('card.edit')}
                                >
                                    <Pencil className="size-3.5 sm:size-4" />
                                </button>
                                <button
                                    type="button"
                                    onClick={openDelete}
                                    aria-label={t('card.delete')}
                                    className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-destructive/15 bg-destructive/8 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground sm:h-10 sm:w-10"
                                    title={t('card.delete')}
                                >
                                    <Trash2 className="size-3.5 sm:size-4" />
                                </button>
                            </div>
                        </div>

                        <div className={cn('grid gap-1.5 text-sm text-muted-foreground sm:gap-2', isListLayout ? 'grid-cols-2 xl:grid-cols-4' : 'grid-cols-2')}>
                            <MetricTile icon={RadioTower} label={t('card.channels')} value={String(model.channel_count)} color={brandColor} wide={isListLayout} />
                            <MetricTile icon={KeyRound} label={t('card.keys')} value={String(model.enabled_key_count)} color={brandColor} wide={isListLayout} />
                            <MetricTile icon={Gauge} label={t('card.averageLatency')} value={latencyLabel} color={brandColor} wide={isListLayout} />
                            <MetricTile icon={CircleCheckBig} label={t('card.successRate')} value={successRateLabel} color={brandColor} wide={isListLayout} />
                        </div>

                        <ChannelTagsRow model={model} maxVisible={isListLayout ? 4 : 3} density="desktop" keyPrefix={model.name} />
                    </div>
                </div>

                <AnimatePresence initial={false}>
                    {isExpanded ? (
                        <motion.div
                            id={detailRegionId}
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: 'auto', opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            transition={{ duration: 0.24 }}
                            className="overflow-hidden"
                        >
                            <div className="border-t border-border/20 pt-4">
                                <ModelItemDetail
                                    model={model}
                                    brandColor={brandColor}
                                    latencyUnit={latencyUnit}
                                    density="desktop"
                                    wideColumns={isListLayout}
                                />
                            </div>
                        </motion.div>
                    ) : null}
                </AnimatePresence>
            </div>

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
