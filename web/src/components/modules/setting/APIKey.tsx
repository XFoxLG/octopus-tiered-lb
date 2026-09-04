'use client';

import { useCallback, useId, useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { KeyRound, Plus, Loader, Trash2, Check, X, Info, CalendarDays, Pencil, Maximize2, AlertTriangle } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { Input } from '@/components/ui/input';
import { Hint } from '@/components/ui/hint';
import { Calendar } from '@/components/ui/calendar';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import {
    useAPIKeyList,
    useCreateAPIKey,
    useUpdateAPIKey,
    useDeleteAPIKey,
    isLegacyHashedAPIKey,
    type APIKey,
} from '@/api/endpoints/apikey';
import { useGroupList } from '@/api/endpoints/group';
import { useChannelList } from '@/api/endpoints/channel';
import { useStatsAPIKey } from '@/api/endpoints/stats';
import { cn, formatCount } from '@/lib/utils';
import { toast } from '@/components/common/Toast';
import { CopyIconButton } from '@/components/common/CopyButton';
import type { ApiError } from '@/api/types';

/**
 * 将用户选择的日期和时间组合为 Unix 秒级时间戳。
 *
 * 语义：用户选择的日期（日历组件）和时间输入框的值，统一按 UTC 时刻存储。
 * 服务端做过期判断时直接用 Unix 时间戳做绝对时间比较，不依赖时区。
 * 回显时 parseExpireDate 解析为 Date 后由日历组件按浏览器本地时区展示。
 */
function toExpireAt(date: Date, time: string): number {
    const t = /^\d{2}:\d{2}$/.test(time) ? time : '00:00';
    const [hh, mm] = t.split(':').map(Number);
    const d = new Date(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate(), hh, mm, 0));
    // 返回 Unix 时间戳（秒）
    return Math.floor(d.getTime() / 1000);
}

function parseExpireDate(expireAt?: number): Date | undefined {
    if (!expireAt) return undefined;
    // 从 Unix 时间戳（秒）转换为 Date
    const d = new Date(expireAt * 1000);
    return isNaN(d.getTime()) ? undefined : d;
}

function normalizeHHmm(input: string): string {
    const cleaned = input.replace(/[^\d:]/g, '');
    const parts = cleaned.includes(':') ? cleaned.split(':') : [cleaned.slice(0, 2), cleaned.slice(2, 4)];
    const hh = Math.min(23, Math.max(0, parseInt(parts[0] || '0', 10)));
    const mm = Math.min(59, Math.max(0, parseInt(parts[1] || '0', 10)));
    return `${hh.toString().padStart(2, '0')}:${mm.toString().padStart(2, '0')}`;
}

function normalizeMoneyInput(input: string): string {
    const cleaned = input.replace(/[^\d.]/g, '');
    const [intPart, ...rest] = cleaned.split('.');
    return rest.length > 0 ? `${intPart}.${rest.join('').slice(0, 6)}` : intPart;
}

/**
 * 将用户输入的 Token 简写字符串解析为整数 token 数。
 * 支持的简写：k/K = 千(1e3)、w/W/万 = 万(1e4)、m/M = 百万(1e6)、b/B/亿 = 亿(1e8)。
 * 例如 "100w" → 1000000, "5亿" → 500000000, "1.5k" → 1500。
 * 纯数字直接返回。空或无法解析返回 0。
 */
function parseTokenShorthand(input: string): number {
    const trimmed = input.trim().toLowerCase();
    if (!trimmed) return 0;
    // 匹配 数字(可含小数) + 可选单位
    const match = trimmed.match(/^(\d+(?:\.\d+)?)\s*(k|w|m|b|万|亿)?$/);
    if (!match) return 0;
    const value = parseFloat(match[1]);
    if (!Number.isFinite(value)) return 0;
    const unit = match[2];
    const multiplier = unit === 'k' ? 1e3
        : unit === 'w' || unit === '万' ? 1e4
        : unit === 'm' ? 1e6
        : unit === 'b' ? 1e9
        : unit === '亿' ? 1e8
        : 1;
    return Math.round(value * multiplier);
}


/** 规范化 Token 输入：允许数字 + 简写后缀，过滤非法字符。 */
function normalizeTokenInput(input: string): string {
    // 保留数字、小数点、以及简写后缀字符
    return input.replace(/[^\d.kwmKWMbB万亿]/g, '');
}

function toggleModel(current: string | undefined, model: string): string | undefined {
    const models = current ? current.split(',').filter(Boolean) : [];
    const next = models.includes(model)
        ? models.filter((m) => m !== model)
        : [...models, model];
    return next.length ? next.join(',') : undefined;
}

function hasModel(supported: string | undefined, model: string): boolean {
    return supported ? supported.split(',').includes(model) : false;
}

/** 将逗号分隔的标签字符串解析为去重、去空的数组。 */
export function parseTags(raw: string | undefined): string[] {
    if (!raw) return [];
    const seen = new Set<string>();
    const result: string[] = [];
    for (const part of raw.split(',')) {
        const tag = part.trim();
        if (tag && !seen.has(tag)) {
            seen.add(tag);
            result.push(tag);
        }
    }
    return result;
}

function serializeTags(tags: string[]): string {
    return tags.join(',');
}

