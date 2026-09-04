'use client';

import { useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { AnimatePresence, motion } from 'motion/react';
import { ChevronDown, Search, Waypoints } from 'lucide-react';
import { useModelCapabilities, type ModelCapability } from '@/api/endpoints/model';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { Input } from '@/components/ui/input';
import { Hint } from '@/components/ui/hint';
import { getModelIcon } from '@/lib/model-icons';
import { cn } from '@/lib/utils';
import {
    AUTO_ENDPOINT,
    buildEndpointGroups,
    type EndpointGroup,
} from '@/components/modules/apikey/endpoint-grouping';

function ModelChip({ name }: { name: string }) {
    const { Avatar } = getModelIcon(name);
    return (
        <span className="inline-flex items-center gap-1.5 rounded-lg border border-border/40 bg-background/60 px-2 py-1 text-xs text-foreground">
            <span className="grid size-4 shrink-0 place-items-center [&>svg]:!size-4">
                <Avatar size={16} />
            </span>
            <span className="truncate">{name}</span>
        </span>
    );
}

function EndpointCard({
    group,
    endpointLabel,
    defaultOpen,
}: {
    group: EndpointGroup;
    endpointLabel: (endpoint: string) => string;
    defaultOpen: boolean;
}) {
    const t = useTranslations('endpoints');
    const [open, setOpen] = useState(defaultOpen);
    const isAuto = group.endpoint === AUTO_ENDPOINT;

    return (
        <div className="overflow-hidden rounded-2xl border border-border bg-card">
            <button
                type="button"
                onClick={() => setOpen((prev) => !prev)}
                aria-expanded={open}
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
                        {t('modelCount', { count: group.models.length })}
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
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.22 }}
                        className="overflow-hidden"
                    >
                        <div className="flex flex-wrap gap-1.5 border-t border-border/30 px-4 py-3">
                            {group.models.map((model) => (
                                <ModelChip key={`${group.endpoint}-${model.name}`} name={model.name} />
                            ))}
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
    const { data: capabilities, isLoading, error, refetch } = useModelCapabilities();
    const [search, setSearch] = useState('');

    const endpointLabel = useMemo(() => {
        const labelMap: Record<string, string> = {
            chat: tCapability('chat'),
            deepseek: tCapability('deepseek'),
            mimo: tCapability('mimo'),
            embeddings: tCapability('embeddings'),
            rerank: tCapability('rerank'),
            moderations: tCapability('moderations'),
            image_generation: tCapability('imageGeneration'),
            audio_speech: tCapability('audioSpeech'),
            audio_transcription: tCapability('audioTranscription'),
            video_generation: tCapability('videoGeneration'),
            music_generation: tCapability('musicGeneration'),
            search: tCapability('search'),
        };
        return (endpoint: string) => {
            if (endpoint === AUTO_ENDPOINT) return t('autoEndpoint');
            return labelMap[endpoint] ?? endpoint;
        };
    }, [t, tCapability]);

    const groups = useMemo(() => buildEndpointGroups(capabilities), [capabilities]);

    const filteredGroups = useMemo(() => {
        const term = search.trim().toLowerCase();
        if (!term) return groups;
        return groups
            .map((group) => {
                const endpointMatches =
                    group.endpoint.toLowerCase().includes(term) ||
                    endpointLabel(group.endpoint).toLowerCase().includes(term);
                if (endpointMatches) return group;
                const models = group.models.filter((m) => m.name.toLowerCase().includes(term));
                return models.length > 0 ? { ...group, models } : null;
            })
            .filter((group): group is EndpointGroup => group !== null);
    }, [groups, search, endpointLabel]);

    if (isLoading) {
        return (
            <section className="rounded-2xl border border-border bg-card p-4">
                <LoadingState />
            </section>
        );
    }

    if (error) {
        return (
            <section className="rounded-2xl border border-border bg-card p-4">
                <ErrorState message={error.message} onRetry={() => refetch()} />
            </section>
        );
    }

    return (
        <div className="space-y-3">
            <section className="rounded-2xl border border-border bg-card p-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div className="flex items-center gap-2">
                        <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                            <Waypoints className="size-5" />
                            {t('title')}
                            <Hint text={t('hint')} />
                        </h2>
                        <span className="rounded-full bg-muted/60 px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
                            {t('endpointCount', { count: groups.length })}
                        </span>
                    </div>
                    <div className="relative sm:w-64">
                        <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                        <Input
                            type="text"
                            value={search}
                            onChange={(e) => setSearch(e.target.value)}
                            placeholder={t('searchPlaceholder')}
                            className="h-9 rounded-xl pl-9 text-sm"
                        />
                    </div>
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
                            key={group.endpoint}
                            group={group}
                            endpointLabel={endpointLabel}
                            defaultOpen={index < 3}
                        />
                    ))}
                </div>
            )}
        </div>
    );
}
