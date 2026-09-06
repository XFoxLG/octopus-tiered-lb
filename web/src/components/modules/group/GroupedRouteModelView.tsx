'use client';

import { useMemo, useState } from 'react';
import { ChevronDown, FolderTree, Layers3, Waves } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import type {
    GroupedRouteModelBucket,
    GroupedRouteModelCategoryBucket,
    GroupedRouteModelRow,
} from './grouped-view';
import { UNGROUPED_BUCKET_ID, UNCATEGORIZED_CATEGORY_ID } from './grouped-view';

function RouteModelRow({ model, index }: { model: GroupedRouteModelRow; index: number }) {
    const { Avatar: ModelAvatar } = getModelIcon(model.name);

    return (
        <div
            className={cn(
                'flex min-w-0 items-center gap-2 rounded-lg border border-border/30 bg-card px-3 py-2 transition-colors hover:border-primary/16 hover:bg-muted/30',
                !model.enabled && 'opacity-60 grayscale',
            )}
        >
            <span className="grid size-7 shrink-0 place-items-center rounded-md bg-primary/10 text-xs font-semibold text-primary">
                {index + 1}
            </span>
            <span className="shrink-0">
                <ModelAvatar size={18} />
            </span>
                <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 items-center gap-1.5">
                        <div className="min-w-0 max-w-[55%] truncate text-sm font-medium text-foreground">{model.name}</div>
                    </div>
                    <div className="truncate text-xs text-muted-foreground">{model.channel_name}</div>
            </div>
            {typeof model.weight === 'number' ? (
                <span className="shrink-0 rounded-md border border-border/30 bg-muted/30 px-2 py-1 text-[11px] font-medium text-muted-foreground">
                    {model.weight}
                </span>
            ) : null}
        </div>
    );
}

function GroupBucketHeader({
    bucket,
    expanded,
    onToggle,
}: {
    bucket: GroupedRouteModelBucket;
    expanded: boolean;
    onToggle: () => void;
}) {
    const t = useTranslations('group');
    const description = bucket.endpoint_type
        ? t('card.endpointType', { value: bucket.endpoint_type })
        : null;

    return (
        <button
            type="button"
            onClick={onToggle}
            aria-expanded={expanded}
            className="flex w-full min-w-0 items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-muted/30 md:px-5"
        >
            <span className="grid size-7 shrink-0 place-items-center rounded-md border border-border/40 bg-muted/30 text-muted-foreground">
                <Waves className="size-3.5" />
            </span>
            <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-semibold text-foreground">{bucket.name}</span>
                {description ? (
                    <span className="mt-0.5 block truncate text-xs text-muted-foreground">{description}</span>
                ) : null}
            </span>
            <span className="shrink-0 rounded-full border border-border/40 bg-card px-2 py-1 text-[11px] font-medium text-muted-foreground">
                {t('groupedView.modelCount', { count: bucket.models.length })}
            </span>
            <ChevronDown
                className={cn('size-4 shrink-0 text-muted-foreground transition-transform', expanded && 'rotate-180')}
            />
        </button>
    );
}

function CategoryHeader({
    category,
    expanded,
    onToggle,
}: {
    category: GroupedRouteModelCategoryBucket;
    expanded: boolean;
    onToggle: () => void;
}) {
    const t = useTranslations('group');
    const isUngrouped = category.kind === 'ungrouped';
    const isUncategorized = category.kind === 'uncategorized';
    const title = isUngrouped
        ? t('groupedView.ungrouped')
        : isUncategorized
            ? t('groupedView.uncategorized')
            : category.name;
    const description = isUngrouped
        ? t('groupedView.ungroupedDescription')
        : isUncategorized
            ? t('groupedView.uncategorizedDescription')
            : t('groupedView.categoryDescription', { count: category.buckets.length });
    const icon = isUngrouped ? (
        <Layers3 className="size-4" />
    ) : (
        <FolderTree className="size-4" />
    );

    return (
        <button
            type="button"
            onClick={onToggle}
            aria-expanded={expanded}
            className="flex w-full min-w-0 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/30 md:px-5 md:py-4"
        >
            <span className="grid size-9 shrink-0 place-items-center rounded-lg border border-border/40 bg-muted/30 text-muted-foreground">
                {icon}
            </span>
            <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-semibold text-foreground md:text-base">{title}</span>
                {description ? (
                    <span className="mt-0.5 block truncate text-xs text-muted-foreground">{description}</span>
                ) : null}
            </span>
            <span className="shrink-0 rounded-full border border-border/40 bg-card px-2 py-1 text-[11px] font-medium text-muted-foreground">
                {t('groupedView.modelCount', { count: category.totalModelCount })}
            </span>
            <ChevronDown
                className={cn('size-4 shrink-0 text-muted-foreground transition-transform', expanded && 'rotate-180')}
            />
        </button>
    );
}