/** 标签输入：支持回车 / 逗号添加、点击移除，并可从已有标签快速补全。 */
function TagInput({
    value,
    suggestions,
    disabled,
    placeholder,
    onChange,
}: {
    value: string | undefined;
    suggestions: string[];
    disabled?: boolean;
    placeholder?: string;
    onChange: (next: string) => void;
}) {
    const tags = useMemo(() => parseTags(value), [value]);
    const [draft, setDraft] = useState('');

    const addTag = useCallback((raw: string) => {
        const tag = raw.trim().replace(/,+$/, '').trim();
        if (!tag) return;
        if (tags.includes(tag)) {
            setDraft('');
            return;
        }
        onChange(serializeTags([...tags, tag]));
        setDraft('');
    }, [tags, onChange]);

    const removeTag = useCallback((tag: string) => {
        onChange(serializeTags(tags.filter((t) => t !== tag)));
    }, [tags, onChange]);

    const availableSuggestions = useMemo(
        () => suggestions.filter((s) => !tags.includes(s)).slice(0, 12),
        [suggestions, tags],
    );

    return (
        <div className="grid gap-1.5">
            <div
                className={cn(
                    'flex flex-wrap items-center gap-1.5 rounded-xl border border-border bg-muted/20 px-2.5 py-2 transition-colors focus-within:border-primary/40',
                    disabled && 'opacity-50',
                )}
            >
                {tags.map((tag) => (
                    <span
                        key={tag}
                        className="inline-flex items-center gap-1 rounded-lg bg-primary/12 px-2 py-1 text-xs font-medium text-primary"
                    >
                        {tag}
                        <button
                            type="button"
                            disabled={disabled}
                            onClick={() => removeTag(tag)}
                            className="grid size-3.5 place-items-center rounded-full text-primary/70 transition-colors hover:bg-primary/20 hover:text-primary"
                        >
                            <X className="size-3" />
                        </button>
                    </span>
                ))}
                <input
                    type="text"
                    value={draft}
                    disabled={disabled}
                    placeholder={tags.length === 0 ? placeholder : ''}
                    onChange={(e) => {
                        const v = e.target.value;
                        if (v.endsWith(',')) {
                            addTag(v);
                        } else {
                            setDraft(v);
                        }
                    }}
                    onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                            e.preventDefault();
                            addTag(draft);
                        } else if (e.key === 'Backspace' && draft === '' && tags.length > 0) {
                            removeTag(tags[tags.length - 1]);
                        }
                    }}
                    onBlur={() => addTag(draft)}
                    className="h-6 min-w-[6rem] flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                />
            </div>
            {availableSuggestions.length > 0 ? (
                <div className="flex flex-wrap gap-1.5">
                    {availableSuggestions.map((s) => (
                        <button
                            key={s}
                            type="button"
                            disabled={disabled}
                            onClick={() => addTag(s)}
                            className="rounded-lg border border-dashed border-border px-2 py-0.5 text-[11px] text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary disabled:opacity-50"
                        >
                            + {s}
                        </button>
                    ))}
                </div>
            ) : null}
        </div>
    );
}

const PER_MODEL_QUOTA_PLACEHOLDER = '{"gpt-4o":{"rpm":5,"tpm":50000}}';

export interface APIKeyFormProps {
    apiKey?: APIKey;
    isPending: boolean;
    submitLabel: string;
    tagSuggestions?: string[];
    onSubmit: (data: Omit<APIKey, 'id'>) => void;
    onClose: () => void;
}

