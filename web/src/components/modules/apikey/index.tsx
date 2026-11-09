'use client';

import { NonChatStats } from '@/components/common/NonChatStats';

import { useCallback, useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { AnimatePresence, motion } from 'motion/react';
import {
    KeyRound,
    Plus,
    Search,
    Tag,
    Pencil,
    Trash2,
    Info,
    Loader,
    X,
    Check,
    FilterX,
    CircleSlash,
    AlertTriangle,
} from 'lucide-react';
import { PageWrapper } from '@/components/common/PageWrapper';
import { Input } from '@/components/ui/input';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import {
    useAPIKeyList,
    useCreateAPIKey,
    useUpdateAPIKey,
    useDeleteAPIKey,
    isLegacyHashedAPIKey,
    type APIKey,
} from '@/api/endpoints/apikey';
import { useStatsAPIKey, type StatsAPIKeyFormatted } from '@/api/endpoints/stats';
import { APIKeyForm, parseTags } from '@/components/modules/setting/APIKey';
import { CopyIconButton } from '@/components/common/CopyButton';
import { toast } from '@/components/common/Toast';
import { cn } from '@/lib/utils';
import type { ApiError } from '@/api/types';

const UNTAGGED = '__untagged__';

function formatExpire(expireAt: number | undefined): { label: string; expired: boolean } | null {
    if (!expireAt) return null;
    const d = new Date(expireAt * 1000);
    if (isNaN(d.getTime())) return null;
    return {
        label: d.toLocaleDateString(),
        expired: d.getTime() < Date.now(),
    };
}

function StatChip({ label, value }: { label: string; value: string }) {
    return (
        <div className="min-w-0 rounded-lg bg-muted/40 px-2.5 py-1.5">
            <div className="truncate text-[10px] uppercase tracking-wide text-muted-foreground">{label}</div>
            <div className="truncate text-sm font-semibold tabular-nums">{value}</div>
        </div>
    );
}

function APIKeyCard({
    apiKey,
    stats,
    onEdit,
    onDelete,
    isDeleting,
}: {
    apiKey: APIKey;
    stats?: StatsAPIKeyFormatted;
    onEdit: () => void;
    onDelete: () => void;
    isDeleting: boolean;
}) {
    const t = useTranslations('setting');
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [showStats, setShowStats] = useState(false);

    const tags = useMemo(() => parseTags(apiKey.tags), [apiKey.tags]);
    const expire = formatExpire(apiKey.expire_at);
    const isLegacyHashed = isLegacyHashedAPIKey(apiKey.api_key);

    const requestTotal = stats
        ? stats.request_success.raw + stats.request_failed.raw
        : 0;
    const successRate = stats && requestTotal > 0
        ? ((stats.request_success.raw / requestTotal) * 100).toFixed(1)
        : null;
    const totalTokens = stats
        ? stats.total_token.raw
        : 0;
    const totalCost = stats
        ? stats.input_cost.raw + stats.output_cost.raw
        : 0;

    return (
        <motion.div
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.97 }}
            transition={{ type: 'spring', stiffness: 400, damping: 32 }}
            className={cn(
                'group relative flex flex-col gap-3 overflow-hidden rounded-2xl border bg-card p-4 transition-colors',
                apiKey.enabled ? 'border-border' : 'border-border/50 bg-muted/20',
            )}
        >
            <div className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                    <span
                        className={cn(
                            'grid size-9 shrink-0 place-items-center rounded-xl',
                            apiKey.enabled ? 'bg-primary/10 text-primary' : 'bg-muted text-muted-foreground',
                        )}
                    >
                        <KeyRound className="size-4" />
                    </span>
                    <div className="min-w-0">
                        <div className="truncate text-sm font-semibold leading-tight">{apiKey.name}</div>
                        <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                            <span
                                className={cn(
                                    'inline-flex items-center gap-1',
                                    apiKey.enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground',
                                )}
                            >
                                <span className={cn('size-1.5 rounded-full', apiKey.enabled ? 'bg-emerald-500' : 'bg-muted-foreground/50')} />
                                {apiKey.enabled ? t('apiKey.page.statusEnabled') : t('apiKey.page.statusDisabled')}
                            </span>
                            {expire ? (
                                <span className={cn(expire.expired && 'text-destructive')}>
                                    · {expire.expired ? t('apiKey.page.expired') : t('apiKey.page.expires')} {expire.label}
                                </span>
                            ) : (
                                <span>· {t('apiKey.page.neverExpires')}</span>
                            )}
                        </div>
                    </div>
                </div>

                <div className="flex shrink-0 items-center gap-1">
                    <button
                        type="button"
                        onClick={() => setShowStats((s) => !s)}
                        className={cn(
                            'grid size-8 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground',
                            showStats && 'bg-muted text-foreground',
                        )}
                        title={t('apiKey.page.viewStats')}
                    >
                        <Info className="size-4" />
                    </button>
                    <button
                        type="button"
                        onClick={onEdit}
                        className="grid size-8 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                        title={t('apiKey.page.edit')}
                    >
                        <Pencil className="size-4" />
                    </button>
                    {isLegacyHashed ? (
                        <button
                            type="button"
                            disabled
                            className="grid size-8 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400"
                            title={t('apiKey.page.legacyHashed')}
                        >
                            <AlertTriangle className="size-4" />
                        </button>
                    ) : (
                        <CopyIconButton
                            text={apiKey.api_key}
                            className="grid size-8 place-items-center rounded-lg bg-primary/10 text-primary transition-all hover:bg-primary hover:text-primary-foreground active:scale-95"
                            copyIconClassName="size-4"
                            checkIconClassName="size-4"
                        />
                    )}
                    <button
                        type="button"
                        onClick={() => setConfirmDelete(true)}
                        className="grid size-8 place-items-center rounded-lg bg-destructive/10 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground"
                        title={t('apiKey.page.delete')}
                    >
                        <Trash2 className="size-4" />
                    </button>
                </div>
            </div>
            {isLegacyHashed ? (
                <div className="flex items-center gap-1.5 rounded-lg bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-600 dark:text-amber-400">
                    <AlertTriangle className="size-3.5 shrink-0" />
                    <span>{t('apiKey.page.legacyHashedHint')}</span>
                </div>
            ) : null}

            {tags.length > 0 ? (
                <div className="flex flex-wrap gap-1.5">
                    {tags.map((tag) => (
                        <span
                            key={tag}
                            className="inline-flex items-center gap-1 rounded-md bg-primary/8 px-1.5 py-0.5 text-[11px] font-medium text-primary/90"
                        >
                            <Tag className="size-2.5" />
                            {tag}
                        </span>
                    ))}
                </div>
            ) : null}

            <AnimatePresence initial={false}>
                {showStats ? (
                    <motion.div
                        key="stats"
                        initial={{ opacity: 0, height: 0 }}
                        animate={{ opacity: 1, height: 'auto' }}
                        exit={{ opacity: 0, height: 0 }}
                        transition={{ duration: 0.18 }}
                        className="overflow-hidden"
                    >
                        {stats && requestTotal > 0 ? (
                            <div className="grid grid-cols-2 gap-2 pt-1 sm:grid-cols-4">
                                <StatChip label={t('apiKey.page.requests')} value={`${requestTotal}`} />
                                <StatChip label={t('apiKey.page.successRate')} value={successRate ? `${successRate}%` : '-'} />
                                <StatChip
                                    label={t('apiKey.page.tokens')}
                                    value={`${formatTokenValue(totalTokens)}`}
                                />
                                <StatChip
                                    label={t('apiKey.page.cost')}
                                    value={`$${totalCost.toFixed(totalCost >= 1 ? 2 : 4)}`}
                                />
                            </div>
                        ) : (
                            <div className="pt-1 text-xs text-muted-foreground">{t('apiKey.page.noStats')}</div>
                        )}
                        <NonChatStats metrics={stats?.non_chat} />
                    </motion.div>
                ) : null}
            </AnimatePresence>

            <AnimatePresence>
                {confirmDelete ? (
                    <motion.div
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        exit={{ opacity: 0 }}
                        className="absolute inset-0 z-10 flex items-center justify-center gap-2 rounded-2xl bg-destructive/95 p-4 backdrop-blur-sm"
                    >
                        <span className="text-sm font-medium text-destructive-foreground">
                            {t('apiKey.page.deleteConfirm')}
                        </span>
                        <button
                            type="button"
                            onClick={() => setConfirmDelete(false)}
                            className="grid size-8 place-items-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-colors hover:bg-destructive-foreground/30"
                        >
                            <X className="size-4" />
                        </button>
                        <button
                            type="button"
                            onClick={onDelete}
                            disabled={isDeleting}
                            className="grid size-8 place-items-center rounded-lg bg-destructive-foreground text-destructive transition-colors hover:bg-destructive-foreground/90 disabled:opacity-50"
                        >
                            {isDeleting ? <Loader className="size-4 animate-spin" /> : <Check className="size-4" />}
                        </button>
                    </motion.div>
                ) : null}
            </AnimatePresence>
        </motion.div>
    );
}