function CategoryBody({ category, expandedKeys, toggleKey }: {
    category: GroupedRouteModelCategoryBucket;
    expandedKeys: Set<string>;
    toggleKey: (key: string) => void;
}) {
    const t = useTranslations('group');

    // 未分组分类：直接渲染扁平模型列表。
    if (category.kind === 'ungrouped') {
        const models = category.models ?? [];
        return (
            <div className="border-t border-border/40 bg-muted/10 px-3 py-3 md:px-4">
                {models.length > 0 ? (
                    <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                        {models.map((model, index) => (
                            <RouteModelRow key={model.id} model={model} index={index} />
                        ))}
                    </div>
                ) : (
                    <div className="rounded-lg border border-border/30 bg-card px-3 py-4 text-sm text-muted-foreground">
                        {t('groupedView.emptyGroup')}
                    </div>
                )}
            </div>
        );
    }

    // 分类/未分类：渲染分组桶列表，每个桶可独立折叠。
    return (
        <div className="border-t border-border/40 bg-muted/10 px-2 py-2 md:px-3">
            <div className="grid gap-2">
                {category.buckets.map((bucket) => {
                    const expanded = expandedKeys.has(bucket.key);
                    return (
                        <article
                            key={bucket.key}
                            className="overflow-hidden rounded-lg border border-border/40 bg-card text-card-foreground"
                        >
                            <GroupBucketHeader
                                bucket={bucket}
                                expanded={expanded}
                                onToggle={() => toggleKey(bucket.key)}
                            />
                            {expanded ? (
                                <div className="border-t border-border/30 bg-muted/10 px-3 py-3 md:px-4">
                                    {bucket.models.length > 0 ? (
                                        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                                            {bucket.models.map((model, index) => (
                                                <RouteModelRow key={model.id} model={model} index={index} />
                                            ))}
                                        </div>
                                    ) : (
                                        <div className="rounded-lg border border-border/30 bg-card px-3 py-4 text-sm text-muted-foreground">
                                            {t('groupedView.emptyGroup')}
                                        </div>
                                    )}
                                </div>
                            ) : null}
                        </article>
                    );
                })}
            </div>
        </div>
    );
}

export function GroupedRouteModelView({ categories }: { categories: GroupedRouteModelCategoryBucket[] }) {
    const t = useTranslations('group');
    // 默认展开前 2 个分类，以及每个分类下前 2 个分组。
    const initialExpanded = useMemo(() => {
        const keys = new Set<string>();
        for (const category of categories.slice(0, 2)) {
            keys.add(category.id);
            for (const bucket of category.buckets.slice(0, 2)) {
                keys.add(bucket.key);
            }
        }
        return keys;
    }, [categories]);
    const [expandedKeys, setExpandedKeys] = useState<Set<string>>(initialExpanded);

    if (categories.length === 0) {
        return (
            <div className="flex h-full min-h-0 items-center justify-center p-6">
                <div className="max-w-md rounded-xl border border-border bg-card p-6 text-center text-card-foreground">
                    <div className="text-sm font-semibold">{t('groupedView.emptyTitle')}</div>
                    <div className="mt-2 text-sm text-muted-foreground">{t('groupedView.emptyDescription')}</div>
                </div>
            </div>
        );
    }

    const toggleKey = (key: string) => {
        setExpandedKeys((current) => {
            const next = new Set(current);
            if (next.has(key)) {
                next.delete(key);
            } else {
                next.add(key);
            }
            return next;
        });
    };

    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-xl px-3 pb-3 md:px-4 md:pb-4">
            <div className="grid gap-3 py-3 md:py-4">
                {categories.map((category) => {
                    const expanded = expandedKeys.has(category.id);
                    return (
                        <article
                            key={category.id}
                            className={cn(
                                'overflow-hidden rounded-xl border border-border bg-card text-card-foreground shadow-sm',
                                (category.id === UNGROUPED_BUCKET_ID || category.id === UNCATEGORIZED_CATEGORY_ID) &&
                                    'border-dashed',
                            )}
                        >
                            <CategoryHeader
                                category={category}
                                expanded={expanded}
                                onToggle={() => toggleKey(category.id)}
                            />
                            {expanded ? (
                                <CategoryBody
                                    category={category}
                                    expandedKeys={expandedKeys}
                                    toggleKey={toggleKey}
                                />
                            ) : null}
                        </article>
                    );
                })}
            </div>
        </div>
    );
}