export function APIKeyForm({ apiKey, isPending, submitLabel, tagSuggestions = [], onSubmit, onClose }: APIKeyFormProps) {
    const t = useTranslations('setting');
    const { data: groups = [] } = useGroupList();
    const { data: channels = [] } = useChannelList();
    const isEditing = !!apiKey;

    // Strip the fixed prefix so the form only edits the suffix part.
    // 存量哈希化 key（明文不可逆）不回填哈希值，留空保存即保持不变。
    const apiKeyPrefix = 'sk-octopus-';
    const initialKeySuffix = apiKey?.api_key?.startsWith(apiKeyPrefix)
        ? apiKey.api_key.slice(apiKeyPrefix.length)
        : (isLegacyHashedAPIKey(apiKey?.api_key) ? '' : (apiKey?.api_key ?? ''));

    const [form, setForm] = useState<Omit<APIKey, 'id'>>(() => ({
        name: apiKey?.name ?? '',
        api_key: initialKeySuffix,
        enabled: apiKey?.enabled ?? true,
        expire_at: apiKey?.expire_at,
        max_cost: apiKey?.max_cost,
        max_tokens: apiKey?.max_tokens,
        supported_models: apiKey?.supported_models,
        allowed_group_categories: apiKey?.allowed_group_categories,
        rate_limit_rpm: apiKey?.rate_limit_rpm ?? 0,
        rate_limit_tpm: apiKey?.rate_limit_tpm ?? 0,
        per_model_quota_json: apiKey?.per_model_quota_json ?? '',
        allowed_ips: apiKey?.allowed_ips ?? '',
        excluded_channels: apiKey?.excluded_channels,
        tags: apiKey?.tags ?? '',
    }));
    const [maxCostInput, setMaxCostInput] = useState(() =>
        apiKey?.max_cost != null ? String(apiKey.max_cost) : ''
    );
    const [maxTokensInput, setMaxTokensInput] = useState(() =>
        apiKey?.max_tokens != null && apiKey.max_tokens > 0 ? String(apiKey.max_tokens) : ''
    );
    const [expireTime, setExpireTime] = useState(() => {
        if (apiKey?.expire_at) {
            const d = new Date(apiKey.expire_at * 1000);
            if (!isNaN(d.getTime())) {
                // 使用本地时区回显小时分钟，与用户输入时一致
                return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
            }
        }
        return '00:00';
    });
    const [expireOpen, setExpireOpen] = useState(false);

    const availableModels = useMemo(() => {
        const names = groups.map((g) => g.name).filter(Boolean);
        return Array.from(new Set(names)).sort((a, b) => a.localeCompare(b));
    }, [groups]);

    const availableGroupCategories = useMemo(() => {
        const categories = groups.map((g) => g.category?.trim()).filter((c): c is string => Boolean(c));
        return Array.from(new Set(categories)).sort((a, b) => a.localeCompare(b));
    }, [groups]);

    const expireDate = parseExpireDate(form.expire_at);
    const neverExpire = !form.expire_at;
    const isUnlimitedCost = maxCostInput.trim() === '';
    const isUnlimitedTokens = maxTokensInput.trim() === '';

    const expireLabel = neverExpire
        ? t('apiKey.form.neverExpire')
        : expireDate
            ? expireDate.toLocaleDateString()
            : t('apiKey.form.selectDate');

    const updateForm = useCallback((updater: Partial<Omit<APIKey, 'id'>>) => {
        setForm((prev) => ({ ...prev, ...updater }));
    }, []);

    const handleSelectDate = useCallback((d: Date | undefined) => {
        if (d) {
            updateForm({ expire_at: toExpireAt(d, expireTime) });
            setExpireOpen(false);
        } else {
            updateForm({ expire_at: undefined });
        }
    }, [updateForm, expireTime]);

    const handleTimeBlur = useCallback(() => {
        if (!expireDate) return;
        const normalized = normalizeHHmm(expireTime);
        setExpireTime(normalized);
        updateForm({ expire_at: toExpireAt(expireDate, normalized) });
    }, [expireDate, expireTime, updateForm]);

    const handleToggleNeverExpire = useCallback(() => {
        if (neverExpire) {
            updateForm({ expire_at: toExpireAt(new Date(), expireTime) });
        } else {
            updateForm({ expire_at: undefined });
            setExpireOpen(false);
        }
    }, [neverExpire, expireTime, updateForm]);

    const handleMaxCostChange = useCallback((val: string) => {
        const normalized = normalizeMoneyInput(val);
        setMaxCostInput(normalized);
        const num = parseFloat(normalized);
        updateForm({ max_cost: Number.isFinite(num) ? num : undefined });
    }, [updateForm]);

    const handleClearMaxCost = useCallback(() => {
        setMaxCostInput('');
        updateForm({ max_cost: undefined });
    }, [updateForm]);

    const handleMaxTokensChange = useCallback((val: string) => {
        const normalized = normalizeTokenInput(val);
        setMaxTokensInput(normalized);
        const parsed = parseTokenShorthand(normalized);
        updateForm({ max_tokens: parsed > 0 ? parsed : undefined });
    }, [updateForm]);

    const handleClearMaxTokens = useCallback(() => {
        setMaxTokensInput('');
        updateForm({ max_tokens: undefined });
    }, [updateForm]);

    const handleSubmit = useCallback((e: React.FormEvent) => {
        e.preventDefault();
        if (!form.name.trim()) return;
        onSubmit(form);
    }, [form, onSubmit]);

    return (
        // @container 让表单按"自身宽度"而非视口切换列数：宽对话框(lg)里两列密排，
        // 窄浮层(~420px)里仍单列。各字段用 @lg:col-span-* 控制跨列。
        <form onSubmit={handleSubmit} className="@container grid grid-cols-1 gap-2 @lg:grid-cols-2 @lg:gap-x-4">
            <label className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                {t('apiKey.form.name')}
                <Input
                    type="text"
                    value={form.name}
                    onChange={(e) => updateForm({ name: e.target.value })}
                    className="h-9 text-sm rounded-xl"
                    disabled={isPending}
                    required
                />
            </label>

            <label className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                <span className="flex items-center gap-1.5">
                    {t('apiKey.form.customKey')}
                    {!isEditing && (
                        <span className="text-[10px] text-muted-foreground/70">({t('apiKey.form.optional')})</span>
                    )}
                    <Hint text={!isEditing ? t('apiKey.form.customKeyHint') : undefined} />
                </span>
                <div className="flex items-center gap-0 rounded-xl border border-border bg-muted/20 overflow-hidden transition-colors focus-within:border-primary/40">
                    <span className="shrink-0 px-3 text-sm font-mono text-muted-foreground select-none">sk-octopus-</span>
                    <Input
                        type="text"
                        value={form.api_key}
                        onChange={(e) => updateForm({ api_key: e.target.value })}
                        placeholder={isEditing ? '' : t('apiKey.form.customKeyPlaceholder')}
                        className="h-9 text-sm rounded-none border-0 bg-transparent focus-visible:ring-0 shadow-none"
                        disabled={isPending}
                    />
                </div>
            </label>

            <div className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                {t('apiKey.form.tags')}
                <TagInput
                    value={form.tags}
                    suggestions={tagSuggestions}
                    disabled={isPending}
                    placeholder={t('apiKey.form.tagsPlaceholder')}
                    onChange={(next) => updateForm({ tags: next })}
                />
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                {t('apiKey.form.maxCost')}
                <div className="flex items-center gap-2">
                    <div className="relative flex-1">
                        <span className="absolute left-3 top-1/2 -translate-y-1/2 text-sm text-muted-foreground">$</span>
                        <Input
                            type="text"
                            inputMode="decimal"
                            placeholder={t('apiKey.form.maxCostPlaceholder')}
                            value={maxCostInput}
                            onChange={(e) => handleMaxCostChange(e.target.value)}
                            className="h-9 text-sm rounded-xl pl-7"
                            disabled={isPending}
                        />
                    </div>
                    <button
                        type="button"
                        onClick={handleClearMaxCost}
                        disabled={isPending}
                        aria-pressed={isUnlimitedCost}
                        className={cn(
                            'h-9 px-3 rounded-xl border text-sm transition-colors shrink-0',
                            isUnlimitedCost
                                ? 'bg-primary text-primary-foreground border-primary/30'
                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30',
                            isPending && 'opacity-50 cursor-not-allowed'
                        )}
                    >
                        {t('apiKey.form.unlimited')}
                    </button>
                </div>
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                <span className="flex items-center gap-1.5">
                    {t('apiKey.form.maxTokens')}
                    <Hint text={t('apiKey.form.maxTokensHint')} />
                </span>
                <div className="flex items-center gap-2">
                    <div className="relative flex-1">
                        <Input
                            type="text"
                            inputMode="text"
                            placeholder={t('apiKey.form.maxTokensPlaceholder')}
                            value={maxTokensInput}
                            onChange={(e) => handleMaxTokensChange(e.target.value)}
                            className="h-9 text-sm rounded-xl"
                            disabled={isPending}
                        />
                        {form.max_tokens != null && form.max_tokens > 0 && (
                            <span className="absolute right-3 top-1/2 -translate-y-1/2 text-[11px] text-muted-foreground tabular-nums pointer-events-none">
                                {formatCount(form.max_tokens).formatted.value}{formatCount(form.max_tokens).formatted.unit && ` ${formatCount(form.max_tokens).formatted.unit}`}
                            </span>
                        )}
                    </div>
                    <button
                        type="button"
                        onClick={handleClearMaxTokens}
                        disabled={isPending}
                        aria-pressed={isUnlimitedTokens}
                        className={cn(
                            'h-9 px-3 rounded-xl border text-sm transition-colors shrink-0',
                            isUnlimitedTokens
                                ? 'bg-primary text-primary-foreground border-primary/30'
                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30',
                            isPending && 'opacity-50 cursor-not-allowed'
                        )}
                    >
                        {t('apiKey.form.unlimited')}
                    </button>
                </div>
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground">
                {t('apiKey.form.rateLimitRpm.label')}
                <div className="flex items-center gap-2">
                    <Input
                        type="number"
                        placeholder={t('apiKey.form.rateLimitRpm.placeholder')}
                        value={form.rate_limit_rpm ?? 0}
                        onChange={(e) => updateForm({ rate_limit_rpm: Number(e.target.value) })}
                        className="h-9 text-sm rounded-xl"
                        disabled={isPending}
                    />
                </div>
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground">
                {t('apiKey.form.rateLimitTpm.label')}
                <div className="flex items-center gap-2">
                    <Input
                        type="number"
                        placeholder={t('apiKey.form.rateLimitTpm.placeholder')}
                        value={form.rate_limit_tpm ?? 0}
                        onChange={(e) => updateForm({ rate_limit_tpm: Number(e.target.value) })}
                        className="h-9 text-sm rounded-xl"
                        disabled={isPending}
                    />
                </div>
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                {t('apiKey.form.perModelQuota.label')}
                <Input
                    type="text"
                    placeholder={PER_MODEL_QUOTA_PLACEHOLDER}
                    value={form.per_model_quota_json ?? ''}
                    onChange={(e) => updateForm({ per_model_quota_json: e.target.value })}
                    className="h-9 text-sm rounded-xl font-mono"
                    disabled={isPending}
                />
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                {t('apiKey.form.allowedIPs')}
                <Input
                    type="text"
                    placeholder={t('apiKey.form.allowedIPsPlaceholder')}
                    value={form.allowed_ips ?? ''}
                    onChange={(e) => updateForm({ allowed_ips: e.target.value })}
                    className="h-9 text-sm rounded-xl font-mono"
                    disabled={isPending}
                />
            </div>

            <div className="grid gap-1 text-xs text-muted-foreground @lg:col-span-2">
                {t('apiKey.form.expireAt')}
                <div className="flex items-center gap-2 relative">
                    <Popover
                        open={expireOpen && !neverExpire}
                        onOpenChange={setExpireOpen}
                    >
                        <PopoverTrigger asChild>
                            <button
                                type="button"
                                disabled={isPending || neverExpire}
                                className="h-9 flex-1 flex items-center justify-between gap-2 rounded-xl border border-border bg-muted/20 px-3 text-sm text-foreground transition-colors hover:bg-muted/30 disabled:opacity-50"
                            >
                                <span className="truncate">{expireLabel}</span>
                                <CalendarDays className="size-4 text-muted-foreground" />
                            </button>
                        </PopoverTrigger>
                        <PopoverContent
                            align="start"
                            side="bottom"
                            sideOffset={8}
                            className="w-fit rounded-2xl border border-border/60 shadow-xl overflow-hidden bg-card p-0"
                        >
                            <Calendar
                                mode="single"
                                selected={expireDate}
                                onSelect={handleSelectDate}
                                disabled={isPending}
                                classNames={{ today: '' }}
                            />
                        </PopoverContent>
                    </Popover>

                    <Input
                        type="text"
                        value={expireTime}
                        onChange={(e) => setExpireTime(e.target.value.replace(/[^\d:]/g, '').slice(0, 5))}
                        onBlur={handleTimeBlur}
                        className="h-9 w-[92px] text-sm rounded-xl"
                        disabled={isPending || neverExpire || !expireDate}
                        inputMode="numeric"
                        placeholder={t('apiKey.form.expireTimePlaceholder')}
                    />

                    <button
                        type="button"
                        onClick={handleToggleNeverExpire}
                        disabled={isPending}
                        aria-pressed={neverExpire}
                        className={cn(
                            'h-9 px-3 rounded-xl border text-sm transition-colors whitespace-nowrap shrink-0',
                            neverExpire
                                ? 'bg-primary text-primary-foreground border-primary/30'
                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30',
                            isPending && 'opacity-50 cursor-not-allowed'
                        )}
                    >
                        {t('apiKey.form.neverExpire')}
                    </button>
                </div>
            </div>

            <div className="grid gap-1 @lg:col-span-2">
                <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    {t('apiKey.form.supportedModels')}
                    <Hint text={t('apiKey.form.modelsHint')} />
                </span>
                <div className="max-h-40 overflow-auto rounded-xl p-2">
                    {availableModels.length === 0 ? (
                        <div className="text-xs text-muted-foreground py-2 text-center">
                            {t('apiKey.form.noModels')}
                        </div>
                    ) : (
                        <div className="flex flex-wrap gap-2">
                            {availableModels.map((m) => {
                                const checked = hasModel(form.supported_models, m);
                                return (
                                    <button
                                        key={m}
                                        type="button"
                                        disabled={isPending}
                                        onClick={() => updateForm({ supported_models: toggleModel(form.supported_models, m) })}
                                        className="text-left disabled:opacity-50"
                                    >
                                        <Badge
                                            variant={checked ? 'default' : 'outline'}
                                            className={cn(
                                                'cursor-pointer select-none',
                                                !checked && 'bg-card hover:bg-card'
                                            )}
                                        >
                                            {m}
                                        </Badge>
                                    </button>
                                );
                            })}
                        </div>
                    )}
                </div>
            </div>

            <div className="grid gap-1 @lg:col-span-2">
                <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    {t('apiKey.form.allowedGroupCategories')}
                    <Hint text={t('apiKey.form.groupCategoriesHint')} />
                </span>
                <div className="max-h-32 overflow-auto rounded-xl p-2">
                    {availableGroupCategories.length === 0 ? (
                        <div className="text-xs text-muted-foreground py-2 text-center">
                            {t('apiKey.form.noGroupCategories')}
                        </div>
                    ) : (
                        <div className="flex flex-wrap gap-2">
                            {availableGroupCategories.map((category) => {
                                const checked = hasModel(form.allowed_group_categories, category);
                                return (
                                    <button
                                        key={category}
                                        type="button"
                                        disabled={isPending}
                                        onClick={() => updateForm({ allowed_group_categories: toggleModel(form.allowed_group_categories, category) })}
                                        className="text-left disabled:opacity-50"
                                    >
                                        <Badge
                                            variant={checked ? 'default' : 'outline'}
                                            className={cn(
                                                'cursor-pointer select-none',
                                                !checked && 'bg-card hover:bg-card'
                                            )}
                                        >
                                            {category}
                                        </Badge>
                                    </button>
                                );
                            })}
                        </div>
                    )}
                </div>
            </div>

            <div className="grid gap-1 @lg:col-span-2">
                <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    {t('apiKey.form.excludedChannels')}
                    <Hint text={t('apiKey.form.excludedChannelsHint')} />
                </span>
                <div className="max-h-40 overflow-auto rounded-xl p-2">
                    {channels.length === 0 ? (
                        <div className="text-xs text-muted-foreground py-2 text-center">
                            {t('apiKey.form.noChannels')}
                        </div>
                    ) : (
                        <div className="flex flex-wrap gap-2">
                            {channels.map((ch) => {
                                const checked = hasModel(form.excluded_channels, String(ch.raw.id));
                                return (
                                    <button
                                        key={ch.raw.id}
                                        type="button"
                                        disabled={isPending}
                                        onClick={() => updateForm({ excluded_channels: toggleModel(form.excluded_channels, String(ch.raw.id)) })}
                                        className="text-left disabled:opacity-50"
                                    >
                                        <Badge
                                            variant={checked ? 'default' : 'outline'}
                                            className={cn(
                                                'cursor-pointer select-none',
                                                !checked && 'bg-card hover:bg-card'
                                            )}
                                        >
                                            {ch.raw.name}
                                        </Badge>
                                    </button>
                                );
                            })}
                        </div>
                    )}
                </div>
            </div>

            <div className="flex items-center justify-between pt-1 @lg:col-span-2">
                <span className="text-xs text-muted-foreground">{t('apiKey.form.enabled')}</span>
                <Switch
                    checked={form.enabled ?? true}
                    onCheckedChange={(checked) => updateForm({ enabled: checked })}
                    disabled={isPending}
                />
            </div>

            <div className="flex gap-2 pt-2 mt-3 @lg:col-span-2">
                <button
                    type="button"
                    onClick={onClose}
                    disabled={isPending}
                    className="flex-1 h-9 flex items-center justify-center gap-1.5 rounded-xl bg-muted text-muted-foreground text-sm font-medium transition-all hover:bg-muted/80 active:scale-[0.98] disabled:opacity-50"
                >
                    <X className="size-4" />
                    {t('apiKey.form.cancel')}
                </button>
                <button
                    type="submit"
                    disabled={isPending || !form.name.trim()}
                    className="flex-1 h-9 flex items-center justify-center gap-1.5 rounded-xl bg-primary text-primary-foreground text-sm font-medium transition-all hover:bg-primary/90 active:scale-[0.98] disabled:opacity-50"
                >
                    {isPending ? <Loader className="size-4 animate-spin" /> : <Check className="size-4" />}
                    {submitLabel}
                </button>
            </div>
        </form>
    );
}