function formatTokenValue(value: number): string {
    if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
    if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
    return `${value}`;
}

export function APIKeyPage() {
    const t = useTranslations('setting');
    const tEndpoints = useTranslations('endpoints');
    const { data: apiKeys, isLoading, error } = useAPIKeyList();
    const { data: statsList = [] } = useStatsAPIKey();
    const createAPIKey = useCreateAPIKey();
    const updateAPIKey = useUpdateAPIKey();
    const deleteAPIKey = useDeleteAPIKey();

    const [search, setSearch] = useState('');
    const [activeTag, setActiveTag] = useState<string | null>(null);
    const [dialogOpen, setDialogOpen] = useState(false);
    const [editingKey, setEditingKey] = useState<APIKey | null>(null);
    const [deletingId, setDeletingId] = useState<number | null>(null);

    const statsById = useMemo(() => {
        const map = new Map<number, StatsAPIKeyFormatted>();
        for (const s of statsList) map.set(s.api_key_id, s);
        return map;
    }, [statsList]);

    const allTags = useMemo(() => {
        const set = new Set<string>();
        let hasUntagged = false;
        for (const key of apiKeys ?? []) {
            const tags = parseTags(key.tags);
            if (tags.length === 0) hasUntagged = true;
            for (const tag of tags) set.add(tag);
        }
        return {
            tags: Array.from(set).sort((a, b) => a.localeCompare(b)),
            hasUntagged,
        };
    }, [apiKeys]);

    const tagSuggestions = allTags.tags;

    const filteredKeys = useMemo(() => {
        const query = search.trim().toLowerCase();
        return (apiKeys ?? [])
            .filter((key) => {
                const tags = parseTags(key.tags);
                if (activeTag === UNTAGGED && tags.length > 0) return false;
                if (activeTag && activeTag !== UNTAGGED && !tags.includes(activeTag)) return false;
                if (query) {
                    const haystack = `${key.name} ${tags.join(' ')}`.toLowerCase();
                    if (!haystack.includes(query)) return false;
                }
                return true;
            })
            .sort((a, b) => a.id - b.id);
    }, [apiKeys, search, activeTag]);

    const hasFilters = search.trim().length > 0 || activeTag !== null;

    const openCreate = useCallback(() => {
        setEditingKey(null);
        setDialogOpen(true);
    }, []);

    const openEdit = useCallback((key: APIKey) => {
        setEditingKey(key);
        setDialogOpen(true);
    }, []);

    const handleSubmit = useCallback((data: Omit<APIKey, 'id'>) => {
        if (editingKey) {
            updateAPIKey.mutate({ id: editingKey.id, ...data }, {
                onSuccess: () => {
                    toast.success(t('apiKey.toast.updateSuccess'));
                    setDialogOpen(false);
                },
                onError: (err) => {
                    toast.error(t('apiKey.toast.updateError'), { description: (err as unknown as ApiError)?.message });
                },
            });
        } else {
            createAPIKey.mutate(data, {
                onSuccess: () => {
                    toast.success(t('apiKey.toast.createSuccess'));
                    setDialogOpen(false);
                },
                onError: (err) => {
                    toast.error(t('apiKey.toast.createError'), { description: (err as unknown as ApiError)?.message });
                },
            });
        }
    }, [editingKey, updateAPIKey, createAPIKey, t]);

    const handleDelete = useCallback((id: number) => {
        setDeletingId(id);
        deleteAPIKey.mutate(id, {
            onSuccess: () => toast.success(t('apiKey.toast.deleteSuccess')),
            onError: (err) => toast.error(t('apiKey.toast.deleteError'), { description: (err as unknown as ApiError)?.message }),
            onSettled: () => setDeletingId((cur) => (cur === id ? null : cur)),
        });
    }, [deleteAPIKey, t]);

    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-xl">
            <PageWrapper className="space-y-4 pb-3 md:pb-6">
                <section className="rounded-2xl border border-border bg-card p-4 md:p-5">
                    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                        <div className="flex items-center gap-2">
                            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                                <KeyRound className="size-5" />
                                {t('apiKey.title')}
                            </h2>
                            <span className="rounded-full bg-muted/60 px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
                                {t('apiKey.page.count', { count: apiKeys?.length ?? 0 })}
                            </span>
                        </div>
                        <div className="flex items-center gap-2">
                            <div className="relative flex-1 sm:w-64 sm:flex-none">
                                <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                                <Input
                                    type="text"
                                    value={search}
                                    onChange={(e) => setSearch(e.target.value)}
                                    placeholder={t('apiKey.page.searchPlaceholder')}
                                    className="h-9 rounded-xl pl-9 text-sm"
                                />
                            </div>
                            <button
                                type="button"
                                onClick={openCreate}
                                className="flex h-9 shrink-0 items-center gap-1.5 rounded-xl bg-primary px-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
                            >
                                <Plus className="size-4" />
                                <span className="hidden sm:inline">{t('apiKey.page.newKey')}</span>
                            </button>
                        </div>
                    </div>

                    {(allTags.tags.length > 0 || allTags.hasUntagged) ? (
                        <div className="mt-3 flex flex-wrap items-center gap-1.5">
                            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                                <Tag className="size-3.5" />
                                {t('apiKey.page.filterByTag')}:
                            </span>
                            <button
                                type="button"
                                onClick={() => setActiveTag(null)}
                                className={cn(
                                    'rounded-full px-2.5 py-1 text-xs font-medium transition-colors',
                                    activeTag === null
                                        ? 'bg-primary text-primary-foreground'
                                        : 'bg-muted/50 text-muted-foreground hover:bg-muted',
                                )}
                            >
                                {t('apiKey.page.allTags')}
                            </button>
                            {allTags.tags.map((tag) => (
                                <button
                                    key={tag}
                                    type="button"
                                    onClick={() => setActiveTag((cur) => (cur === tag ? null : tag))}
                                    className={cn(
                                        'rounded-full px-2.5 py-1 text-xs font-medium transition-colors',
                                        activeTag === tag
                                            ? 'bg-primary text-primary-foreground'
                                            : 'bg-muted/50 text-muted-foreground hover:bg-muted',
                                    )}
                                >
                                    {tag}
                                </button>
                            ))}
                            {allTags.hasUntagged ? (
                                <button
                                    type="button"
                                    onClick={() => setActiveTag((cur) => (cur === UNTAGGED ? null : UNTAGGED))}
                                    className={cn(
                                        'inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium transition-colors',
                                        activeTag === UNTAGGED
                                            ? 'bg-primary text-primary-foreground'
                                            : 'bg-muted/50 text-muted-foreground hover:bg-muted',
                                    )}
                                >
                                    <CircleSlash className="size-3" />
                                    {t('apiKey.page.untagged')}
                                </button>
                            ) : null}
                        </div>
                    ) : null}
                </section>

                {isLoading ? (
                    <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
                        <Loader className="size-5 animate-spin" />
                    </div>
                ) : error ? (
                    <div className="flex h-40 items-center justify-center text-sm text-destructive">
                        {t('apiKey.loadFailed')}
                    </div>
                ) : (apiKeys?.length ?? 0) === 0 ? (
                    <div className="flex h-40 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-border text-sm text-muted-foreground">
                        <KeyRound className="size-8 opacity-40" />
                        {t('apiKey.empty')}
                        <button
                            type="button"
                            onClick={openCreate}
                            className="flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
                        >
                            <Plus className="size-4" />
                            {t('apiKey.page.newKey')}
                        </button>
                    </div>
                ) : filteredKeys.length === 0 ? (
                    <div className="flex h-40 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-border text-sm text-muted-foreground">
                        {t('apiKey.page.noMatch')}
                        {hasFilters ? (
                            <button
                                type="button"
                                onClick={() => {
                                    setSearch('');
                                    setActiveTag(null);
                                }}
                                className="flex h-9 items-center gap-1.5 rounded-xl border border-border px-4 text-sm font-medium transition-colors hover:bg-muted"
                            >
                                <FilterX className="size-4" />
                                {t('apiKey.page.clearFilters')}
                            </button>
                        ) : null}
                    </div>
                ) : (
                    <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
                        <AnimatePresence>
                            {filteredKeys.map((key) => (
                                <APIKeyCard
                                    key={key.id}
                                    apiKey={key}
                                    stats={statsById.get(key.id)}
                                    onEdit={() => openEdit(key)}
                                    onDelete={() => handleDelete(key.id)}
                                    isDeleting={deleteAPIKey.isPending && deletingId === key.id}
                                />
                            ))}
                        </AnimatePresence>
                    </div>
                )}
            </PageWrapper>

            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent className="max-h-[88dvh] overflow-y-auto rounded-3xl sm:max-w-md lg:max-w-3xl">
                    <DialogHeader>
                        <DialogTitle>
                            {editingKey ? t('apiKey.page.editTitle') : t('apiKey.page.createTitle')}
                        </DialogTitle>
                        <DialogDescription>
                            {editingKey ? t('apiKey.page.editDescription') : t('apiKey.page.createDescription')}
                        </DialogDescription>
                    </DialogHeader>
                    <APIKeyForm
                        key={editingKey ? `edit-${editingKey.id}` : 'create'}
                        apiKey={editingKey ?? undefined}
                        isPending={editingKey ? updateAPIKey.isPending : createAPIKey.isPending}
                        submitLabel={editingKey ? t('apiKey.form.save') : t('apiKey.form.create')}
                        tagSuggestions={tagSuggestions}
                        onSubmit={handleSubmit}
                        onClose={() => setDialogOpen(false)}
                    />
                </DialogContent>
            </Dialog>
        </div>
    );
}
