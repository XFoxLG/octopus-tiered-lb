'use client';

import { useId, useState } from 'react';
import { useTranslations } from 'next-intl';
import { AnimatePresence, motion } from 'motion/react';
import { ChevronDown, Waypoints } from 'lucide-react';
import { useModelCapabilities } from '@/api/endpoints/model';
import { useNavStore } from '@/components/modules/navbar';
import { useSearchStore } from '@/components/modules/toolbar';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { Hint } from '@/components/ui/hint';
import { getModelIcon } from '@/lib/model-icons';
import { cn } from '@/lib/utils';
import {
    AUTO_ENDPOINT,
    buildEndpointGroups,
    type EndpointGroup,
} from '@/components/modules/apikey/endpoint-grouping';

// 单个分组默认渲染的模型芯片数上限；更多模型折叠进「展开更多」按钮，
// 避免大模型集合一次性渲染数百个带图标的芯片拖慢展开与重排。
const INITIAL_VISIBLE_MODELS = 24;

// 端点名 -> group.form.endpointType.options 下的 i18n key。
const ENDPOINT_LABEL_KEYS: Record<string, string> = {
    chat: 'chat',
    deepseek: 'deepseek',
    mimo: 'mimo',
    embeddings: 'embeddings',
    rerank: 'rerank',
    moderations: 'moderations',
    image_generation: 'imageGeneration',
    audio_speech: 'audioSpeech',
    audio_transcription: 'audioTranscription',
    video_generation: 'videoGeneration',
    music_generation: 'musicGeneration',
    search: 'search',
};

function ModelChip({ name }: { name: string }) {
    const { Avatar } = getModelIcon(name);
    return (
        <span
            title={name}
            className="flex min-w-0 max-w-full items-center gap-1.5 rounded-lg border border-border/40 bg-background/60 px-2 py-1 text-xs text-foreground"
        >
            <span className="grid size-4 shrink-0 place-items-center [&>svg]:!size-4">
                <Avatar size={16} />
            </span>
            <span className="min-w-0 truncate">{name}</span>
        </span>
    );
}

function EndpointCard({
    group,
    endpointLabel,
    defaultOpen,
    searchActive,
}: {
    group: EndpointGroup;
    endpointLabel: (endpoint: string) => string;
    defaultOpen: boolean;
    searchActive: boolean;
}) {
    const t = useTranslations('endpoints');
    const panelId = useId();
    // null 表示跟随默认展开策略（默认展开前 3 组；搜索时强制展开，
    // 修复搜索命中项落在默认折叠分组里不展示的问题）。
    // 用户点击后以用户选择为准，搜索期间也允许手动收起单个分组。
    const [expandedOverride, setExpandedOverride] = useState<boolean | null>(null);
    const open = expandedOverride ?? (defaultOpen || searchActive);
    const isAuto = group.endpoint === AUTO_ENDPOINT;
    const totalModels = group.models.length;
    const collapsible = totalModels > INITIAL_VISIBLE_MODELS;
    const [showAllModels, setShowAllModels] = useState(false);
    // 搜索命中的模型必须全部可见：若此时仍截断，「展开更多」会把命中项藏起来。
    const visibleModels = searchActive || showAllModels || !collapsible
        ? group.models
        : group.models.slice(0, INITIAL_VISIBLE_MODELS);
    const hiddenModelCount = totalModels - visibleModels.length;

    return (
        <div className="overflow-hidden rounded-2xl border border-border bg-card">
            <button
                type="button"
                onClick={() => setExpandedOverride(!open)}
                aria-expanded={open}
                aria-controls={panelId}
                className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40"
            >
                <span
                    className={cn(
                        'grid size-9 shrink-0 place-items-center rounded-xl',
                        isAuto ? 'bg-muted text-muted-foreground' : 'bg-primary/10 text-primary',
                    )}
                >
                    <Waypoints className="size-4" />
                </span>
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <span className="truncate text-sm font-semibold">{endpointLabel(group.endpoint)}</span>
                        {!isAuto ? (
                            <code className="shrink-0 rounded-md bg-muted/60 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                                {group.endpoint}
                            </code>
                        ) : null}
                    </div>
                    <div className="mt-0.5 text-[11px] text-muted-foreground">
                        {t('modelCount', { count: totalModels })}
                    </div>
                </div>
                <ChevronDown
                    className={cn('size-4 shrink-0 text-muted-foreground transition-transform duration-200', open && 'rotate-180')}
                />
            </button>

            <AnimatePresence initial={false}>
                {open ? (
                    <motion.div
                        key="body"
                        id={panelId}
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.22 }}
                        className="overflow-hidden"
                    >
                        <div className="flex flex-wrap gap-1.5 border-t border-border/30 px-4 py-3">
                            {visibleModels.map((model) => (
                                <ModelChip key={`${group.endpoint}-${model.name}`} name={model.name} />
                            ))}
                            {collapsible && !searchActive ? (
                                <button
                                    type="button"
                                    onClick={() => setShowAllModels((prev) => !prev)}
                                    aria-expanded={showAllModels}
                                    className="self-center rounded-lg border border-dashed border-border/60 px-2 py-1 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary"
                                >
                                    {showAllModels
                                        ? t('showLessModels')
                                        : t('showMoreModels', { count: hiddenModelCount })}
                                </button>
                            ) : null}
                        </div>
                    </motion.div>
                ) : null}
            </AnimatePresence>
        </div>
    );
}