function APIKeyFormOverlay({
    layoutId,
    apiKey,
    isPending,
    submitLabel,
    tagSuggestions,
    onSubmit,
    onClose,
}: {
    layoutId: string;
    apiKey?: APIKey;
    isPending: boolean;
    submitLabel: string;
    tagSuggestions?: string[];
    onSubmit: (data: Omit<APIKey, 'id'>) => void;
    onClose: () => void;
}) {
    return (
        <motion.div
            layoutId={layoutId}
            className="fixed inset-x-3 top-[calc(env(safe-area-inset-top,0px)+0.75rem)] bottom-[calc(4.75rem+env(safe-area-inset-bottom,0px))] z-50 overflow-y-auto overscroll-contain rounded-3xl border border-border bg-card p-4 shadow-xl sm:fixed sm:left-1/2 sm:right-auto sm:top-1/2 sm:z-50 sm:max-h-[85dvh] sm:w-[min(420px,calc(100vw-2rem))] sm:-translate-x-1/2 sm:-translate-y-1/2 sm:p-5"
            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
        >
            <APIKeyForm
                key={apiKey ? `apikey-form-${apiKey.id}` : 'apikey-form-create'}
                apiKey={apiKey}
                isPending={isPending}
                submitLabel={submitLabel}
                tagSuggestions={tagSuggestions}
                onSubmit={onSubmit}
                onClose={onClose}
            />
        </motion.div>
    );
}

