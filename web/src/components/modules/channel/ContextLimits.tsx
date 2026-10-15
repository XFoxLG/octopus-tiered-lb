'use client';

import { useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Gauge, Pencil, Plus, Save, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Hint } from '@/components/ui/hint';
import { Input } from '@/components/ui/input';
import { toast } from '@/components/common/Toast';
import {
    useChannelModelLimits,
    useDeleteChannelModelLimit,
    useSetChannelModelLimit,
} from '@/api/endpoints/channel-model-limit';

/**
 * v2.9.0：渠道×模型上下文上限管理。
 *
 * manual（人工声明）永不被自动学习覆盖；learned（自动学习）为累计多次独立
 * 上游超限后写入的参考值，只读——需要人工覆盖时在下方新增一行 manual。
 * 上限用于请求前预检：估算输入超限的候选会被直接排除，不再等上游 400。
 */
export function ContextLimits({ channelId }: { channelId: number }) {
    const t = useTranslations('channel.contextLimits');
    const { data: limits = [], isLoading } = useChannelModelLimits(channelId);
    const setLimit = useSetChannelModelLimit(channelId);
    const deleteLimit = useDeleteChannelModelLimit(channelId);

    const [newModelName, setNewModelName] = useState('');
    const [newMaxTokens, setNewMaxTokens] = useState('');
    const [editingModel, setEditingModel] = useState<string | null>(null);
    const [editingTokens, setEditingTokens] = useState('');

    const rows = useMemo(
        () =>
            [...limits].sort((a, b) => {
                if (a.source !== b.source) return a.source === 'manual' ? -1 : 1;
                return a.model_name.localeCompare(b.model_name);
            }),
        [limits],
    );

    const handleCreate = () => {
        const modelName = newModelName.trim();
        const maxTokens = Number(newMaxTokens);
        if (!modelName || !Number.isFinite(maxTokens) || maxTokens <= 0) return;
        setLimit.mutate(
            { model_name: modelName, max_context_tokens: Math.trunc(maxTokens) },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    setNewModelName('');
                    setNewMaxTokens('');
                },
            },
        );
    };

    const handleSaveEdit = (modelName: string) => {
        const maxTokens = Number(editingTokens);
        if (!Number.isFinite(maxTokens) || maxTokens <= 0) return;
        setLimit.mutate(
            { model_name: modelName, max_context_tokens: Math.trunc(maxTokens) },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    setEditingModel(null);
                    setEditingTokens('');
                },
            },
        );
    };

    const handleDelete = (modelName: string) => {
        deleteLimit.mutate(modelName, {
            onSuccess: () => toast.success(t('deleted')),
        });
    };

    return (
        <div className="space-y-3">
            <h3 className="flex min-w-0 items-center gap-2 text-sm font-semibold text-foreground">
                <Gauge className="size-4 shrink-0 text-muted-foreground" />
                <span>{t('title')}</span>
                <Hint text={t('titleHint')} />
            </h3>
            <p className="text-xs text-muted-foreground">{t('description')}</p>

            <div className="flex flex-col gap-2 rounded-lg border border-border/40 bg-card p-3 sm:flex-row sm:items-center">
                <Input
                    value={newModelName}
                    onChange={(e) => setNewModelName(e.target.value)}
                    placeholder={t('modelPlaceholder')}
                    aria-label={t('modelPlaceholder')}
                    className="flex-1 rounded-lg"
                />
                <Input
                    type="number"
                    min={1}
                    value={newMaxTokens}
                    onChange={(e) => setNewMaxTokens(e.target.value)}
                    placeholder={t('tokensPlaceholder')}
                    aria-label={t('tokensPlaceholder')}
                    className="w-full rounded-lg sm:w-44"
                />
                <Button
                    type="button"
                    size="sm"
                    onClick={handleCreate}
                    disabled={setLimit.isPending || !newModelName.trim() || !(Number(newMaxTokens) > 0)}
                    className="rounded-lg"
                >
                    <Plus className="mr-1 h-3.5 w-3.5" />
                    {t('add')}
                </Button>
            </div>

            {isLoading ? (
                <p className="text-xs text-muted-foreground">{t('loading')}</p>
            ) : rows.length === 0 ? (
                <p className="text-xs text-muted-foreground">{t('empty')}</p>
            ) : (
                <ul className="space-y-1.5">
                    {rows.map((row) => (
                        <li
                            key={row.model_name}
                            className="flex flex-col gap-2 rounded-lg border border-border/30 bg-card px-3 py-2 sm:flex-row sm:items-center sm:justify-between"
                        >
                            <div className="min-w-0">
                                <div className="flex min-w-0 items-center gap-2">
                                    <span className="truncate text-sm font-medium">{row.model_name}</span>
                                    <Badge
                                        variant="secondary"
                                        className={
                                            row.source === 'manual'
                                                ? 'shrink-0 rounded-full border border-primary/30 bg-primary/10 text-[10px] text-primary'
                                                : 'shrink-0 rounded-full border border-border/60 bg-muted/60 text-[10px] text-muted-foreground'
                                        }
                                        title={row.source === 'manual' ? t('sourceManualHint') : t('sourceLearnedHint')}
                                    >
                                        {row.source === 'manual' ? t('sourceManual') : t('sourceLearned')}
                                    </Badge>
                                </div>
                                <div className="text-xs text-muted-foreground">
                                    {t('tokensValue', { tokens: row.max_context_tokens.toLocaleString() })}
                                    {row.source !== 'manual' && row.evidence_count > 0
                                        ? ` · ${t('evidence', { count: row.evidence_count })}`
                                        : ''}
                                </div>
                            </div>
                            <div className="flex shrink-0 items-center gap-1.5">
                                {editingModel === row.model_name ? (
                                    <>
                                        <Input
                                            type="number"
                                            min={1}
                                            value={editingTokens}
                                            onChange={(e) => setEditingTokens(e.target.value)}
                                            aria-label={t('tokensPlaceholder')}
                                            className="h-8 w-32 rounded-lg"
                                        />
                                        <Button
                                            type="button"
                                            size="sm"
                                            variant="ghost"
                                            onClick={() => handleSaveEdit(row.model_name)}
                                            disabled={setLimit.isPending}
                                            title={t('save')}
                                            className="h-8 rounded-lg px-2"
                                        >
                                            <Save className="h-3.5 w-3.5" />
                                        </Button>
                                        <Button
                                            type="button"
                                            size="sm"
                                            variant="ghost"
                                            onClick={() => {
                                                setEditingModel(null);
                                                setEditingTokens('');
                                            }}
                                            title={t('cancel')}
                                            className="h-8 rounded-lg px-2"
                                        >
                                            <X className="h-3.5 w-3.5" />
                                        </Button>
                                    </>
                                ) : (
                                    <>
                                        <Button
                                            type="button"
                                            size="sm"
                                            variant="ghost"
                                            onClick={() => {
                                                setEditingModel(row.model_name);
                                                setEditingTokens(String(row.max_context_tokens));
                                            }}
                                            title={t('edit')}
                                            className="h-8 rounded-lg px-2"
                                        >
                                            <Pencil className="h-3.5 w-3.5" />
                                        </Button>
                                        <Button
                                            type="button"
                                            size="sm"
                                            variant="ghost"
                                            onClick={() => handleDelete(row.model_name)}
                                            disabled={deleteLimit.isPending}
                                            title={t('remove')}
                                            className="h-8 rounded-lg px-2 text-destructive"
                                        >
                                            <Trash2 className="h-3.5 w-3.5" />
                                        </Button>
                                    </>
                                )}
                            </div>
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
}