export function EndpointsView() {
    const t = useTranslations('endpoints');
    const tCapability = useTranslations('group.form.endpointType.options');
    // keep-alive 会把切走的页面留在挂载状态：模块非激活时暂停能力查询，避免后台空转轮询。
    const moduleActive = useNavStore((s) => s.activeItem === 'model');
    const { data: capabilities, isLoading, error, refetch } = useModelCapabilities(moduleActive);
    // 与工具栏搜索框共用同一个 store（model 页搜索词），避免出现两个搜索入口。
    const searchTerm = useSearchStore((s) => s.getSearchTerm('model'));
    const searchActive = searchTerm.trim().length > 0;

    const endpointLabel = (endpoint: string) => {
        if (endpoint === AUTO_ENDPOINT) return t('autoEndpoint');
        const labelKey = ENDPOINT_LABEL_KEYS[endpoint];
        return labelKey ? tCapability(labelKey) : endpoint;
    };

    const groups = buildEndpointGroups(capabilities);

    const filteredGroups = searchActive
        ? groups
            .map((group) => {
                const term = searchTerm.trim().toLowerCase();
                const endpointMatches =
                    group.endpoint.toLowerCase().includes(term) ||
                    endpointLabel(group.endpoint).toLowerCase().includes(term);
                if (endpointMatches) return group;
                const models = group.models.filter((m) => m.name.toLowerCase().includes(term));
                return models.length > 0 ? { ...group, models } : null;
            })
            .filter((group): group is EndpointGroup => group !== null)
        : groups;

    if (isLoading) {
        return (
            <section className="rounded-2xl border border-border bg-card p-4">
                <LoadingState />
            </section>
        );
    }

    if (error && !capabilities) {
        return (
            <section className="rounded-2xl border border-border bg-card p-4">
                <ErrorState message={error.message} onRetry={() => refetch()} />
            </section>
        );
    }

    return (
        <div className="space-y-3">
            <section className="rounded-2xl border border-border bg-card p-4">
                <div className="flex flex-wrap items-center gap-2">
                    <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                        <Waypoints className="size-5" />
                        {t('title')}
                        <Hint text={t('hint')} />
                    </h2>
                    <span className="rounded-full bg-muted/60 px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
                        {t('endpointCount', { count: groups.length })}
                    </span>
                </div>
            </section>

            {groups.length === 0 ? (
                <div className="flex h-40 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-border text-sm text-muted-foreground">
                    <Waypoints className="size-8 opacity-40" />
                    {t('empty')}
                </div>
            ) : filteredGroups.length === 0 ? (
                <div className="flex h-40 items-center justify-center rounded-2xl border border-dashed border-border text-sm text-muted-foreground">
                    {t('noMatch')}
                </div>
            ) : (
                <div className="space-y-2.5">
                    {filteredGroups.map((group, index) => (
                        <EndpointCard
                            key={`${group.endpoint}-${searchTerm.trim()}`}
                            group={group}
                            endpointLabel={endpointLabel}
                            defaultOpen={index < 3}
                            searchActive={searchActive}
                        />
                    ))}
                </div>
            )}
        </div>
    );
}