function APIKeyStatsCard({
    layoutId,
    apiKey,
    onClose,
}: {
    layoutId: string;
    apiKey: APIKey;
    onClose: () => void;
}) {
    const t = useTranslations('setting');
    const { data: statsList = [] } = useStatsAPIKey();
    const stats = useMemo(() => statsList.find((s) => s.api_key_id === apiKey.id), [statsList, apiKey.id]);

    return (
        <motion.div
            layoutId={layoutId}
            className="fixed left-1/2 top-1/2 z-50 w-[min(320px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 flex flex-col bg-card p-5 rounded-3xl border border-border max-h-[85dvh] overflow-y-auto overscroll-contain"
            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
        >
            <div className="flex items-center justify-between gap-2 mb-3">
                <h3 className="text-sm font-semibold text-card-foreground line-clamp-1">
                    {apiKey.name}
                </h3>
                <button
                    type="button"
                    onClick={onClose}
                    className="size-8 flex items-center justify-center rounded-lg bg-muted text-muted-foreground transition-colors hover:bg-muted/80"
                >
                    <X className="size-4" />
                </button>
            </div>

            {!stats ? (
                <div className="text-sm text-muted-foreground">{t('apiKey.stats.noData')}</div>
            ) : (
                <div className="grid grid-cols-2 gap-3 text-sm">
                    <div className="rounded-lg bg-muted/40 p-3">
                        <div className="text-xs text-muted-foreground">{t('apiKey.stats.inputToken')}</div>
                        <div className="font-medium tabular-nums">
                            {stats.input_token.formatted.value}
                            {stats.input_token.formatted.unit}
                        </div>
                    </div>
                    <div className="rounded-lg bg-muted/40 p-3">
                        <div className="text-xs text-muted-foreground">{t('apiKey.stats.outputToken')}</div>
                        <div className="font-medium tabular-nums">
                            {stats.output_token.formatted.value}
                            {stats.output_token.formatted.unit}
                        </div>
                    </div>
                    <div className="rounded-lg bg-muted/40 p-3">
                        <div className="text-xs text-muted-foreground">{t('apiKey.stats.inputCost')}</div>
                        <div className="font-medium tabular-nums">
                            {stats.input_cost.formatted.value}
                            {stats.input_cost.formatted.unit}
                        </div>
                    </div>
                    <div className="rounded-lg bg-muted/40 p-3">
                        <div className="text-xs text-muted-foreground">{t('apiKey.stats.outputCost')}</div>
                        <div className="font-medium tabular-nums">
                            {stats.output_cost.formatted.value}
                            {stats.output_cost.formatted.unit}
                        </div>
                    </div>
                    <div className="rounded-lg bg-muted/40 p-3">
                        <div className="text-xs text-muted-foreground">{t('apiKey.stats.requestSuccess')}</div>
                        <div className="font-medium tabular-nums">
                            {stats.request_success.formatted.value}
                            {stats.request_success.formatted.unit}
                        </div>
                    </div>
                    <div className="rounded-lg bg-muted/40 p-3">
                        <div className="text-xs text-muted-foreground">{t('apiKey.stats.requestFailed')}</div>
                        <div className="font-medium tabular-nums">
                            {stats.request_failed.formatted.value}
                            {stats.request_failed.formatted.unit}
                        </div>
                    </div>
                </div>
            )}
        </motion.div>
    );
}

