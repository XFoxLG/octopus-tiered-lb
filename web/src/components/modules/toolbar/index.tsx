'use client';

import { useState } from 'react';
import { ArrowUpAZ, Boxes, Clock3, Layers3, LayoutGrid, List, Plus, RadioTower, RefreshCw, Rows3, Search, SlidersHorizontal, X } from 'lucide-react';
import { motion, AnimatePresence, useReducedMotion } from 'motion/react';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
} from '@/components/ui/morphing-dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { buttonVariants } from '@/components/ui/button';
import { useModelMarket, useUpdateModelPrice } from '@/api/endpoints/model';
import { formatAverageLatency } from '@/components/modules/model/latency-format';
import { formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';
import { useNavStore, type NavItem } from '@/components/modules/navbar';
import { CreateDialogContent as ChannelCreateContent } from '@/components/modules/channel/Create';
import { ChannelGroupManagerDialog } from '@/components/modules/channel/GroupManager';
import { CreateDialogContent as GroupCreateContent } from '@/components/modules/group/Create';
import { CCSwitchLinkButton } from '@/components/modules/group/CCSwitchLinkButton';
import { MaintenanceButton } from '@/components/modules/group/MaintenanceButton';
import { CreateDialogContent as ModelCreateContent } from '@/components/modules/model/Create';
import { useModelViewStore } from '@/components/modules/model/view-store';
import { useTranslations } from 'next-intl';
import { useIsMobile } from '@/hooks/use-mobile';
import { useSearchStore } from './search-store';
import { TOOLBAR_ACTION_CLASS, TOOLBAR_ICON_ACTION_CLASS, TOOLBAR_TEXT_ACTION_CLASS, TOOLBAR_PRIMARY_ACTION_CLASS } from './action-styles';
import {
    useToolbarViewOptionsStore,
    TOOLBAR_PAGES,
    normalizeGroupFilterValue,
    type ToolbarPage,
    type ChannelFilter,
    type GroupFilter,
    type GroupViewMode,
    type ModelFilter,
    type ModelSortMode,
    type ModelLatencyUnit,
    type ToolbarSortField,
    type ToolbarSortOrder,
} from './view-options-store';

const CHANNEL_FILTER_OPTIONS: ChannelFilter[] = ['all', 'enabled', 'disabled'];
const GROUP_FILTER_OPTIONS: GroupFilter[] = ['all', 'with-members', 'empty', 'chat', 'deepseek', 'mimo', 'embeddings', 'rerank', 'moderations', 'image_generation', 'audio_speech', 'audio_transcription', 'video_generation', 'music_generation', 'search'];
const MODEL_FILTER_OPTIONS: ModelFilter[] = ['all', 'priced', 'free'];
const MODEL_SORT_OPTIONS: ModelSortMode[] = ['success-rate', 'request-count'];
const MODEL_LATENCY_UNIT_OPTIONS: ModelLatencyUnit[] = ['auto', 'ms', 's', 'h'];
type CombinedSortOption = {
    value: `${ToolbarSortField}-${ToolbarSortOrder}`;
    field: ToolbarSortField;
    order: ToolbarSortOrder;
    labelKey: string;
};
const COMBINED_SORT_OPTIONS: readonly CombinedSortOption[] = [
    { value: 'name-asc', field: 'name', order: 'asc', labelKey: 'popover.nameAsc' },
    { value: 'name-desc', field: 'name', order: 'desc', labelKey: 'popover.nameDesc' },
    { value: 'created-asc', field: 'created', order: 'asc', labelKey: 'popover.createdAsc' },
    { value: 'created-desc', field: 'created', order: 'desc', labelKey: 'popover.createdDesc' },
] as const;

const OPTION_BUTTON_CLASS = 'min-h-9 sm:h-9 rounded-md border px-3 text-xs font-medium transition-[color,background-color,border-color] duration-150 active:scale-[0.98] py-2 sm:py-0';
const ACTIVE_OPTION_CLASS = 'border-primary/30 bg-primary text-primary-foreground';
const INACTIVE_OPTION_CLASS = 'border-border bg-card text-foreground hover:border-primary/20 hover:bg-muted/50';

function isToolbarPage(item: NavItem): item is ToolbarPage {
    return (TOOLBAR_PAGES as readonly NavItem[]).includes(item);
}

function CreateDialogContent({ activeItem }: { activeItem: ToolbarPage }) {
    switch (activeItem) {
        case 'channel':
            return <ChannelCreateContent />;
        case 'group':
            return <GroupCreateContent />;
        case 'model':
            return <ModelCreateContent />;
    }
}

function getCreateDialogContentClassName(activeItem: ToolbarPage) {
    if (activeItem === 'group') {
        return 'h-[calc(100dvh-1rem)] w-[min(100vw-1rem,92rem)] max-w-none rounded-xl border border-border bg-card px-2 pt-2 pb-[calc(env(safe-area-inset-bottom)+0.75rem)] text-card-foreground shadow-lg flex flex-col overflow-hidden sm:max-w-none md:h-[calc(100dvh-2rem)] md:w-[min(100vw-2rem,92rem)] md:rounded-xl md:px-4 md:py-4';
    }

    if (activeItem === 'channel') {
        return 'flex h-[calc(100dvh-1rem)] max-h-[calc(100dvh-1rem)] w-[calc(100vw-1rem)] max-w-none flex-col overflow-hidden rounded-lg border border-border bg-card p-0 text-card-foreground shadow-lg sm:max-h-[calc(100dvh-3rem)] sm:max-w-none md:h-[min(52rem,calc(100dvh-3rem))] md:w-[min(100vw-3rem,64rem)]';
    }

    return 'w-[min(100vw-1rem,34rem)] max-w-full bg-card text-card-foreground px-4 py-4 rounded-xl max-h-[calc(100dvh-1rem)] flex flex-col overflow-hidden md:px-6 md:max-h-[calc(100dvh-2rem)]';
}

export function Toolbar() {
    const t = useTranslations('toolbar');
    const navT = useTranslations('navbar');
    const modelT = useTranslations('model');
    const endpointsT = useTranslations('endpoints');
    const isMobile = useIsMobile();
    const reduceMotion = useReducedMotion();
    const lightweightMotion = isMobile || reduceMotion;
    const { activeItem } = useNavStore();
    const toolbarItem = isToolbarPage(activeItem) ? activeItem : null;
    const modelView = useModelViewStore((s) => s.modelView);
    const setModelView = useModelViewStore((s) => s.setModelView);
    const searchTerm = useSearchStore((s) => (toolbarItem ? s.searchTerms[toolbarItem] || '' : ''));
    const setSearchTerm = useSearchStore((s) => s.setSearchTerm);
    const layout = useToolbarViewOptionsStore((s) => (toolbarItem ? s.getLayout(toolbarItem) : 'grid'));
    const sortField = useToolbarViewOptionsStore((s) =>
        toolbarItem === 'channel' || toolbarItem === 'group' ? s.getSortField(toolbarItem) : 'name'
    );
    const sortOrder = useToolbarViewOptionsStore((s) => (toolbarItem ? s.getSortOrder(toolbarItem) : 'asc'));
    const setLayout = useToolbarViewOptionsStore((s) => s.setLayout);
    const setSortConfig = useToolbarViewOptionsStore((s) => s.setSortConfig);
    const setSortOrder = useToolbarViewOptionsStore((s) => s.setSortOrder);
    const channelFilter = useToolbarViewOptionsStore((s) => s.channelFilter);
    const groupFilter = useToolbarViewOptionsStore((s) => normalizeGroupFilterValue(s.groupFilter));
    const modelFilter = useToolbarViewOptionsStore((s) => s.modelFilter);
    const groupViewMode = useToolbarViewOptionsStore((s) => s.groupViewMode);
    const modelSortMode = useToolbarViewOptionsStore((s) => s.modelSortMode);
    const modelLatencyUnit = useToolbarViewOptionsStore((s) => s.modelLatencyUnit);
    const setChannelFilter = useToolbarViewOptionsStore((s) => s.setChannelFilter);
    const setGroupFilter = useToolbarViewOptionsStore((s) => s.setGroupFilter);
    const setGroupViewMode = useToolbarViewOptionsStore((s) => s.setGroupViewMode);
    const setModelFilter = useToolbarViewOptionsStore((s) => s.setModelFilter);
    const setModelSortMode = useToolbarViewOptionsStore((s) => s.setModelSortMode);
    const setModelLatencyUnit = useToolbarViewOptionsStore((s) => s.setModelLatencyUnit);
    const [expandedSearchItem, setExpandedSearchItem] = useState<ToolbarPage | null>(null);
    const searchExpanded = expandedSearchItem === toolbarItem;
    const { data: modelMarket } = useModelMarket();
    const updateModelPrice = useUpdateModelPrice();
    const modelSummary = modelMarket?.summary ?? {
        model_count: 0,
        coverage_count: 0,
        unique_channel_count: 0,
        average_latency_ms: 0,
        last_update_time: '',
    };

    if (!toolbarItem) return null;
    const showLayoutOptions = toolbarItem !== 'group';
    const showCombinedSortOptions = toolbarItem === 'channel' || toolbarItem === 'group';
    const showSortOptions = toolbarItem !== 'model';

    const channelFilterLabelKeys: Record<ChannelFilter, string> = {
        all: 'popover.filter.channel.all',
        enabled: 'popover.filter.channel.enabled',
        disabled: 'popover.filter.channel.disabled',
    };
    const groupFilterLabelKeys: Record<GroupFilter, string> = {
        all: 'popover.filter.group.all',
        'with-members': 'popover.filter.group.withMembers',
        empty: 'popover.filter.group.empty',
        chat: 'popover.filter.group.chat',
        deepseek: 'popover.filter.group.deepseek',
        mimo: 'popover.filter.group.mimo',
        responses: 'popover.filter.group.chat',
        messages: 'popover.filter.group.chat',
        embeddings: 'popover.filter.group.embeddings',
        rerank: 'popover.filter.group.rerank',
        moderations: 'popover.filter.group.moderations',
        image_generation: 'popover.filter.group.imageGeneration',
        audio_speech: 'popover.filter.group.audioSpeech',
        audio_transcription: 'popover.filter.group.audioTranscription',
        video_generation: 'popover.filter.group.videoGeneration',
        music_generation: 'popover.filter.group.musicGeneration',
        search: 'popover.filter.group.search',
    };
    const modelFilterLabelKeys: Record<ModelFilter, string> = {
        all: 'popover.filter.model.all',
        priced: 'popover.filter.model.priced',
        free: 'popover.filter.model.free',
    };
    const groupViewLabelKeys: Record<GroupViewMode, string> = {
        cards: 'popover.filter.group.view.cards',
        grouped: 'popover.filter.group.view.grouped',
    };
    const modelSortLabelKeys: Record<ModelSortMode, string> = {
        'success-rate': 'popover.filter.model.sort.successRate',
        'request-count': 'popover.filter.model.sort.requestCount',
    };
    const modelLatencyUnitLabelKeys: Record<ModelLatencyUnit, string> = {
        auto: 'popover.filter.model.latencyUnit.auto',
        ms: 'popover.filter.model.latencyUnit.ms',
        s: 'popover.filter.model.latencyUnit.s',
        h: 'popover.filter.model.latencyUnit.h',
    };

    const filterOptions = toolbarItem === 'channel'
        ? CHANNEL_FILTER_OPTIONS.map((value) => ({
            value,
            label: t(channelFilterLabelKeys[value]),
        }))
        : toolbarItem === 'group'
            ? GROUP_FILTER_OPTIONS.map((value) => ({
                value,
                label: t(groupFilterLabelKeys[value]),
            }))
            : MODEL_FILTER_OPTIONS.map((value) => ({
                value,
                label: t(modelFilterLabelKeys[value]),
            }));

    const activeFilter = toolbarItem === 'channel'
        ? channelFilter
        : toolbarItem === 'group'
            ? groupFilter
            : modelFilter;
    const activePageLabel = navT(toolbarItem);
    const searchAriaLabel = `Search ${activePageLabel}`;
    const clearSearchAriaLabel = `Clear ${activePageLabel} search`;
    const createAriaLabel = `Create ${activePageLabel}`;


    const handleFilterChange = (value: string) => {
        switch (toolbarItem) {
            case 'channel':
                setChannelFilter(value as ChannelFilter);
                break;
            case 'group':
                setGroupFilter(normalizeGroupFilterValue(value));
                break;
            case 'model':
                setModelFilter(value as ModelFilter);
                break;
        }
    };

    return (
        <AnimatePresence mode="wait">
            <motion.div
                key="toolbar"
                initial={lightweightMotion ? { opacity: 0 } : { opacity: 0, scale: 0.9 }}
                animate={lightweightMotion ? { opacity: 1 } : { opacity: 1, scale: 1 }}
                exit={lightweightMotion ? { opacity: 0 } : { opacity: 0, scale: 0.9 }}
                transition={{ duration: lightweightMotion ? 0.12 : 0.2 }}
                className="flex max-w-full items-center gap-1 sm:gap-2 [&_.header-action-icon]:max-[380px]:!block [&_.header-action-icon]:max-[380px]:!size-4"
            >
                {/* 搜索按钮/展开框 */}
                <div
                    className={cn(
                        'relative h-9 shrink-0 transition-[width] duration-300 ease-out sm:h-11',
                        searchExpanded
                            ? 'w-[min(13rem,calc(100vw-12rem))] sm:w-44 lg:w-60'
                            : 'w-9 sm:w-11'
                    )}
                >
                    {!searchExpanded ? (
                        <motion.button
                            layoutId="search-box"
                            type="button"
                            aria-label={searchAriaLabel}
                            onClick={() => setExpandedSearchItem(toolbarItem)}
                            className={cn(
                                buttonVariants({ variant: "ghost", size: "icon" }),
                                "absolute inset-0",
                                TOOLBAR_ICON_ACTION_CLASS
                            )}
                        >
                            <motion.span layout="position"><Search className="header-action-icon size-4 transition-colors duration-300" /></motion.span>
                        </motion.button>
                    ) : (
                        <motion.div
                            layoutId="search-box"
                            className={cn(
                                "absolute inset-0 flex items-center gap-2 px-3.5",
                                TOOLBAR_ACTION_CLASS
                            )}
                            transition={lightweightMotion ? { duration: 0.12 } : { type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <motion.span layout="position"><Search className="header-action-icon size-4 text-muted-foreground shrink-0" /></motion.span>
                            <input
                                type="text"
                                aria-label={searchAriaLabel}
                                value={searchTerm}
                                onChange={(e) => setSearchTerm(toolbarItem, e.target.value)}
                                autoFocus
                                className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                            />
                            <button
                                type="button"
                                aria-label={clearSearchAriaLabel}
                                onClick={() => {
                                    setSearchTerm(toolbarItem, '');
                                    setExpandedSearchItem(null);
                                }}
                                className="grid size-7 shrink-0 place-items-center rounded-full border border-border/30 bg-card text-muted-foreground transition-colors hover:text-foreground sm:size-7 [-webkit-tap-highlight-color:transparent] before:absolute before:grid before:size-11 before:place-items-center before:rounded-full sm:before:size-7 relative"
                            >
                                <X className="size-3.5" />
                            </button>
                        </motion.div>
                    )}
                </div>

                {toolbarItem === 'group' && (
                    <MaintenanceButton className="hidden sm:inline-flex" />
                )}
                {toolbarItem === 'group' && (
                    <CCSwitchLinkButton className="hidden sm:inline-flex" />
                )}
                <div className="flex shrink-0 items-center">
                    <Popover>
                        <PopoverTrigger asChild>
                            <button
                                type="button"
                                aria-label={t('popover.ariaLabel')}
                                className={cn(
                                    buttonVariants({ variant: 'ghost', size: 'default' }),
                                    TOOLBAR_TEXT_ACTION_CLASS,
                                    "w-9 px-0 sm:w-auto sm:px-3.5"
                                )}
                            >
                                <SlidersHorizontal className="header-action-icon size-4 transition-colors duration-300" />
                                <span className="hidden sm:inline">{t('popover.filter.title')}</span>
                            </button>
                        </PopoverTrigger>
                        <PopoverContent
                            align="end"
                            side={isMobile ? "top" : "bottom"}
                            sideOffset={isMobile ? 8 : 12}
                            className={cn(
                                "max-w-[calc(100vw-1rem)] rounded-xl border border-border bg-popover p-3 shadow-md",
                                toolbarItem === 'model' ? "w-[min(100vw-1rem,40rem)]" : "w-72"
                            )}
                        >
                            <div className="grid gap-3">
                                {toolbarItem === 'model' && (() => {
                                    const lastUpdateRaw = modelSummary.last_update_time;
                                    const formatted = lastUpdateRaw ? formatDateTime(lastUpdateRaw) : '-';
                                    const lastUpdateLabel = formatted !== '-' && lastUpdateRaw && new Date(lastUpdateRaw).getFullYear() > 1
                                        ? formatted
                                        : modelT('summary.neverUpdated');
                                    const hasData = modelSummary.model_count > 0;
                                    const summaryMetrics = [
                                        { key: 'models', icon: Boxes, label: modelT('summary.modelCount'), value: modelSummary.model_count.toLocaleString() },
                                        { key: 'coverage', icon: Rows3, label: modelT('summary.coverage'), value: modelSummary.coverage_count.toLocaleString() },
                                        { key: 'unique', icon: RadioTower, label: modelT('summary.uniqueChannels'), value: modelSummary.unique_channel_count.toLocaleString() },
                                        { key: 'latency', icon: Clock3, label: modelT('summary.averageLatency'), value: formatAverageLatency(modelSummary.average_latency_ms, hasData ? 1 : 0, 'auto') },
                                    ];
                                    return (
                                        <div className="grid gap-2 rounded-lg border border-border bg-muted/20 p-2.5">
                                            <div className="flex flex-col gap-1.5 sm:flex-row sm:items-center sm:justify-between">
                                                <div className="min-w-0">
                                                    <div className="text-xs font-semibold text-foreground sm:text-sm">{modelT('summary.title')}</div>
                                                    <div className="text-[0.65rem] text-muted-foreground sm:text-[11px]">{modelT('summary.description')}</div>
                                                </div>
                                                <button
                                                    type="button"
                                                    onClick={() => updateModelPrice.mutate()}
                                                    disabled={updateModelPrice.isPending}
                                                    className={cn(
                                                        OPTION_BUTTON_CLASS,
                                                        'inline-flex items-center gap-1.5 border-border/30 bg-card text-foreground hover:border-border hover:bg-muted',
                                                    )}
                                                >
                                                    <RefreshCw className={cn('size-3.5', updateModelPrice.isPending && 'animate-spin')} />
                                                    {updateModelPrice.isPending ? modelT('summary.refreshing') : modelT('summary.refresh')}
                                                </button>
                                            </div>
                                            <div className="flex items-center gap-1.5 rounded-lg border border-border/30 bg-card px-2.5 py-1.5 text-[0.65rem] text-muted-foreground sm:gap-2 sm:px-3 sm:py-2 sm:text-[11px]">
                                                <Clock3 className="size-3.5 shrink-0 text-primary sm:size-4" />
                                                <span className="min-w-0 truncate">{modelT('summary.lastUpdate')}: {lastUpdateLabel}</span>
                                            </div>
                                            <div className="grid grid-cols-2 gap-1.5 sm:gap-2 lg:grid-cols-4">
                                                {summaryMetrics.map((m) => (
                                                    <div key={m.key} className="min-w-0 overflow-hidden rounded-lg border border-border/30 bg-card px-2 py-1.5 sm:px-2.5 sm:py-2">
                                                        <div className="flex min-w-0 items-center gap-1 text-[0.6rem] text-muted-foreground sm:gap-1.5 sm:text-[10px]">
                                                            <m.icon className="size-3 shrink-0 text-primary sm:size-3.5" />
                                                            <span className="min-w-0 truncate leading-tight">{m.label}</span>
                                                        </div>
                                                        <div className="mt-0.5 min-w-0 truncate text-[1.15rem] font-semibold leading-none tracking-tight sm:mt-1 sm:text-[1.45rem]">{m.value}</div>
                                                    </div>
                                                ))}
                                            </div>
                                        </div>
                                    );
                                })()}

                                {showLayoutOptions && (
                                    <div className="grid gap-2 rounded-lg border border-border bg-muted/20 p-2.5">
                                        <p className="text-xs font-semibold text-muted-foreground">{t('popover.layout')}</p>
                                        <div className="grid grid-cols-3 gap-2">
                                            <button
                                                type="button"
                                                onClick={() => setLayout(toolbarItem, 'grid')}
                                                className={cn(
                                                    OPTION_BUTTON_CLASS,
                                                    'inline-flex items-center justify-center gap-1.5',
                                                    layout === 'grid' ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                )}
                                            >
                                                <LayoutGrid className="size-3.5" />
                                                {t('popover.grid')}
                                            </button>
                                            <button
                                                type="button"
                                                onClick={() => setLayout(toolbarItem, 'list')}
                                                className={cn(
                                                    OPTION_BUTTON_CLASS,
                                                    'inline-flex items-center justify-center gap-1.5',
                                                    layout === 'list' ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                )}
                                            >
                                                <List className="size-3.5" />
                                                {t('popover.list')}
                                            </button>
                                            <button
                                                type="button"
                                                onClick={() => setLayout(toolbarItem, 'compact')}
                                                className={cn(
                                                    OPTION_BUTTON_CLASS,
                                                    'inline-flex items-center justify-center gap-1.5',
                                                    layout === 'compact' ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                )}
                                            >
                                                <Rows3 className="size-3.5" />
                                                {t('popover.compact')}
                                            </button>
                                        </div>
                                    </div>
                                )}

                                {showSortOptions && (
                                    <div className="grid gap-2 rounded-lg border border-border bg-card p-2.5">
                                        <p className="text-xs font-semibold text-muted-foreground">{t('popover.sort')}</p>
                                        {showCombinedSortOptions ? (
                                            <div className="grid grid-cols-2 gap-2">
                                                {COMBINED_SORT_OPTIONS.map((option) => (
                                                    <button
                                                        key={option.value}
                                                        type="button"
                                                        onClick={() => {
                                                            if (toolbarItem === 'channel' || toolbarItem === 'group') {
                                                                setSortConfig(toolbarItem, option.field, option.order);
                                                            }
                                                        }}
                                                        className={cn(
                                                            OPTION_BUTTON_CLASS,
                                                            'inline-flex items-center justify-center gap-1.5',
                                                            sortField === option.field && sortOrder === option.order
                                                                ? ACTIVE_OPTION_CLASS
                                                                : INACTIVE_OPTION_CLASS
                                                        )}
                                                    >
                                                        {option.field === 'name' ? <ArrowUpAZ className="size-3.5" /> : <Clock3 className="size-3.5" />}
                                                        {t(option.labelKey)}
                                                    </button>
                                                ))}
                                            </div>
                                        ) : (
                                            <div className="grid grid-cols-2 gap-2">
                                                <button
                                                    type="button"
                                                    onClick={() => setSortOrder(toolbarItem, 'asc')}
                                                    className={cn(
                                                        OPTION_BUTTON_CLASS,
                                                        'inline-flex items-center justify-center gap-1.5',
                                                        sortOrder === 'asc' ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                    )}
                                                >
                                                    <ArrowUpAZ className="size-3.5" />
                                                    {t('popover.nameAsc')}
                                                </button>
                                                <button
                                                    type="button"
                                                    onClick={() => setSortOrder(toolbarItem, 'desc')}
                                                    className={cn(
                                                        OPTION_BUTTON_CLASS,
                                                        'inline-flex items-center justify-center gap-1.5',
                                                        sortOrder === 'desc' ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                    )}
                                                >
                                                    <ArrowUpAZ className="size-3.5" />
                                                    {t('popover.nameDesc')}
                                                </button>
                                            </div>
                                        )}
                                    </div>
                                )}

                                <div className="grid gap-2 rounded-lg border border-border bg-card p-2.5">
                                    <p className="text-xs font-semibold text-muted-foreground">{t('popover.filter.title')}</p>
                                    <div className="grid max-h-72 gap-2 overflow-y-auto pr-1">
                                        {toolbarItem === 'model' && (
                                            <>
                                                <div className="grid gap-2 rounded-lg border border-border bg-muted/14 p-2">
                                                    <p className="text-[11px] font-semibold text-muted-foreground">{t('popover.filter.model.sort.title')}</p>
                                                    {MODEL_SORT_OPTIONS.map((option) => (
                                                        <button
                                                            key={option}
                                                            type="button"
                                                            onClick={() => setModelSortMode(option)}
                                                            className={cn(
                                                                OPTION_BUTTON_CLASS,
                                                                'text-left',
                                                                modelSortMode === option ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                            )}
                                                        >
                                                            {t(modelSortLabelKeys[option])}
                                                        </button>
                                                    ))}
                                                </div>
                                                <div className="grid gap-2 rounded-lg border border-border bg-muted/14 p-2">
                                                    <p className="text-[11px] font-semibold text-muted-foreground">{t('popover.filter.model.latencyUnit.title')}</p>
                                                    <div className="grid grid-cols-2 gap-2">
                                                        {MODEL_LATENCY_UNIT_OPTIONS.map((option) => (
                                                            <button
                                                                key={option}
                                                                type="button"
                                                                onClick={() => setModelLatencyUnit(option)}
                                                                className={cn(
                                                                    OPTION_BUTTON_CLASS,
                                                                    'text-center',
                                                                    modelLatencyUnit === option ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                                )}
                                                            >
                                                                {t(modelLatencyUnitLabelKeys[option])}
                                                            </button>
                                                        ))}
                                                    </div>
                                                </div>
                                            </>
                                        )}
                                        {toolbarItem === 'group' && (
                                            <div className="grid gap-2 rounded-lg border border-border bg-muted/14 p-2">
                                                <p className="text-[11px] font-semibold text-muted-foreground">{t('popover.filter.group.view.title')}</p>
                                                <div className="grid grid-cols-2 gap-2">
                                                    {(['cards', 'grouped'] as const).map((option) => (
                                                        <button
                                                            key={option}
                                                            type="button"
                                                            onClick={() => setGroupViewMode(option)}
                                                            aria-pressed={groupViewMode === option}
                                                            className={cn(
                                                                OPTION_BUTTON_CLASS,
                                                                'inline-flex items-center justify-center gap-1.5 text-center',
                                                                groupViewMode === option ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS,
                                                            )}
                                                        >
                                                            {option === 'grouped' ? <Layers3 className="size-3.5" /> : <List className="size-3.5" />}
                                                            {t(groupViewLabelKeys[option])}
                                                        </button>
                                                    ))}
                                                </div>
                                            </div>
                                        )}
                                        {filterOptions.map((option) => (
                                            <button
                                                key={option.value}
                                                type="button"
                                                onClick={() => handleFilterChange(option.value)}
                                                className={cn(
                                                    OPTION_BUTTON_CLASS,
                                                    'text-left',
                                                    activeFilter === option.value ? ACTIVE_OPTION_CLASS : INACTIVE_OPTION_CLASS
                                                )}
                                            >
                                                {option.label}
                                            </button>
                                        ))}
                                    </div>
                                </div>
                            </div>
                        </PopoverContent>
                    </Popover>
                </div>

                <div className="flex items-center gap-1 sm:gap-1.5">
                    {toolbarItem === 'channel' ? (
                        <ChannelGroupManagerDialog className={cn(
                            buttonVariants({ variant: "ghost", size: "default" }),
                            TOOLBAR_TEXT_ACTION_CLASS,
                            "hidden sm:flex"
                        )} />
                    ) : null}

                    {/* 模型广场 / 可用端点 切换开关：仅模型页显示，置于添加模型按钮左侧 */}
                    {toolbarItem === 'model' ? (
                        <div className="flex items-center gap-0.5 rounded-lg border border-border bg-card p-0.5 sm:gap-1 sm:p-1">
                            <button
                                type="button"
                                onClick={() => setModelView('market')}
                                aria-pressed={modelView === 'market'}
                                className={cn(
                                    'rounded-md px-3 py-1.5 text-xs font-medium transition-colors sm:px-4 sm:py-2 sm:text-sm',
                                    modelView === 'market'
                                        ? 'bg-primary text-primary-foreground shadow-sm'
                                        : 'text-muted-foreground hover:text-foreground',
                                )}
                            >
                                {modelT('marketTitle')}
                            </button>
                            <button
                                type="button"
                                onClick={() => setModelView('endpoints')}
                                aria-pressed={modelView === 'endpoints'}
                                className={cn(
                                    'rounded-md px-3 py-1.5 text-xs font-medium transition-colors sm:px-4 sm:py-2 sm:text-sm',
                                    modelView === 'endpoints'
                                        ? 'bg-primary text-primary-foreground shadow-sm'
                                        : 'text-muted-foreground hover:text-foreground',
                                )}
                            >
                                {endpointsT('title')}
                            </button>
                            <button
                                type="button"
                                onClick={() => setModelView('categories')}
                                aria-pressed={modelView === 'categories'}
                                className={cn(
                                    'rounded-md px-3 py-1.5 text-xs font-medium transition-colors sm:px-4 sm:py-2 sm:text-sm',
                                    modelView === 'categories'
                                        ? 'bg-primary text-primary-foreground shadow-sm'
                                        : 'text-muted-foreground hover:text-foreground',
                                )}
                            >
                                {modelT('priceCategory.title')}
                            </button>
                        </div>
                    ) : null}

                    {/* 创建按钮 */}
                    <MorphingDialog>
                        <MorphingDialogTrigger
                            ariaLabel={createAriaLabel}
                            className={cn(
                                buttonVariants({ variant: "default", size: "icon" }),
                                TOOLBAR_PRIMARY_ACTION_CLASS
                            )}
                        >
                            <Plus className="header-action-icon size-4 transition-colors duration-300" />
                        </MorphingDialogTrigger>

                        <MorphingDialogContainer>
                            <MorphingDialogContent
                                className={getCreateDialogContentClassName(toolbarItem)}
                                dismissOnOverlayClick={toolbarItem !== 'channel'}
                            >
                                <CreateDialogContent activeItem={toolbarItem} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>
                </div>
            </motion.div>
        </AnimatePresence>
    );
}

export { useSearchStore } from './search-store';
export { normalizeGroupFilterValue, useToolbarViewOptionsStore } from './view-options-store';