function APIKeyKeyItem({
    apiKey,
    statsLayoutId,
    editLayoutId,
    deleteLayoutId,
    onViewStats,
    onEdit,
    onDelete,
    isDeleting,
}: {
    apiKey: APIKey;
    statsLayoutId: string;
    editLayoutId: string;
    deleteLayoutId: string;
    onViewStats: () => void;
    onEdit: () => void;
    onDelete: () => void;
    isDeleting: boolean;
}) {
    const t = useTranslations('setting');
    const [confirmDelete, setConfirmDelete] = useState(false);
    const isLegacyHashed = isLegacyHashedAPIKey(apiKey.api_key);

    return (
        <motion.div
            layout
            initial={{ opacity: 0, scale: 0.95 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.95, transition: { duration: 0.2 } }}
            transition={{ type: 'spring', stiffness: 500, damping: 30 }}
            className="group relative flex items-center justify-between gap-3 p-3 rounded-xl bg-muted/50 overflow-hidden origin-top"
        >
            <span className="text-sm font-medium truncate">{apiKey.name}</span>

            <div className="flex items-center gap-1.5">
                <motion.button
                    type="button"
                    layoutId={statsLayoutId}
                    onClick={onViewStats}
                    className="flex size-8 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground active:scale-95"
                    title={t('apiKey.actions.viewStats')}
                >
                    <Info className="size-4" />
                </motion.button>
                <motion.button
                    type="button"
                    layoutId={editLayoutId}
                    onClick={onEdit}
                    className="flex size-8 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground active:scale-95"
                    title={t('apiKey.actions.edit')}
                >
                    <Pencil className="size-4" />
                </motion.button>
                {isLegacyHashed ? (
                    <button
                        type="button"
                        disabled
                        title={t('apiKey.page.legacyHashed')}
                        className="flex size-8 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400"
                    >
                        <AlertTriangle className="size-4" />
                    </button>
                ) : (
                    <CopyIconButton
                        text={apiKey.api_key}
                        className="flex size-8 items-center justify-center rounded-lg bg-primary/10 text-primary transition-all hover:bg-primary hover:text-primary-foreground active:scale-95"
                        copyIconClassName="size-4"
                        checkIconClassName="size-4"
                    />
                )}

                {!confirmDelete && (
                    <motion.button
                        layoutId={deleteLayoutId}
                        onClick={() => setConfirmDelete(true)}
                        className="flex size-8 items-center justify-center rounded-lg bg-destructive/10 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground"
                    >
                        <Trash2 className="size-4" />
                    </motion.button>
                )}
            </div>

            <AnimatePresence>
                {confirmDelete && (
                    <motion.div
                        layoutId={deleteLayoutId}
                        className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-3 rounded-xl"
                        transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                    >
                        <button
                            onClick={() => setConfirmDelete(false)}
                            className="flex size-8 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                        >
                            <X className="size-4" />
                        </button>
                        <button
                            onClick={onDelete}
                            disabled={isDeleting}
                            className="flex-1 h-8 flex items-center justify-center gap-1.5 rounded-lg bg-destructive-foreground text-destructive text-sm font-medium transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50"
                        >
                            <Trash2 className="size-3.5" />
                            {isDeleting ? '...' : t('apiKey.form.confirm')}
                        </button>
                    </motion.div>
                )}
            </AnimatePresence>
        </motion.div>
    );
}

function APIKeyPanelBase({
    idPrefix,
    containerClassName,
    listClassName,
    renderHeaderExtra,
}: {
    idPrefix: string;
    containerClassName: string;
    listClassName: string;
    renderHeaderExtra?: (ctx: {
        disabled: boolean;
        onCloseAllOverlays: () => void;
    }) => React.ReactNode;
}) {
    const t = useTranslations('setting');
    const { data: apiKeys, isLoading: apiKeysLoading, error: apiKeysError } = useAPIKeyList();
    const createAPIKey = useCreateAPIKey();
    const updateAPIKey = useUpdateAPIKey();
    const deleteAPIKey = useDeleteAPIKey();

    const instanceId = useId();
    const addLayoutId = `add-btn-${idPrefix}-${instanceId}`;
    const statsPrefix = `${idPrefix}-stats-${instanceId}`;
    const editPrefix = `${idPrefix}-edit-${instanceId}`;
    const deletePrefix = `${idPrefix}-delete-`;

    const [isAdding, setIsAdding] = useState(false);
    const [viewingStats, setViewingStats] = useState<{ apiKey: APIKey; layoutId: string } | null>(null);
    const [editingKey, setEditingKey] = useState<{ apiKey: APIKey; layoutId: string } | null>(null);
    const [deletingId, setDeletingId] = useState<number | null>(null);

    const sortedApiKeys = useMemo(() => {
        if (!apiKeys) return [];
        return [...apiKeys].sort((a, b) => a.id - b.id);
    }, [apiKeys]);

    const tagSuggestions = useMemo(() => {
        const set = new Set<string>();
        for (const key of apiKeys ?? []) {
            for (const tag of parseTags(key.tags)) set.add(tag);
        }
        return Array.from(set).sort((a, b) => a.localeCompare(b));
    }, [apiKeys]);

    const handleDelete = useCallback((id: number) => {
        setDeletingId(id);
        deleteAPIKey.mutate(id, {
            onSuccess: () => {
                toast.success(t('apiKey.toast.deleteSuccess'));
            },
            onError: (error) => {
                const msg = (error as unknown as ApiError)?.message;
                toast.error(t('apiKey.toast.deleteError'), { description: msg });
            },
            onSettled: () => setDeletingId((cur) => (cur === id ? null : cur)),
        });
    }, [deleteAPIKey, t]);

    const closeAllOverlays = useCallback(() => {
        setIsAdding(false);
        setViewingStats(null);
        setEditingKey(null);
    }, []);

    const disabledHeaderActions = createAPIKey.isPending || isAdding || !!viewingStats || !!editingKey;

    const handleCreate = useCallback((data: Omit<APIKey, 'id'>) => {
        createAPIKey.mutate(data, {
            onSuccess: () => {
                toast.success(t('apiKey.toast.createSuccess'));
                setIsAdding(false);
            },
            onError: (error) => {
                const msg = (error as unknown as ApiError)?.message;
                toast.error(t('apiKey.toast.createError'), { description: msg });
            },
        });
    }, [createAPIKey, t]);

    const handleUpdate = useCallback((apiKey: APIKey, data: Omit<APIKey, 'id'>) => {
        updateAPIKey.mutate({ id: apiKey.id, ...data }, {
            onSuccess: () => {
                toast.success(t('apiKey.toast.updateSuccess'));
                setEditingKey(null);
            },
            onError: (error) => {
                const msg = (error as unknown as ApiError)?.message;
                toast.error(t('apiKey.toast.updateError'), { description: msg });
            },
        });
    }, [t, updateAPIKey]);

    return (
        <div className={containerClassName}>
            <div className="flex items-center justify-between gap-3">
                <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                    <KeyRound className="h-5 w-5" />
                    {t('apiKey.title')}
                </h2>
                <div className="flex items-center gap-2">
                    <motion.button
                        layoutId={addLayoutId}
                        type="button"
                        onClick={() => setIsAdding(true)}
                        disabled={disabledHeaderActions}
                        className="h-9 w-9 flex items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted disabled:opacity-50"
                        title={t('apiKey.add')}
                    >
                        <Plus className="size-4" />
                    </motion.button>
                    {renderHeaderExtra?.({ disabled: disabledHeaderActions, onCloseAllOverlays: closeAllOverlays })}
                </div>
            </div>

            <AnimatePresence>
                {isAdding && (
                    <APIKeyFormOverlay
                        layoutId={addLayoutId}
                        isPending={createAPIKey.isPending}
                        submitLabel={t('apiKey.form.create')}
                        tagSuggestions={tagSuggestions}
                        onSubmit={handleCreate}
                        onClose={() => setIsAdding(false)}
                    />
                )}
            </AnimatePresence>

            <AnimatePresence>
                {viewingStats && (
                    <APIKeyStatsCard
                        layoutId={viewingStats.layoutId}
                        apiKey={viewingStats.apiKey}
                        onClose={() => setViewingStats(null)}
                    />
                )}
            </AnimatePresence>

            <AnimatePresence>
                {editingKey && (
                    <APIKeyFormOverlay
                        layoutId={editingKey.layoutId}
                        apiKey={editingKey.apiKey}
                        isPending={updateAPIKey.isPending}
                        submitLabel={t('apiKey.form.save')}
                        tagSuggestions={tagSuggestions}
                        onSubmit={(data) => handleUpdate(editingKey.apiKey, data)}
                        onClose={() => setEditingKey(null)}
                    />
                )}
            </AnimatePresence>

            <div className={listClassName}>
                {apiKeysLoading ? (
                    <div className="h-full flex items-center justify-center text-sm text-muted-foreground">
                        <Loader className="size-4 animate-spin" />
                    </div>
                ) : apiKeysError ? (
                    <div className="h-full flex items-center justify-center text-sm text-destructive">
                        {t('apiKey.loadFailed')}
                    </div>
                ) : apiKeys?.length === 0 ? (
                    <div className="h-full flex items-center justify-center text-sm text-muted-foreground">
                        {t('apiKey.empty')}
                    </div>
                ) : (
                    <AnimatePresence>
                        {sortedApiKeys.map((apiKey) => {
                            const statsLayoutId = `${statsPrefix}-${apiKey.id}`;
                            const editLayoutId = `${editPrefix}-${apiKey.id}`;
                            const deleteLayoutId = `${deletePrefix}${apiKey.id}`;
                            return (
                                <APIKeyKeyItem
                                    key={apiKey.id}
                                    apiKey={apiKey}
                                    statsLayoutId={statsLayoutId}
                                    editLayoutId={editLayoutId}
                                    deleteLayoutId={deleteLayoutId}
                                    onViewStats={() => {
                                        closeAllOverlays();
                                        setViewingStats({ apiKey, layoutId: statsLayoutId });
                                    }}
                                    onEdit={() => {
                                        closeAllOverlays();
                                        setEditingKey({ apiKey, layoutId: editLayoutId });
                                    }}
                                    onDelete={() => handleDelete(apiKey.id)}
                                    isDeleting={deleteAPIKey.isPending && deletingId === apiKey.id}
                                />
                            );
                        })}
                    </AnimatePresence>
                )}
            </div>
        </div>
    );
}

function APIKeyDialogPanel() {
    const t = useTranslations('setting');
    const { setIsOpen } = useMorphingDialog();
    return (
        <APIKeyPanelBase
            idPrefix="apikey-dialog"
            containerClassName="rounded-3xl border border-border bg-card p-6 space-y-5 relative w-screen max-w-full md:max-w-xl"
            listClassName="space-y-2 h-[calc(100vh-10rem)] overflow-y-auto"
            renderHeaderExtra={() => (
                <button
                    type="button"
                    onClick={() => setIsOpen(false)}
                    className="h-9 w-9 flex items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted"
                    title={t('apiKey.actions.close')}
                >
                    <X className="size-4" />
                </button>
            )}
        />
    );
}

export function SettingAPIKey() {
    return (
        <APIKeyPanelBase
            idPrefix="apikey"
            containerClassName="relative space-y-5 rounded-xl border border-border/35 bg-card p-6 text-card-foreground shadow-md "
            listClassName="h-36 space-y-2 overflow-y-auto rounded-lg border border-border/30 bg-card p-3 shadow-sm"
            renderHeaderExtra={() => (
                <MorphingDialog>
                    <MorphingDialogTrigger className="h-9 w-9 flex items-center justify-center rounded-md border-border/30 bg-card text-muted-foreground shadow-sm transition-colors hover:bg-card">
                        <Maximize2 className="size-4" />
                    </MorphingDialogTrigger>
                    <MorphingDialogContainer>
                        <MorphingDialogContent className="relative">
                            <APIKeyDialogPanel />
                        </MorphingDialogContent>
                    </MorphingDialogContainer>
                </MorphingDialog>
            )}
        />
    );
}
