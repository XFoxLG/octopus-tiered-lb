import {
    AutoGroupType,
    ChannelType,
    RequestRewriteProfile,
    RequestRewriteHeaderProfile,
    SystemMessageStrategy,
    ToolRoleStrategy,
    type Channel,
    useChannelGroupList,
    type RequestRewriteConfig,
    useFetchModel,
    useFetchModelsPerKey,
    type KeyModelResult,
    useTestChannel,
    type TestChannelSummary,
    type ChannelProxyMode,
    type UpstreamProtocol,
    type ChannelReasoningBufferStrategy,
} from '@/api/endpoints/channel';
import { ProxySelector } from '@/components/modules/proxy-pool/ProxySelector';
import { useSettingList, SettingKey } from '@/api/endpoints/setting';
import { ConnectionEditor } from './ConnectionEditor';
import { canFetchConnectionModels, hasConnectionAddress } from './connection-config';
import { normalizeFetchedModels, toggleModelSelection } from './model-picker';
import {
    applyPerKeyModelFill,
    planPerKeyModelFill,
    type PerKeyFillMatch,
} from './key-model-fill';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Hint } from '@/components/ui/hint';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { toast } from '@/components/common/Toast';
import { cn } from '@/lib/utils';
import { useTranslations } from 'next-intl';
import { useEffect, useMemo, useRef, useState } from 'react';
import { RefreshCw, X, Plus, FlaskConical, CheckCircle2, AlertTriangle, Trash2, Sparkles, Orbit, Layers3, KeyRound, Search, Check, ListFilter, ChevronDown, ChevronRight } from 'lucide-react';
import { getModelIcon } from '@/lib/model-icons';

export interface ChannelKeyFormItem {
    id?: number;
    enabled: boolean;
    channel_key: string;
    priority?: number;
    status_code?: number;
    last_use_time_stamp?: number;
    total_cost?: number;
    remark?: string;
    supported_models?: string;
}

export interface ChannelFormData {
    connection_config?: Channel['connection_config'];
    name: string;
    group_id: number;
    type: ChannelType;
    base_urls: Channel['base_urls'];
    custom_header: Channel['custom_header'];
    channel_proxy: string;
    param_override: string;
    outbound_format_override: string;
    /** 渠道声明的上游协议（有序）。空数组 = 沿用分组 outbound_format。 */
    upstream_protocols: UpstreamProtocol[];
    /** 渠道级超时覆盖（秒）：0 = 跟随分组，-1 = 关闭，>0 = 秒数。 */
    first_token_time_out: number;
    attempt_time_out: number;
    stream_idle_timeout: number;
    /** 渠道级推理缓冲策略；空串 = 跟随分组。 */
    reasoning_buffer_strategy: ChannelReasoningBufferStrategy;
    request_rewrite: RequestRewriteConfig;
    relay_log_raw_sse_until: number;
    keys: ChannelKeyFormItem[];
    model: string;
    custom_model: string;
    enabled: boolean;
    proxy_mode: ChannelProxyMode;
    proxy_config_id: number | null;
    auto_sync: boolean;
    auto_sync_key_models: boolean;
    auto_group: AutoGroupType;
    skip_model_test: boolean;
    disposable: boolean;
    expire_at: string;
    key_selection_strategy: string;
    match_regex: string;
    max_concurrency: number;
    rpm_limit: number;
    retryable_status_codes: string;
    retryable_keywords: string;
    non_retryable_status_codes: string;
    error_message_template: string;
}

/**
 * 从渠道数据推导表单展示用的代理模式，镜像后端 resolveChannelProxy 的兼容语义
 *（issue #195）：历史数据可能 proxy_mode=direct 但旧 proxy 布尔仍为 true，
 * 此时按 legacy 字段回退展示，避免"开关开着但地址为空"的误导状态。
 */
export function deriveChannelProxyMode(channel: Pick<Channel, 'proxy_mode' | 'proxy' | 'channel_proxy'>): ChannelProxyMode {
    const mode = channel.proxy_mode;
    if (mode && mode !== 'direct') return mode;
    if (!channel.proxy) return 'direct';
    return channel.channel_proxy?.trim() ? 'pool' : 'system';
}

export function createDefaultRequestRewriteFormData(): RequestRewriteConfig {
    return {
        enabled: false,
        profile: RequestRewriteProfile.Preserve,
        tool_role_strategy: ToolRoleStrategy.Keep,
        system_message_strategy: SystemMessageStrategy.Keep,
        header_profile: RequestRewriteHeaderProfile.None,
    };
}

export function normalizeRequestRewriteFormData(config?: RequestRewriteConfig | null): RequestRewriteConfig {
    return {
        enabled: config?.enabled ?? false,
        profile: config?.profile ?? RequestRewriteProfile.Preserve,
        tool_role_strategy: config?.tool_role_strategy ?? ToolRoleStrategy.Keep,
        system_message_strategy: config?.system_message_strategy ?? SystemMessageStrategy.Keep,
        header_profile: config?.header_profile ?? RequestRewriteHeaderProfile.None,
    };
}

export function isRequestRewriteSupportedChannelType(channelType: ChannelType): boolean {
    return channelType === ChannelType.OpenAIChat || channelType === ChannelType.MiMoChat || channelType === ChannelType.OpenAIResponse;
}

export function getEffectiveRequestRewriteFormData(channelType: ChannelType, config?: RequestRewriteConfig | null, connection?: Channel['connection_config']): RequestRewriteConfig {
    const normalized = normalizeRequestRewriteFormData(config);
    if (connection ? connection.endpoints.some((e) => e.protocol === 'chat' || e.protocol === 'responses') : isRequestRewriteSupportedChannelType(channelType)) {
        return normalized;
    }

    return {
        ...normalized,
        enabled: false,
    };
}


/**
 * 切换一个协议的勾选状态，保持「勾选顺序 = 优先级顺序」。
 *
 * 新勾选的协议追加到末尾（不插队），取消勾选直接移除。后端 NormalizeUpstreamProtocols
 * 同样保留顺序，所以前端不需要额外排序。
 */
export function toggleUpstreamProtocol(
    protocols: UpstreamProtocol[],
    protocol: UpstreamProtocol,
    checked: boolean,
): UpstreamProtocol[] {
    if (checked) {
        if (protocols.includes(protocol)) return protocols;
        return [...protocols, protocol];
    }
    return protocols.filter((item) => item !== protocol);
}

/**
 * 判断某个协议与已选集合是否冲突（用于把冲突项标成不可选并给出原因）。
 *
 * 与后端 NormalizeUpstreamProtocols 的互斥规则保持一致：
 * - passthrough / raw 是整体透传，不能与任何其它协议共存；
 * - chat_only / responses_only / messages_only 与同协议的宽松模式互相矛盾。
 */

/** 上报当前选择里是否存在会被后端拒绝的互斥组合。 */
export function upstreamProtocolConflict(protocols: UpstreamProtocol[]): boolean {
    if (protocols.length <= 1) return false;
    if (protocols.includes('passthrough') || protocols.includes('raw')) return true;
    const exclusivePairs: Array<[UpstreamProtocol, UpstreamProtocol]> = [
        ['chat_only', 'chat'],
        ['responses_only', 'responses'],
        ['messages_only', 'messages'],
    ];
    return exclusivePairs.some(([strict, loose]) => protocols.includes(strict) && protocols.includes(loose));
}

/**
 * 把超时输入框的字符串转成数字。空串与非法输入都当作 0（= 跟随分组），
 * 保持后端 -1 / 0 / >0 三态语义中的「未覆盖」值。
 */
export function normalizeTimeoutInputValue(raw: string): number {
    const trimmed = raw.trim();
    if (trimmed === '') return 0;
    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed)) return 0;
    return Math.trunc(parsed);
}

export interface ChannelFormProps {
    formData: ChannelFormData;
    onFormDataChange: (data: ChannelFormData) => void;
    onSubmit: (event: React.FormEvent<HTMLFormElement>) => void;
    isPending: boolean;
    submitText: string;
    pendingText: string;
    onCancel?: () => void;
    cancelText?: string;
    idPrefix?: string;
    channelId?: number;
    layout?: 'default' | 'create';
}

function SectionHeader({
    icon: Icon,
    title,
    hint,
}: {
    icon: typeof Sparkles;
    title: string;
    hint?: string;
}) {
    return (
        <h3 className="flex min-w-0 items-center gap-2 text-sm font-semibold text-foreground">
            <Icon className="size-4 shrink-0 text-muted-foreground" />
            <span>{title}</span>
            {hint ? <Hint text={hint} /> : null}
        </h3>
    );
}

interface ModelPickerDialogPanelProps {
    models: string[];
    draftSelected: string[];
    onDraftChange: (models: string[]) => void;
    isLoading: boolean;
    onApply: () => void;
    perKeyResults?: KeyModelResult[] | null;
    perKeyLoading?: boolean;
    /** 把单个 key 的抓取结果回填到对应表单 key 的 supported_models */
    onFillPerKeyResult?: (result: KeyModelResult, resultIndex: number) => void;
    /** 把所有 passed 的抓取结果批量回填 */
    onFillAllPerKeyResults?: () => void;
}

interface ModelProviderGroup {
    label: string;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    Avatar: React.ComponentType<any>;
    color: string;
    models: string[];
}

function groupModelsByProvider(models: string[]): ModelProviderGroup[] {
    const groupMap = new Map<string, { label: string; Avatar: ModelProviderGroup['Avatar']; color: string; models: string[] }>();

    for (const model of models) {
        const { label, Avatar, color } = getModelIcon(model);
        const existing = groupMap.get(label);
        if (existing) {
            existing.models.push(model);
        } else {
            groupMap.set(label, { label, Avatar, color, models: [model] });
        }
    }

    return Array.from(groupMap.values()).sort((a, b) => {
        if (a.label === 'Model') return 1;
        if (b.label === 'Model') return -1;
        return a.label.localeCompare(b.label);
    });
}

function ModelPickerDialogPanel({ models, draftSelected, onDraftChange, isLoading, onApply, perKeyResults, perKeyLoading, onFillPerKeyResult, onFillAllPerKeyResults }: ModelPickerDialogPanelProps) {
    const t = useTranslations('channel.form.modelPicker');
    const { setIsOpen } = useMorphingDialog();
    const [searchTerm, setSearchTerm] = useState('');
    const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({});
    const [viewMode, setViewMode] = useState<'all' | 'perKey'>('all');
    const hasPerKeyData = perKeyResults && perKeyResults.length > 0;
    const showPerKey = perKeyLoading || hasPerKeyData;
    const canFillAllPerKey =
        typeof onFillAllPerKeyResults === 'function' &&
        !!perKeyResults?.some((result) => result.passed && (result.models?.length ?? 0) > 0);

    const normalizedSearch = searchTerm.trim().toLowerCase();
    const isSearching = normalizedSearch.length > 0;

    const groups = useMemo(() => groupModelsByProvider(models), [models]);

    const filteredGroups = useMemo(() => {
        if (!isSearching) return groups;
        return groups
            .map((group) => ({
                ...group,
                models: group.models.filter((m) => m.toLowerCase().includes(normalizedSearch)),
            }))
            .filter((group) => group.models.length > 0);
    }, [groups, normalizedSearch, isSearching]);

    const filteredModels = useMemo(
        () => filteredGroups.flatMap((g) => g.models),
        [filteredGroups]
    );

    const selectedSet = new Set(draftSelected);
    const allFilteredSelected = filteredModels.length > 0 && filteredModels.every((m) => selectedSet.has(m));

    const toggleModel = (model: string) => {
        onDraftChange(
            draftSelected.includes(model)
                ? draftSelected.filter((item) => item !== model)
                : [...draftSelected, model]
        );
    };

    const toggleGroupSelection = (groupModels: string[]) => {
        onDraftChange(toggleModelSelection(draftSelected, groupModels));
    };

    const toggleGroupCollapsed = (label: string) => {
        setCollapsedGroups((prev) => ({ ...prev, [label]: !prev[label] }));
    };

    const handleSelectFiltered = () => {
        onDraftChange(toggleModelSelection(draftSelected, filteredModels));
    };

    const handleApply = () => {
        onApply();
        setIsOpen(false);
    };

    return (
        <div className="relative flex h-full min-h-0 flex-col overflow-hidden rounded-xl border border-border/35 bg-card text-card-foreground shadow-md">
            <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_16%_14%,color-mix(in_oklch,var(--primary)_18%,transparent)_0%,transparent_30%),linear-gradient(180deg,color-mix(in_oklch,white_18%,transparent),transparent_28%)]" />
            <MorphingDialogTitle className="shrink-0">
                <header className="relative flex items-center justify-between gap-4 border-b border-border/20 px-5 py-4 md:px-6">
                    <div className="min-w-0 space-y-2">
                        <div className="flex items-center gap-2">
                            <span className="h-2.5 w-10 rounded-full bg-primary/18 shadow-sm" />
                            <span className="h-2.5 w-20 rounded-full bg-card shadow-inner" />
                        </div>
                        <div className="space-y-1">
                            <h3 className="truncate text-lg font-semibold tracking-tight text-card-foreground md:text-xl">
                                {t('title')}
                            </h3>
                            <p className="text-xs text-muted-foreground">
                                {t('description', { count: models.length })}
                            </p>
                        </div>
                    </div>
                    <MorphingDialogClose className="relative right-0 top-0" />
                </header>
            </MorphingDialogTitle>

            <MorphingDialogDescription disableLayoutAnimation className="relative flex min-h-0 flex-1 flex-col gap-4 px-4 py-4 md:px-6">
                {showPerKey && (
                    <div className="flex shrink-0 items-center gap-1 rounded-lg border border-border/25 bg-muted/40 p-1">
                        <button
                            type="button"
                            onClick={() => setViewMode('all')}
                            className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${viewMode === 'all' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
                        >
                            {t('allModels')}
                        </button>
                        <button
                            type="button"
                            onClick={() => setViewMode('perKey')}
                            className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${viewMode === 'perKey' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
                        >
                            {t('perKey')}
                        </button>
                    </div>
                )}
                {viewMode === 'all' && (
                    <>
                <div className="relative shrink-0">
                    <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        value={searchTerm}
                        onChange={(event) => setSearchTerm(event.target.value)}
                        placeholder={t('searchPlaceholder')}
                        className="h-11 rounded-lg pl-9"
                    />
                </div>

                <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        <Badge variant="secondary" className="rounded-full">
                            {t('selectedCount', { count: draftSelected.length })}
                        </Badge>
                        <span>{t('filteredCount', { count: filteredModels.length })}</span>
                    </div>
                    <div className="flex items-center gap-2">
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => onDraftChange([])}
                            disabled={draftSelected.length === 0}
                            className="h-8 rounded-lg px-2 text-xs text-muted-foreground hover:text-foreground"
                        >
                            {t('clear')}
                        </Button>
                        <Button
                            type="button"
                            variant="secondary"
                            size="sm"
                            onClick={handleSelectFiltered}
                            disabled={filteredModels.length === 0}
                            className="h-8 rounded-lg px-2 text-xs"
                        >
                            <ListFilter className="size-3.5" />
                            {allFilteredSelected ? t('unselectFiltered') : t('selectFiltered')}
                        </Button>
                    </div>
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border border-border/25 bg-card p-2 shadow-sm">
                    {isLoading ? (
                        <div className="flex h-40 items-center justify-center gap-2 text-sm text-muted-foreground">
                            <RefreshCw className="size-4 animate-spin" />
                            {t('loading')}
                        </div>
                    ) : filteredGroups.length > 0 ? (
                        <div className="flex flex-col gap-1">
                            {filteredGroups.map((group) => {
                                const isCollapsed = !isSearching && collapsedGroups[group.label];
                                const groupSelectedCount = group.models.filter((m) => selectedSet.has(m)).length;
                                const allGroupSelected = group.models.length > 0 && groupSelectedCount === group.models.length;
                                const Avatar = group.Avatar;

                                return (
                                    <div key={group.label} className="rounded-lg">
                                        <div className="flex items-center gap-2 px-1 py-1.5">
                                            <button
                                                type="button"
                                                onClick={() => toggleGroupCollapsed(group.label)}
                                                className="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground/60 hover:text-foreground transition-colors"
                                            >
                                                <ChevronRight
                                                    className={`size-3.5 transition-transform duration-150 ${isCollapsed ? '' : 'rotate-90'}`}
                                                />
                                            </button>
                                            <Avatar className="size-4 shrink-0" />
                                            <span className="text-xs font-medium text-foreground">{group.label}</span>
                                            <Badge variant="secondary" className="h-4 rounded-full px-1.5 text-[0.625rem] tabular-nums">
                                                {groupSelectedCount}/{group.models.length}
                                            </Badge>
                                            <button
                                                type="button"
                                                onClick={() => toggleGroupSelection(group.models)}
                                                className={`ml-auto shrink-0 rounded px-2 py-0.5 text-[0.625rem] font-medium transition-colors ${
                                                    allGroupSelected
                                                        ? 'text-primary hover:text-primary/70'
                                                        : 'text-muted-foreground/60 hover:text-foreground'
                                                }`}
                                            >
                                                {allGroupSelected ? t('unselectFiltered') : t('selectFiltered')}
                                            </button>
                                        </div>
                                        {!isCollapsed && (
                                            <div className="grid gap-2 sm:grid-cols-2 pl-7 pb-1">
                                                {group.models.map((model) => {
                                                    const selected = selectedSet.has(model);
                                                    return (
                                                        <button
                                                            key={model}
                                                            type="button"
                                                            onClick={() => toggleModel(model)}
                                                            className={`flex min-w-0 items-center gap-3 rounded-lg border px-3 py-2.5 text-left text-sm transition-colors ${
                                                                selected
                                                                    ? 'border-primary/30 bg-primary/10 text-foreground'
                                                                    : 'border-border/25 bg-background/40 text-muted-foreground hover:border-border/60 hover:text-foreground'
                                                            }`}
                                                        >
                                                            <span className={`flex size-5 shrink-0 items-center justify-center rounded-md border ${
                                                                selected ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'
                                                            }`}>
                                                                {selected ? <Check className="size-3.5" /> : null}
                                                            </span>
                                                            <span className="min-w-0 flex-1 truncate font-mono" title={model}>
                                                                {model}
                                                            </span>
                                                        </button>
                                                    );
                                                })}
                                            </div>
                                        )}
                                    </div>
                                );
                            })}
                        </div>
                    ) : (
                        <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
                            {models.length === 0 ? t('empty') : t('noSearchResult')}
                        </div>
                    )}
                </div>
                    </>
                )}
                {viewMode === 'perKey' && (
                    <>
                    {hasPerKeyData && onFillAllPerKeyResults && (
                        <div className="flex shrink-0 items-center justify-between gap-2">
                            <p className="min-w-0 flex-1 text-xs leading-5 text-muted-foreground">
                                {t('perKeyHint')}
                            </p>
                            <Button
                                type="button"
                                variant="secondary"
                                size="sm"
                                onClick={onFillAllPerKeyResults}
                                disabled={!canFillAllPerKey}
                                className="h-8 shrink-0 rounded-lg px-2 text-xs"
                            >
                                <Sparkles className="size-3.5" />
                                {t('fillAll')}
                            </Button>
                        </div>
                    )}
                    <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border border-border/25 bg-card p-2 shadow-sm">
                        {perKeyLoading ? (
                            <div className="flex h-40 items-center justify-center gap-2 text-sm text-muted-foreground">
                                <RefreshCw className="size-4 animate-spin" />
                                {t('loading')}
                            </div>
                        ) : hasPerKeyData ? (
                            <div className="flex flex-col gap-3">
                                {perKeyResults!.map((result, idx) => (
                                    <div
                                        key={`${result.key_id ?? 0}-${result.key_masked ?? ''}-${idx}`}
                                        className={`rounded-lg border p-3 ${result.passed ? 'border-border/25 bg-background/40' : 'border-red-500/20 bg-red-500/5'}`}
                                    >
                                        <div className="mb-2 flex flex-wrap items-center gap-2">
                                            {result.passed ? (
                                                <CheckCircle2 className="size-4 shrink-0 text-green-500" />
                                            ) : (
                                                <AlertTriangle className="size-4 shrink-0 text-red-400" />
                                            )}
                                            <span className="truncate text-xs font-mono text-foreground">
                                                {result.key_masked}
                                            </span>
                                            {result.key_remark && (
                                                <Badge variant="secondary" className="h-4 rounded-full px-1.5 text-[0.625rem]">
                                                    {result.key_remark}
                                                </Badge>
                                            )}
                                            {result.passed && result.models.length > 0 && (
                                                <Button
                                                    type="button"
                                                    variant="secondary"
                                                    size="sm"
                                                    onClick={() => toggleGroupSelection(result.models)}
                                                    className="ml-auto h-7 shrink-0 rounded-md px-2 text-xs"
                                                >
                                                    <ListFilter className="size-3" />
                                                    {result.models.every((model) => selectedSet.has(model))
                                                        ? t('unselectKeyModels')
                                                        : t('selectKeyModels')}
                                                </Button>
                                            )}
                                            {onFillPerKeyResult && (
                                                <Button
                                                    type="button"
                                                    variant="ghost"
                                                    size="sm"
                                                    onClick={() => onFillPerKeyResult(result, idx)}
                                                    disabled={!result.passed || (result.models?.length ?? 0) === 0}
                                                    aria-label={t('fillOne')}
                                                    className="h-7 shrink-0 rounded-md px-2 text-xs text-muted-foreground/70 hover:bg-transparent hover:text-foreground disabled:opacity-40"
                                                >
                                                    <Check className="size-3" />
                                                    {t('fillOne')}
                                                </Button>
                                            )}
                                        </div>
                                        {result.passed && result.models.length > 0 ? (
                                            <div className="flex flex-wrap gap-1.5">
                                                {result.models.map((model) => {
                                                    const selected = selectedSet.has(model);
                                                    return (
                                                        <button
                                                            key={model}
                                                            type="button"
                                                            onClick={() => toggleModel(model)}
                                                            className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-mono transition-colors ${
                                                                selected
                                                                    ? 'border-primary/30 bg-primary/10 text-foreground'
                                                                    : 'border-border/25 bg-background/40 text-muted-foreground hover:border-border/60 hover:text-foreground'
                                                            }`}
                                                        >
                                                            <span className={`flex size-3.5 shrink-0 items-center justify-center rounded-full ${
                                                                selected ? 'bg-primary text-primary-foreground' : 'border border-border'
                                                            }`}>
                                                                {selected ? <Check className="size-2.5" /> : null}
                                                            </span>
                                                            {model}
                                                        </button>
                                                    );
                                                })}
                                            </div>
                                        ) : !result.passed && result.message ? (
                                            <p className="text-xs text-red-400">{result.message}</p>
                                        ) : (
                                            <p className="text-xs text-muted-foreground">{t('noModels')}</p>
                                        )}
                                    </div>
                                ))}
                            </div>
                        ) : (
                            <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
                                {t('empty')}
                            </div>
                        )}
                    </div>
                    </>
                )}

                <div className="flex shrink-0 flex-col gap-2 border-t border-border/20 pt-4 sm:flex-row">
                    <Button
                        type="button"
                        variant="secondary"
                        onClick={() => setIsOpen(false)}
                        className="h-11 rounded-lg sm:flex-1"
                    >
                        {t('cancel')}
                    </Button>
                    <Button
                        type="button"
                        onClick={handleApply}
                        className="h-11 rounded-lg sm:flex-1"
                    >
                        {t('apply', { count: draftSelected.length })}
                    </Button>
                </div>
            </MorphingDialogDescription>
        </div>
    );
}

export function ChannelForm({
    formData,
    onFormDataChange,
    onSubmit,
    isPending,
    submitText,
    pendingText,
    onCancel,
    cancelText,
    idPrefix = 'channel',
    channelId,
    layout = 'default',
}: ChannelFormProps) {
    const t = useTranslations('channel.form');
    const { data: settings } = useSettingList();
    const { data: channelGroups = [] } = useChannelGroupList();
    const [formOpenedAt] = useState(() => Math.floor(Date.now() / 1000));
    const requestRewriteSupported = formData.connection_config ? formData.connection_config.endpoints.some((e) => e.protocol === 'chat' || e.protocol === 'responses') : isRequestRewriteSupportedChannelType(formData.type);

    const isCreateLayout = layout === 'create';
    const sectionClassName = isCreateLayout
        ? 'min-w-0 space-y-3 border-b border-border pb-5'
        : 'space-y-4 rounded-lg bg-card/70 p-4 md:p-5';
    const labelClassName = 'text-sm font-medium text-card-foreground';
    const fieldGroupClassName = 'space-y-2';

    const globalKeyStrategy = settings?.find((s) => s.key === SettingKey.KeySelectionStrategy)?.value ?? 'cost';
    const effectiveKeyStrategy = formData.key_selection_strategy || globalKeyStrategy;
    const showPriorityInput = effectiveKeyStrategy === 'priority';
    const rawSSECaptureActive = formData.relay_log_raw_sse_until > formOpenedAt;
    const rawSSEExpiry = rawSSECaptureActive
        ? new Date(formData.relay_log_raw_sse_until * 1000).toLocaleString()
        : t('rawSSEDisabled');

    const setRawSSECaptureDuration = (durationSeconds: number) => {
        onFormDataChange({
            ...formData,
            relay_log_raw_sse_until: durationSeconds > 0 ? Math.floor(Date.now() / 1000) + durationSeconds : 0,
        });
    };
    // 本仓没有号池（pool）渠道概念，表单里始终可以编辑 key，
    // 因此“回填到该 key / 全部填充”恒可用（与上游 canFillPerKeyModels 语义对齐）。
    const canFillPerKeyModels = true;

    // Ensure the form always shows at least 1 row for base_urls / keys / custom_header.
    // This avoids "empty list" UI and also keeps URL + APIKEY layout consistent.
    useEffect(() => {
        if (!formData.base_urls || formData.base_urls.length === 0) {
            onFormDataChange({ ...formData, base_urls: [{ url: '', delay: 0, suffix_mode: 'openai_compat' }] });
            return;
        }
        if (!formData.keys || formData.keys.length === 0) {
            onFormDataChange({ ...formData, keys: [{ enabled: true, channel_key: '', priority: 0 }] });
            return;
        }
        if (!formData.custom_header || formData.custom_header.length === 0) {
            onFormDataChange({ ...formData, custom_header: [{ header_key: '', header_value: '' }] });
        }
    }, [formData, onFormDataChange]);

    useEffect(() => {
        if (formData.group_id !== 0 || channelGroups.length === 0) {
            return;
        }
        const defaultGroup = channelGroups.find((item) => item.is_default) ?? channelGroups[0];
        if (!defaultGroup) {
            return;
        }
        onFormDataChange({ ...formData, group_id: defaultGroup.id });
    }, [channelGroups, formData, onFormDataChange]);

    const autoModels = formData.model
        ? formData.model.split(',').map((m) => m.trim()).filter(Boolean)
        : [];
    const customModels = formData.custom_model
        ? formData.custom_model.split(',').map((m) => m.trim()).filter(Boolean)
        : [];
    const [inputValue, setInputValue] = useState('');
    const [fetchedModels, setFetchedModels] = useState<string[]>([]);
    const inputRef = useRef<HTMLInputElement>(null);

    const fetchModel = useFetchModel();
    const fetchModelsPerKey = useFetchModelsPerKey();
    const isFetchingModels = fetchModel.isPending || fetchModelsPerKey.isPending;
    const testChannel = useTestChannel();
    const [testSummary, setTestSummary] = useState<TestChannelSummary | null>(null);
    const [modelPickerDraft, setModelPickerDraft] = useState<string[]>([]);
    const [perKeyResults, setPerKeyResults] = useState<KeyModelResult[] | null>(null);

    const effectiveKey =
        formData.keys.find((k) => k.enabled && k.channel_key.trim())?.channel_key.trim() || '';

    const updateModels = (nextAuto: string[], nextCustom: string[]) => {
        const model = nextAuto.join(',');
        const custom_model = nextCustom.join(',');
        if (formData.model === model && formData.custom_model === custom_model) return;
        onFormDataChange({ ...formData, model, custom_model });
    };

    const applyFetchedModelSelection = () => {
        const customModelSet = new Set(customModels);
        const nextAutoModels = Array.from(new Set(modelPickerDraft)).filter((model) => !customModelSet.has(model));
        updateModels(nextAutoModels, customModels);
        toast.success(t('modelPicker.applySuccess', { count: nextAutoModels.length }));
    };

    /**
     * 描述一个按 key 抓取结果（用于 toast 文案）：优先备注，其次脱敏 key，最后回退到序号。
     */
    const describePerKeyResult = (result: KeyModelResult, index: number) =>
        result.key_remark?.trim() || result.key_masked?.trim() || t('modelPicker.fillKeyFallback', { index: index + 1 });

    /**
     * 将回填计划落到表单 keys 状态（只改状态，不调后端），
     * 并把“成功 / 抓取失败跳过 / 上游返回空列表跳过 / 匹配不上”汇总成一条 toast。
     */
    const applyPerKeyFillPlan = (results: KeyModelResult[]) => {
        const plan = planPerKeyModelFill(results, formData.keys);

        if (plan.fills.length > 0) {
            onFormDataChange({
                ...formData,
                keys: applyPerKeyModelFill(formData.keys, plan.fills),
            });
        }

        const skippedLabels = plan.skippedFailed.map((match: PerKeyFillMatch) =>
            describePerKeyResult(match.result, match.resultIndex),
        );
        const emptyLabels = plan.skippedEmpty.map((match: PerKeyFillMatch) =>
            describePerKeyResult(match.result, match.resultIndex),
        );
        const unmatchedLabels = plan.unmatched.map((match: PerKeyFillMatch) =>
            describePerKeyResult(match.result, match.resultIndex),
        );

        // sonner 的 description 默认不保留换行，用分隔符拼接保证多段提示都能完整渲染
        const description = [
            skippedLabels.length > 0 ? t('modelPicker.fillSkippedFailed', { count: skippedLabels.length, keys: skippedLabels.join(', ') }) : '',
            emptyLabels.length > 0 ? t('modelPicker.fillSkippedEmpty', { count: emptyLabels.length, keys: emptyLabels.join(', ') }) : '',
            unmatchedLabels.length > 0 ? t('modelPicker.fillUnmatched', { count: unmatchedLabels.length, keys: unmatchedLabels.join(', ') }) : '',
        ].filter(Boolean).join(' · ');

        if (plan.fills.length === 0) {
            toast.warning(description || t('modelPicker.fillNothing'));
            return;
        }

        toast.success(t('modelPicker.fillAllSuccess', { count: plan.fills.length }), description ? { description } : undefined);
    };

    const handleFillAllPerKeyResults = () => {
        if (!perKeyResults || perKeyResults.length === 0) {
            toast.warning(t('modelPicker.fillNothing'));
            return;
        }
        applyPerKeyFillPlan(perKeyResults);
    };

    const handleFillPerKeyResult = (result: KeyModelResult, resultIndex: number) => {
        const plan = planPerKeyModelFill([result], formData.keys);

        if (plan.unmatched.length > 0) {
            toast.warning(t('modelPicker.fillUnmatched', {
                count: 1,
                keys: describePerKeyResult(result, resultIndex),
            }));
            return;
        }

        if (plan.fills.length === 0) {
            // 按钮对 passed=false / models 为空的结果已禁用，这里是防御分支：
            // 优先把“为何没填”的具体原因告知用户，而不是一句笼统的“无可填充”。
            const failedMatch = plan.skippedFailed[0];
            if (failedMatch) {
                toast.warning(t('modelPicker.fillSkippedFailed', {
                    count: 1,
                    keys: describePerKeyResult(failedMatch.result, failedMatch.resultIndex),
                }));
                return;
            }

            const emptyMatch = plan.skippedEmpty[0];
            if (emptyMatch) {
                toast.warning(t('modelPicker.fillSkippedEmpty', {
                    count: 1,
                    keys: describePerKeyResult(emptyMatch.result, emptyMatch.resultIndex),
                }));
                return;
            }

            toast.warning(t('modelPicker.fillNothing'));
            return;
        }

        onFormDataChange({
            ...formData,
            keys: applyPerKeyModelFill(formData.keys, plan.fills),
        });
        // 计数以“实际写入的非空模型数”为准，而不是原始 result.models.length（后者可能含空白项）
        const filledCount = plan.fills[0].supportedModels ? plan.fills[0].supportedModels.split(',').filter(Boolean).length : 0;
        toast.success(t('modelPicker.fillOneSuccess', {
            count: filledCount,
            key: describePerKeyResult(result, resultIndex),
        }));
    };

    const normalizedHeaders = useMemo(() =>
        (formData.custom_header ?? [])
            .map((h) => ({ header_key: h.header_key.trim(), header_value: h.header_value }))
            .filter((h) => h.header_key && h.header_value !== ''),
        [formData.custom_header]
    );

    const buildTestPayload = () => ({
        connection_config: formData.connection_config,
        skip_model_test: formData.skip_model_test,
        type: formData.type,
        base_urls: (formData.base_urls ?? []).filter((u) => u.url.trim()).map((u) => ({
            url: u.url.trim(),
            delay: Number(u.delay || 0),
            suffix_mode: u.suffix_mode && u.suffix_mode !== 'auto' ? u.suffix_mode : undefined,
        })),
        keys: formData.keys
            .filter((k) => k.channel_key.trim())
            .map((k) => ({ enabled: k.enabled, channel_key: k.channel_key.trim(), remark: k.remark ?? '' })),
        proxy_mode: formData.proxy_mode,
        proxy_config_id: formData.proxy_mode === 'pool' ? formData.proxy_config_id : null,
        proxy: formData.proxy_mode !== 'direct',
        channel_proxy: formData.channel_proxy?.trim() || '',
        match_regex: formData.match_regex.trim() || '',
        custom_header: normalizedHeaders,
        model: formData.model,
        custom_model: formData.custom_model,
        name: formData.name,
        enabled: formData.enabled,
        auto_sync: formData.auto_sync,
        auto_group: formData.auto_group,
        param_override: formData.param_override.trim() || '',
        outbound_format_override: formData.outbound_format_override.trim() || '',
    });

    const handleTestChannel = () => {
        setTestSummary(null);
        testChannel.mutate(buildTestPayload(), {
            onSuccess: (data) => {
                setTestSummary(data);
                toast.success(data.passed ? t('test.success') : t('test.partialSuccess'));
            },
            onError: (error) => {
                const errorMessage = error instanceof Error ? error.message : String(error);
                toast.error(t('test.failed'), { description: errorMessage });
            },
        });
    };

    const maskKey = (secret: string): string => {
        const trimmed = secret.trim();
        if (!trimmed) return '';
        if (trimmed.length <= 8) return trimmed;
        return trimmed.slice(0, 4) + '...' + trimmed.slice(-4);
    };

    const handleRemoveFailedKeys = () => {
        if (!testSummary) return;
        const failedIds = new Set<string>();
        for (const result of testSummary.results) {
            if (!result.passed) {
                failedIds.add(`${result.key_masked ?? ''}||${result.key_remark ?? ''}`);
            }
        }
        if (failedIds.size === 0) return;
        const nextKeys = formData.keys.filter((k) => {
            const id = `${maskKey(k.channel_key)}||${k.remark ?? ''}`;
            return !failedIds.has(id);
        });
        if (nextKeys.length === 0) {
            nextKeys.push({ enabled: true, channel_key: '' });
        }
        onFormDataChange({ ...formData, keys: nextKeys });
        setTestSummary(null);
    };

    const handleRefreshModels = async () => {
        if (!canFetchConnectionModels(formData.connection_config, formData.base_urls) || !effectiveKey) return;
        setModelPickerDraft(autoModels);
        setFetchedModels([]);
        setPerKeyResults(null);

        const payload = {
            connection_config: formData.connection_config,
            type: formData.type,
            base_urls: formData.base_urls,
            keys: formData.keys
                .filter((k) => k.channel_key.trim())
                .map((k) => ({ enabled: k.enabled, channel_key: k.channel_key.trim() })),
            proxy_mode: formData.proxy_mode,
            proxy_config_id: formData.proxy_mode === 'pool' ? formData.proxy_config_id : null,
            proxy: formData.proxy_mode !== 'direct',
            channel_proxy: formData.channel_proxy?.trim() || '',
            match_regex: formData.match_regex.trim() || '',
            custom_header: normalizedHeaders,
        };

        const handleModels = (data: unknown) => {
            const models = normalizeFetchedModels(data);
            setFetchedModels(models);
            if (models.length > 0) {
                toast.success(t('modelRefreshSuccess', { count: models.length }));
            } else {
                toast.warning(t('modelRefreshEmpty'));
            }
        };
        const handleError = (error: unknown) => {
            setFetchedModels([]);
            setPerKeyResults(null);
            const errorMessage = error instanceof Error ? error.message : String(error);
            toast.error(t('modelRefreshFailed'), { description: errorMessage });
        };

        const enabledKeys = formData.keys.filter((k) => k.enabled && k.channel_key.trim());
        if (enabledKeys.length > 1) {
            fetchModelsPerKey.mutate(
                {
                    ...payload,
                    // 保留 id / remark，供逐 key 回填精确定位。
                    keys: enabledKeys.map((k) => ({
                        id: k.id,
                        enabled: true,
                        channel_key: k.channel_key.trim(),
                        remark: k.remark ?? '',
                    })),
                },
                {
                    onSuccess: (data) => {
                        setPerKeyResults(data.results.map((result) => ({
                            ...result,
                            models: normalizeFetchedModels(result.models),
                        })));
                        if (!data.results.some((result) => result.passed)) {
                            setFetchedModels([]);
                            toast.error(t('modelRefreshFailed'));
                            return;
                        }
                        handleModels(data.all_models);
                    },
                    onError: handleError,
                }
            );
            return;
        }

        fetchModel.mutate(payload, {
            onSuccess: handleModels,
            onError: handleError,
        });
    };

    const handleAddModel = (model: string) => {
        const trimmedModel = model.trim();
        if (trimmedModel && !customModels.includes(trimmedModel) && !autoModels.includes(trimmedModel)) {
            updateModels(autoModels, [...customModels, trimmedModel]);
        }
        setInputValue('');
    };

    const handleRemoveAutoModel = (model: string) => {
        updateModels(autoModels.filter(m => m !== model), customModels);
    };

    const handleRemoveCustomModel = (model: string) => {
        updateModels(autoModels, customModels.filter(m => m !== model));
    };

    const handleInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter') {
            e.preventDefault();
            if (inputValue.trim()) handleAddModel(inputValue);
        }
    };

    const handleAddKey = () => {
        onFormDataChange({
            ...formData,
            keys: [...formData.keys, { enabled: true, channel_key: '', priority: 0 }],
        });
    };

    const handleUpdateKey = (idx: number, patch: Partial<ChannelKeyFormItem>) => {
        const next = formData.keys.map((k, i) => (i === idx ? { ...k, ...patch } : k));
        onFormDataChange({ ...formData, keys: next });
    };

    const handleRemoveKey = (idx: number) => {
        const curr = formData.keys ?? [];
        if (curr.length <= 1) return;
        const next = curr.filter((_, i) => i !== idx);
        onFormDataChange({ ...formData, keys: next });
    };

    const handleAddHeader = () => {
        onFormDataChange({
            ...formData,
            custom_header: [...(formData.custom_header ?? []), { header_key: '', header_value: '' }],
        });
    };

    const handleUpdateHeader = (idx: number, patch: Partial<Channel['custom_header'][number]>) => {
        const next = (formData.custom_header ?? []).map((h, i) => (i === idx ? { ...h, ...patch } : h));
        onFormDataChange({ ...formData, custom_header: next });
    };

    const handleRemoveHeader = (idx: number) => {
        const curr = formData.custom_header ?? [];
        if (curr.length <= 1) return;
        onFormDataChange({ ...formData, custom_header: curr.filter((_, i) => i !== idx) });
    };


    return (
        <form onSubmit={onSubmit} className="flex h-full min-h-0 flex-col">
            <div className={cn(
                'min-h-0 flex-1 overflow-y-auto overscroll-contain',
                isCreateLayout
                    ? 'grid content-start gap-5 px-4 py-5 sm:px-6 md:grid-cols-2 md:gap-x-6 [&_input]:h-11 [&_[data-slot=select-trigger]]:h-11 [&_[data-slot=select-trigger]]:min-w-0'
                    : 'space-y-4 pb-2',
            )}>
<section className={cn(sectionClassName, isCreateLayout && 'md:col-span-2')}>
                <SectionHeader icon={Orbit} title={t('basicInfo')} />
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div className={fieldGroupClassName}>
                        <label htmlFor={`${idPrefix}-name`} className={labelClassName}>
                        {t('name')}
                        </label>
                        <Input
                            className="rounded-lg"
                            id={`${idPrefix}-name`}
                            type="text"
                            value={formData.name}
                            onChange={(event) => onFormDataChange({ ...formData, name: event.target.value })}
                            required
                        />
                    </div>



                    <div className={fieldGroupClassName}>
                        <label htmlFor={`${idPrefix}-group`} className={labelClassName}>
                            {t('group')}
                        </label>
                        <Select
                            value={String(formData.group_id || 0)}
                            onValueChange={(value) => onFormDataChange({ ...formData, group_id: Number(value) })}
                        >
                            <SelectTrigger id={`${idPrefix}-group`} className="w-full rounded-lg border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                <SelectValue placeholder={t('groupLoading')} />
                            </SelectTrigger>
                            <SelectContent className="rounded-lg">
                                {channelGroups.length > 0 ? (
                                    channelGroups.map((group) => (
                                        <SelectItem key={group.id} className="rounded-xl" value={String(group.id)}>
                                            {group.name}
                                        </SelectItem>
                                    ))
                                ) : (
                                    <SelectItem className="rounded-xl" value="0">
                                        {t('groupLoading')}
                                    </SelectItem>
                                )}
                            </SelectContent>
                        </Select>
                    </div>
                    <label className="flex items-center gap-2 cursor-pointer sm:col-span-2">
                        <Switch
                            checked={formData.enabled}
                            onCheckedChange={(checked) => onFormDataChange({ ...formData, enabled: checked })}
                        />
                        <span className="text-sm font-medium text-card-foreground">{t('enabled')}</span>
                    </label>
                </div>
            </section>

            <section className={cn(sectionClassName, isCreateLayout && 'md:col-span-2')}>
                <ConnectionEditor value={formData.connection_config} onChange={(connection_config) => onFormDataChange({ ...formData, connection_config, ...(connection_config.catalog.format === 'manual' ? { auto_sync: false, auto_sync_key_models: false } : {}) })} channelId={channelId} idPrefix={idPrefix} />
            </section>

            <section className={cn(sectionClassName, isCreateLayout && 'md:col-span-2')}>
                <SectionHeader icon={KeyRound} title={t('apiKeyConfig')} />
                <div className="flex items-center justify-end gap-2">
                    <Badge variant="secondary" className="rounded-full">
                        {formData.keys.length}
                    </Badge>
                    <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={handleTestChannel}
                        disabled={testChannel.isPending || formData.skip_model_test || !hasConnectionAddress(formData.connection_config, formData.base_urls) || !formData.keys?.some((k) => k.channel_key.trim())}
                        className="h-10 rounded-lg px-3 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                    >
                        {testChannel.isPending ? (
                            <RefreshCw className="h-3 w-3 mr-1 animate-spin" />
                        ) : (
                            <FlaskConical className="h-3 w-3 mr-1" />
                        )}
                        {t('test.button')}
                    </Button>
                    <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={handleAddKey}
                        className="h-10 rounded-lg px-3 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                    >
                        <Plus className="h-3 w-3 mr-1" />
                        {t('add')}
                    </Button>
                </div>
                <div className="space-y-2">
                    {(formData.keys ?? []).map((k, idx) => (
                        <div key={k.id ?? `new-${idx}`} className="rounded-lg border border-border/25 bg-card p-2 space-y-2">
                        <div className={cn(
                            "grid grid-cols-[minmax(0,1fr)_auto] gap-2 lg:items-center",
                            showPriorityInput ? "lg:grid-cols-[minmax(0,1fr)_6rem_8rem_6.5rem_2.75rem]" : "lg:grid-cols-[minmax(0,1fr)_8rem_6.5rem_2.75rem]"
                        )}>
                            <Input
                                type="text"
                                value={k.channel_key}
                                onChange={(e) => handleUpdateKey(idx, { channel_key: e.target.value })}
                                placeholder={t('apiKey')}
                                aria-label={`${t('apiKey')} ${idx + 1}`}
                                required={idx === 0}
                                className="col-span-2 rounded-lg lg:col-span-1"
                            />
                            {showPriorityInput && (
                                <Hint text={t('priorityHint')} side="top">
                                    <Input
                                        type="number"
                                        value={k.priority ?? 0}
                                        onChange={(e) => handleUpdateKey(idx, { priority: Number(e.target.value || 0) })}
                                        placeholder={t('priority')}
                                        className="rounded-lg"
                                    />
                                </Hint>
                            )}
                            <Input
                                type="text"
                                value={k.remark ?? ''}
                                onChange={(e) => handleUpdateKey(idx, { remark: e.target.value })}
                                placeholder={t('remark')}
                                className="col-span-2 min-w-0 rounded-lg lg:col-span-1"
                            />
                            <label className="flex items-center gap-2 rounded-lg border border-border/20 bg-card px-3 py-2 text-sm text-card-foreground">
                                <Switch
                                    checked={k.enabled}
                                    onCheckedChange={(checked) => handleUpdateKey(idx, { enabled: checked })}
                                />
                                <span>{t('enabled')}</span>
                            </label>
                            <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onClick={() => handleRemoveKey(idx)}
                                disabled={(formData.keys ?? []).length <= 1}
                                className="size-11 shrink-0 rounded-lg p-0 text-muted-foreground hover:bg-muted hover:text-destructive disabled:opacity-40"
                                title={t('remove')}
                            >
                                <X className="h-4 w-4" />
                            </Button>
                        </div>
                        <Hint text={t('supportedModelsHint')} side="top">
                            <Input
                                type="text"
                                value={k.supported_models ?? ''}
                                onChange={(e) => handleUpdateKey(idx, { supported_models: e.target.value })}
                                placeholder={t('supportedModels')}
                                className="rounded-lg text-xs text-muted-foreground"
                            />
                        </Hint>
                        </div>
                    ))}
                </div>

                {testSummary && (
                    <div className="space-y-2 rounded-lg border border-border/25 bg-card p-3">
                        <div className="flex items-center justify-between gap-2">
                            <div className="flex items-center gap-2 text-sm font-medium text-card-foreground">
                                {testSummary.passed ? (
                                    <CheckCircle2 className="h-4 w-4 text-green-500" />
                                ) : (
                                    <AlertTriangle className="h-4 w-4 text-orange-500" />
                                )}
                                <span>
                                    {testSummary.passed ? t('test.success') : t('test.partialSuccess')}
                                    <Hint text={t('test.hint')} />
                                </span>
                            </div>
                            <div className="flex items-center gap-2">
                                {testSummary.results.some((r) => !r.passed) && (
                                    <Button
                                        type="button"
                                        variant="ghost"
                                        size="sm"
                                        onClick={handleRemoveFailedKeys}
                                        className="h-6 px-2 text-xs text-destructive/70 hover:text-destructive hover:bg-transparent"
                                    >
                                        <Trash2 className="h-3 w-3 mr-1" />
                                        {t('test.removeFailedKeys')}
                                    </Button>
                                )}
                                <Badge variant="secondary">{testSummary.results.length} {t('test.results')}</Badge>
                            </div>
                        </div>
                        <div className="space-y-2 max-h-48 overflow-y-auto">
                            {testSummary.results.map((result, idx) => (
                                <div key={`${result.base_url}-${result.key_masked}-${idx}`} className="rounded-lg border border-border/30 bg-card p-2.5 text-xs space-y-1">
                                    <div className="flex flex-wrap items-center justify-between gap-2">
                                        <span className="font-mono truncate">{result.base_url}</span>
                                        <div className="flex items-center gap-1 shrink-0">
                                            <Badge variant="secondary">{result.key_masked || '-'}</Badge>
                                            <Badge variant="secondary">{result.status_code}</Badge>
                                        </div>
                                    </div>
                                    <div className="flex items-center justify-between gap-2 text-muted-foreground">
                                        <span>{result.key_remark || t('test.noRemark')}</span>
                                        <span>{result.latency_ms}ms · {result.passed ? t('test.pass') : t('test.fail')}</span>
                                    </div>
                                    {result.message && <p className="break-all text-muted-foreground">{result.message}</p>}
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </section>

            <section className={cn(sectionClassName, isCreateLayout && 'md:col-span-2')}>
                <SectionHeader icon={Layers3} title={t('modelConfig')} />
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <label className="flex items-center gap-2 cursor-pointer">
                        <Switch
                            checked={formData.auto_sync && formData.connection_config?.catalog.format !== 'manual'}
                            disabled={formData.connection_config?.catalog.format === 'manual'}
                            onCheckedChange={(checked) => onFormDataChange({ ...formData, auto_sync: checked })}
                        />
                        <span className="text-sm text-card-foreground">{t('autoSync')}</span>
                    </label>
                    {/* 仅在自动同步开启时有意义：参照 request_rewrite 子项的 disabled 依赖处理方式。
                        Hint 放在 label 外侧，避免在 label 内嵌可交互控件导致点击图标误触发开关 */}
                    <div className="flex items-center gap-1">
                        <label className={cn('flex items-center gap-2', formData.auto_sync ? 'cursor-pointer' : 'cursor-not-allowed')}>
                            <Switch
                                checked={formData.auto_sync_key_models}
                                disabled={!formData.auto_sync || formData.connection_config?.catalog.format === 'manual'}
                                onCheckedChange={(checked) => onFormDataChange({ ...formData, auto_sync_key_models: checked })}
                            />
                            <span className={cn('text-sm text-card-foreground', !formData.auto_sync && 'text-muted-foreground/60')}>
                                {t('autoSyncKeyModels')}
                            </span>
                        </label>
                        <Hint text={t('autoSyncKeyModelsHint')} />
                    </div>
                </div>
                <div className="flex items-center justify-end gap-2">
                    <MorphingDialog onOpen={handleRefreshModels}>
                        <MorphingDialogTrigger
                            ariaLabel={t('modelRefresh')}
                            disabled={!canFetchConnectionModels(formData.connection_config, formData.base_urls) || !effectiveKey || isFetchingModels}
                            className="inline-flex h-10 items-center justify-center gap-2 rounded-lg border border-border px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                        >
                            <RefreshCw className={`size-4 ${isFetchingModels ? 'animate-spin' : ''}`} />
                            {t('modelRefresh')}
                        </MorphingDialogTrigger>
                        <MorphingDialogContainer>
                            <MorphingDialogContent className="h-[calc(100dvh-2rem)] w-[min(100vw-2rem,54rem)] max-w-full rounded-xl border border-border/35 bg-card p-0 md:h-[min(44rem,calc(100dvh-3rem))]">
                                <ModelPickerDialogPanel
                                    models={fetchedModels}
                                    draftSelected={modelPickerDraft}
                                    onDraftChange={setModelPickerDraft}
                                    isLoading={isFetchingModels}
                                    onApply={applyFetchedModelSelection}
                                    perKeyResults={perKeyResults}
                                    perKeyLoading={fetchModelsPerKey.isPending}
                                    onFillPerKeyResult={canFillPerKeyModels ? handleFillPerKeyResult : undefined}
                                    onFillAllPerKeyResults={canFillPerKeyModels ? handleFillAllPerKeyResults : undefined}
                                />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>
                </div>
                <input type="hidden" value={formData.model} required />
                <div className="flex min-w-0 items-center gap-2">
                    <Input
                        ref={inputRef}
                        id={`${idPrefix}-model-custom`}
                        type="text"
                        value={inputValue}
                        onChange={(e) => setInputValue(e.target.value)}
                        onKeyDown={handleInputKeyDown}
                        placeholder={t('modelCustomPlaceholder')}
                        aria-label={t('modelCustomPlaceholder')}
                        className="min-w-0 flex-1 rounded-lg"
                    />
                    <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        onClick={() => handleAddModel(inputValue)}
                        disabled={!inputValue.trim() || customModels.includes(inputValue.trim()) || autoModels.includes(inputValue.trim())}
                        className="size-11 shrink-0 rounded-lg"
                        aria-label={t('modelAdd')}
                        title={t('modelAdd')}
                    >
                        <Plus className="size-4" />
                    </Button>
                </div>
                <div className="space-y-2">
                    <div className="flex items-center justify-between gap-2">
                        <label className="text-xs font-medium">{t('modelSelected')} ({autoModels.length + customModels.length})</label>
                        {(autoModels.length + customModels.length) > 0 && (
                            <Button type="button" variant="ghost" size="sm" onClick={() => updateModels([], [])} className="h-9 px-2 text-xs">
                                {t('modelClearAll')}
                            </Button>
                        )}
                    </div>
                    <div className="max-h-40 min-h-12 overflow-y-auto rounded-lg border border-border p-2.5">
                        {(autoModels.length + customModels.length) > 0 ? (
                            <div className="flex flex-wrap gap-2">
                                {[...autoModels, ...customModels].map((model) => (
                                    <Badge key={model} variant="secondary" className="max-w-full gap-1">
                                        <span className="min-w-0 break-all whitespace-normal">{model}</span>
                                        <button
                                            type="button"
                                            onClick={() => customModels.includes(model) ? handleRemoveCustomModel(model) : handleRemoveAutoModel(model)}
                                            className="grid size-7 shrink-0 place-items-center rounded-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                                            aria-label={`${t('remove')} ${model}`}
                                            title={t('remove')}
                                        ><X className="size-3.5" /></button>
                                    </Badge>
                                ))}
                            </div>
                        ) : <div className="py-2 text-center text-xs text-muted-foreground">{t('modelNoSelected')}</div>}
                    </div>
                </div>
            </section>

            <details className={cn('group w-full min-w-0 rounded-lg border border-border/35 bg-card/70', isCreateLayout && 'md:col-span-2')}>
                <summary className="flex min-h-14 cursor-pointer list-none content-center items-center rounded-lg px-4 py-4 text-sm font-medium text-card-foreground transition-colors hover:bg-card focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                    <span className="flex flex-1 items-center justify-between gap-2">
                        <span className="flex items-center gap-2">
                            <span className="h-2 w-2 rounded-full bg-primary/70" />
                            {t('advanced')}
                        </span>
                        <ChevronDown className="size-4 shrink-0 text-muted-foreground transition-transform duration-200 group-open:rotate-180" aria-hidden="true" />
                    </span>
                </summary>
                <div className="px-4 pb-4">
                        <div className="space-y-4">
                        <div className="grid min-w-0 items-start gap-4 md:grid-cols-2">
                            <ProxySelector
                                layout="stacked"
                                value={{ proxy_mode: formData.proxy_mode, proxy_config_id: formData.proxy_config_id }}
                                onChange={(next) => onFormDataChange({
                                    ...formData,
                                    proxy_mode: next.proxy_mode as ChannelProxyMode,
                                    proxy_config_id: next.proxy_config_id ?? null,
                                })}
                            />
                            <div className={fieldGroupClassName}>
                                <label htmlFor={`${idPrefix}-key-strategy`} className={labelClassName}>{t('keySelectionStrategy')}</label>
                                <Select
                                    value={formData.key_selection_strategy || '__inherit__'}
                                    onValueChange={(value) => onFormDataChange({ ...formData, key_selection_strategy: value === '__inherit__' ? '' : value })}
                                >
                                    <SelectTrigger id={`${idPrefix}-key-strategy`} className="h-11 w-full rounded-lg">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className="rounded-lg">
                                        <SelectItem className="rounded-xl" value="__inherit__">{t('keySelectionStrategyInherit')}</SelectItem>
                                        <SelectItem className="rounded-xl" value="cost">{t('keySelectionStrategyCost')}</SelectItem>
                                        <SelectItem className="rounded-xl" value="availability">{t('keySelectionStrategyAvailability')}</SelectItem>
                                        <SelectItem className="rounded-xl" value="speed">{t('keySelectionStrategySpeed')}</SelectItem>
                                        <SelectItem className="rounded-xl" value="priority">{t('keySelectionStrategyPriority')}</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>
                        </div>
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            <div className={fieldGroupClassName}>
                                <label htmlFor={`${idPrefix}-auto-group`} className={labelClassName}>
                                    {t('autoGroup')}
                                </label>
                                <Select
                                    value={String(formData.auto_group)}
                                    onValueChange={(value) => onFormDataChange({ ...formData, auto_group: Number(value) as AutoGroupType })}
                                >
                                    <SelectTrigger id={`${idPrefix}-auto-group`} className="w-full rounded-lg border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className="rounded-lg">
                                        <SelectItem className="rounded-xl" value={String(AutoGroupType.None)}>{t('autoGroupNone')}</SelectItem>
                                        <SelectItem className="rounded-xl" value={String(AutoGroupType.Fuzzy)}>{t('autoGroupFuzzy')}</SelectItem>
                                        <SelectItem className="rounded-xl" value={String(AutoGroupType.Exact)}>{t('autoGroupExact')}</SelectItem>
                                        <SelectItem className="rounded-xl" value={String(AutoGroupType.Regex)}>{t('autoGroupRegex')}</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>

                            <div className={fieldGroupClassName}>
                                <label htmlFor={`${idPrefix}-channel-proxy`} className={labelClassName}>
                                    {t('channelProxy')}
                                </label>
                                <Input
                                    id={`${idPrefix}-channel-proxy`}
                                    type="text"
                                    value={formData.channel_proxy}
                                    onChange={(e) => onFormDataChange({ ...formData, channel_proxy: e.target.value })}
                                    placeholder={t('channelProxyPlaceholder')}
                                    className="rounded-lg"
                                />
                            </div>
                        </div>

                        <div className={fieldGroupClassName}>
                            <div className="flex items-center justify-between">
                                <label className={labelClassName}>
                                    {t('customHeader')} {formData.custom_header.length > 0 ? `(${formData.custom_header.length})` : ''}
                                </label>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="sm"
                                    onClick={handleAddHeader}
                                    className="h-10 rounded-lg px-3 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                                >
                                    <Plus className="h-3 w-3 mr-1" />
                                    {t('customHeaderAdd')}
                                </Button>
                            </div>
                            <div className="space-y-2">
                                {(formData.custom_header ?? []).map((h, idx) => (
                                    <div key={`hdr-${idx}`} className="grid gap-2 rounded-lg border border-border/25 bg-card p-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] lg:items-center">
                                        <Input
                                            type="text"
                                            value={h.header_key}
                                            onChange={(e) => handleUpdateHeader(idx, { header_key: e.target.value })}
                                            placeholder={t('customHeaderKey')}
                                            className="rounded-lg"
                                        />
                                        <Input
                                            type="text"
                                            value={h.header_value}
                                            onChange={(e) => handleUpdateHeader(idx, { header_value: e.target.value })}
                                            placeholder={t('customHeaderValue')}
                                            className="rounded-lg"
                                        />
                                        <Button
                                            type="button"
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => handleRemoveHeader(idx)}
                                            disabled={(formData.custom_header ?? []).length <= 1}
                                            className="size-11 shrink-0 rounded-lg p-0 text-muted-foreground hover:bg-muted hover:text-destructive disabled:opacity-40"
                                            title={t('remove')}
                                        >
                                            <X className="h-4 w-4" />
                                        </Button>
                                    </div>
                                ))}
                            </div>
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-match-regex`} className={labelClassName}>
                                {t('matchRegex')}
                            </label>
                            <Input
                                id={`${idPrefix}-match-regex`}
                                type="text"
                                value={formData.match_regex}
                                onChange={(e) => onFormDataChange({ ...formData, match_regex: e.target.value })}
                                placeholder={t('matchRegexPlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-max-concurrency`} className={labelClassName}>
                                {t('channelCapacityConcurrency')}
                                <Hint text={t('channelCapacityConcurrencyHint')} />
                            </label>
                            <Input
                                id={`${idPrefix}-max-concurrency`}
                                type="number"
                                min={0}
                                value={formData.max_concurrency}
                                onChange={(e) => onFormDataChange({ ...formData, max_concurrency: Number(e.target.value || 0) })}
                                placeholder={t('channelCapacityConcurrencyPlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-rpm-limit`} className={labelClassName}>
                                {t('channelCapacityRpm')}
                                <Hint text={t('channelCapacityRpmHint')} />
                            </label>
                            <Input
                                id={`${idPrefix}-rpm-limit`}
                                type="number"
                                min={0}
                                value={formData.rpm_limit}
                                onChange={(e) => onFormDataChange({ ...formData, rpm_limit: Number(e.target.value || 0) })}
                                placeholder={t('channelCapacityRpmPlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-retryable-status-codes`} className={labelClassName}>
                                {t('retryableStatusCodes')}
                                <Hint text={t('retryableStatusCodesHint')} />
                            </label>
                            <Input
                                id={`${idPrefix}-retryable-status-codes`}
                                type="text"
                                value={formData.retryable_status_codes}
                                onChange={(e) => onFormDataChange({ ...formData, retryable_status_codes: e.target.value })}
                                placeholder={t('retryableStatusCodesPlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-retryable-keywords`} className={labelClassName}>
                                {t('retryableKeywords')}
                                <Hint text={t('retryableKeywordsHint')} />
                            </label>
                            <Input
                                id={`${idPrefix}-retryable-keywords`}
                                type="text"
                                value={formData.retryable_keywords}
                                onChange={(e) => onFormDataChange({ ...formData, retryable_keywords: e.target.value })}
                                placeholder={t('retryableKeywordsPlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-non-retryable-status-codes`} className={labelClassName}>
                                {t('nonRetryableStatusCodes')}
                                <Hint text={t('nonRetryableStatusCodesHint')} />
                            </label>
                            <Input
                                id={`${idPrefix}-non-retryable-status-codes`}
                                type="text"
                                value={formData.non_retryable_status_codes}
                                onChange={(e) => onFormDataChange({ ...formData, non_retryable_status_codes: e.target.value })}
                                placeholder={t('nonRetryableStatusCodesPlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-error-message-template`} className={labelClassName}>
                                {t('errorMessageTemplate')}
                                <Hint text={t('errorMessageTemplateHint')} />
                            </label>
                            <Input
                                id={`${idPrefix}-error-message-template`}
                                type="text"
                                value={formData.error_message_template}
                                onChange={(e) => onFormDataChange({ ...formData, error_message_template: e.target.value })}
                                placeholder={t('errorMessageTemplatePlaceholder')}
                                className="rounded-lg"
                            />
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-param-override`} className={labelClassName}>
                                {t('paramOverride')}
                            </label>
                            <textarea
                                id={`${idPrefix}-param-override`}
                                value={formData.param_override}
                                onChange={(e) => onFormDataChange({ ...formData, param_override: e.target.value })}
                                placeholder={t('paramOverridePlaceholder')}
                                className="min-h-28 w-full rounded-lg border border-border/35 bg-card px-3 py-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            />
                        </div>



                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-first-token-timeout`} className={labelClassName}>
                                {t('channelTimeouts')}
                                <Hint text={t('channelTimeoutsHint')} />
                            </label>
                            <div className="grid gap-2 sm:grid-cols-3">
                                <div className="space-y-1">
                                    <label htmlFor={`${idPrefix}-first-token-timeout`} className="text-xs text-muted-foreground">
                                        {t('channelFirstTokenTimeout')}
                                    </label>
                                    <Input
                                        id={`${idPrefix}-first-token-timeout`}
                                        type="number"
                                        min={-1}
                                        value={formData.first_token_time_out}
                                        onChange={(e) => onFormDataChange({
                                            ...formData,
                                            first_token_time_out: normalizeTimeoutInputValue(e.target.value),
                                        })}
                                        className="rounded-lg"
                                    />
                                </div>
                                <div className="space-y-1">
                                    <label htmlFor={`${idPrefix}-attempt-timeout`} className="text-xs text-muted-foreground">
                                        {t('channelAttemptTimeout')}
                                    </label>
                                    <Input
                                        id={`${idPrefix}-attempt-timeout`}
                                        type="number"
                                        min={-1}
                                        value={formData.attempt_time_out}
                                        onChange={(e) => onFormDataChange({
                                            ...formData,
                                            attempt_time_out: normalizeTimeoutInputValue(e.target.value),
                                        })}
                                        className="rounded-lg"
                                    />
                                </div>
                                <div className="space-y-1">
                                    <label htmlFor={`${idPrefix}-stream-idle-timeout`} className="text-xs text-muted-foreground">
                                        {t('channelStreamIdleTimeout')}
                                    </label>
                                    <Input
                                        id={`${idPrefix}-stream-idle-timeout`}
                                        type="number"
                                        min={-1}
                                        value={formData.stream_idle_timeout}
                                        onChange={(e) => onFormDataChange({
                                            ...formData,
                                            stream_idle_timeout: normalizeTimeoutInputValue(e.target.value),
                                        })}
                                        className="rounded-lg"
                                    />
                                </div>
                            </div>
                            <p className="px-1 text-xs leading-5 text-muted-foreground">{t('channelTimeoutsValueHint')}</p>
                        </div>

                        <div className={fieldGroupClassName}>
                            <label htmlFor={`${idPrefix}-reasoning-buffer-strategy`} className={labelClassName}>
                                {t('channelReasoningBufferStrategy')}
                                <Hint text={t('channelReasoningBufferStrategyHint')} />
                            </label>
                            <Select
                                value={formData.reasoning_buffer_strategy || 'inherit'}
                                onValueChange={(value) => onFormDataChange({
                                    ...formData,
                                    reasoning_buffer_strategy: value === 'inherit'
                                        ? ''
                                        : (value as ChannelReasoningBufferStrategy),
                                })}
                            >
                                <SelectTrigger
                                    id={`${idPrefix}-reasoning-buffer-strategy`}
                                    className="min-w-0 w-full max-w-full rounded-lg"
                                >
                                    <SelectValue placeholder={t('channelReasoningBufferStrategyInherit')} />
                                </SelectTrigger>
                                <SelectContent className="min-w-0" style={{ width: 'var(--radix-select-trigger-width)' }}>
                                    <SelectItem value="inherit">{t('channelReasoningBufferStrategyInherit')}</SelectItem>
                                    <SelectItem value="buffer">{t('channelReasoningBufferStrategyBuffer')}</SelectItem>
                                    <SelectItem value="immediate">{t('channelReasoningBufferStrategyImmediate')}</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>

                        <div className={`${fieldGroupClassName} rounded-lg border border-amber-500/20 bg-amber-500/5 p-3`}>
                            <div className="flex flex-wrap items-start justify-between gap-3">
                                <div>
                                    <p className={labelClassName}>{t('rawSSECapture')}</p>
                                    <p className="text-xs text-muted-foreground">{t('rawSSECaptureHint')}</p>
                                </div>
                                <Badge variant={rawSSECaptureActive ? 'default' : 'secondary'}>
                                    {rawSSEExpiry}
                                </Badge>
                            </div>
                            <div className="flex flex-wrap gap-2">
                                <Button type="button" size="sm" variant={!rawSSECaptureActive ? 'default' : 'outline'} onClick={() => setRawSSECaptureDuration(0)}>
                                    {t('rawSSEOff')}
                                </Button>
                                <Button type="button" size="sm" variant="outline" onClick={() => setRawSSECaptureDuration(15 * 60)}>
                                    {t('rawSSE15Minutes')}
                                </Button>
                                <Button type="button" size="sm" variant="outline" onClick={() => setRawSSECaptureDuration(60 * 60)}>
                                    {t('rawSSE1Hour')}
                                </Button>
                                <Button type="button" size="sm" variant="outline" onClick={() => setRawSSECaptureDuration(6 * 60 * 60)}>
                                    {t('rawSSE6Hours')}
                                </Button>
                            </div>
                        </div>

                        <div className="space-y-4 pt-2 border-t border-border/20">
                            <div className="flex flex-wrap items-center justify-between gap-3">
                                <div>
                                    <p className="text-sm font-medium text-card-foreground">
                                        {t('requestRewrite')}
                                        <Hint text={t('requestRewriteHint')} />
                                    </p>
                                </div>
                                <label className="flex items-center gap-2 cursor-pointer">
                                    <Switch
                                        checked={requestRewriteSupported && formData.request_rewrite.enabled}
                                        onCheckedChange={(checked) => onFormDataChange({
                                            ...formData,
                                            request_rewrite: {
                                                ...formData.request_rewrite,
                                                enabled: checked,
                                            },
                                        })}
                                        disabled={!requestRewriteSupported}
                                    />
                                    <span className="text-sm text-card-foreground">{t('requestRewriteEnabled')}</span>
                                </label>
                            </div>

                            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
                                <div className={fieldGroupClassName}>
                                    <label htmlFor={`${idPrefix}-request-rewrite-profile`} className={labelClassName}>
                                        {t('requestRewriteProfile')}
                                        <Hint text={t('requestRewriteProfileHint')} />
                                    </label>
                                    <Select
                                        value={formData.request_rewrite.profile ?? RequestRewriteProfile.OpenAIChatCompat}
                                        onValueChange={(value) => onFormDataChange({
                                            ...formData,
                                            request_rewrite: {
                                                ...formData.request_rewrite,
                                                profile: value as RequestRewriteProfile,
                                            },
                                        })}
                                        disabled={!requestRewriteSupported || !formData.request_rewrite.enabled}
                                    >
                                        <SelectTrigger id={`${idPrefix}-request-rewrite-profile`} className="w-full rounded-lg border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="rounded-lg">
                                            <SelectItem className="rounded-xl" value={RequestRewriteProfile.Preserve}>保留</SelectItem>
                                            <SelectItem className="rounded-xl" value={RequestRewriteProfile.OpenAIChatCompat}>{t('requestRewriteProfileOpenAIChatCompat')}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>

                                <div className={fieldGroupClassName}>
                                    <label htmlFor={`${idPrefix}-request-rewrite-tool-role`} className={labelClassName}>
                                        {t('requestRewriteToolRoleStrategy')}
                                    </label>
                                    <Select
                                        value={formData.request_rewrite.tool_role_strategy ?? ToolRoleStrategy.Keep}
                                        onValueChange={(value) => onFormDataChange({
                                            ...formData,
                                            request_rewrite: {
                                                ...formData.request_rewrite,
                                                tool_role_strategy: value as ToolRoleStrategy,
                                            },
                                        })}
                                        disabled={!requestRewriteSupported || !formData.request_rewrite.enabled}
                                    >
                                        <SelectTrigger id={`${idPrefix}-request-rewrite-tool-role`} className="w-full rounded-lg border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="rounded-lg">
                                            <SelectItem className="rounded-xl" value={ToolRoleStrategy.Keep}>{t('requestRewriteStrategyKeep')}</SelectItem>
                                            <SelectItem className="rounded-xl" value={ToolRoleStrategy.StringifyToUser}>{t('requestRewriteStrategyStringifyToUser')}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>

                                <div className={fieldGroupClassName}>
                                    <label htmlFor={`${idPrefix}-request-rewrite-header-profile`} className={labelClassName}>
                                        Headers 请求重写
                                    </label>
                                    <Select
                                        value={formData.request_rewrite.header_profile || '__none__'}
                                        onValueChange={(value) => onFormDataChange({
                                            ...formData,
                                            request_rewrite: {
                                                ...formData.request_rewrite,
                                                header_profile: value === '__none__' ? RequestRewriteHeaderProfile.None : value as RequestRewriteHeaderProfile,
                                            },
                                        })}
                                        disabled={!requestRewriteSupported || !formData.request_rewrite.enabled}
                                    >
                                        <SelectTrigger id={`${idPrefix}-request-rewrite-header-profile`} className="w-full rounded-lg border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="rounded-lg">
                                            <SelectItem className="rounded-xl" value="__none__">无</SelectItem>
                                            <SelectItem className="rounded-xl" value={RequestRewriteHeaderProfile.Codex}>Codex</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>

                                <div className={fieldGroupClassName}>
                                    <label htmlFor={`${idPrefix}-request-rewrite-system`} className={labelClassName}>
                                        {t('requestRewriteSystemMessageStrategy')}
                                    </label>
                                    <Select
                                        value={formData.request_rewrite.system_message_strategy ?? SystemMessageStrategy.Keep}
                                        onValueChange={(value) => onFormDataChange({
                                            ...formData,
                                            request_rewrite: {
                                                ...formData.request_rewrite,
                                                system_message_strategy: value as SystemMessageStrategy,
                                            },
                                        })}
                                        disabled={!requestRewriteSupported || !formData.request_rewrite.enabled}
                                    >
                                        <SelectTrigger id={`${idPrefix}-request-rewrite-system`} className="w-full rounded-lg border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="rounded-lg">
                                            <SelectItem className="rounded-xl" value={SystemMessageStrategy.Keep}>{t('requestRewriteStrategyKeep')}</SelectItem>
                                            <SelectItem className="rounded-xl" value={SystemMessageStrategy.Merge}>{t('requestRewriteStrategyMerge')}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                            </div>
                        </div>
                        <div className="grid grid-cols-1 gap-4 border-t border-border/20 pt-4 sm:grid-cols-2">
                            <label className="flex items-center gap-2 cursor-pointer">
                                <Switch
                                    checked={formData.skip_model_test}
                                    onCheckedChange={(checked) => onFormDataChange({ ...formData, skip_model_test: checked })}
                                />
                                <span className="text-sm text-card-foreground">{t('skipModelTest')}</span>
                            </label>
                            <label className="flex items-center gap-2 cursor-pointer">
                                <Switch
                                    checked={formData.disposable}
                                    onCheckedChange={(checked) => onFormDataChange({ ...formData, disposable: checked })}
                                />
                                <span className="text-sm text-card-foreground">{t('disposable')}</span>
                            </label>
                            {formData.disposable && (
                                <div className="col-span-full grid min-w-0 gap-4 sm:grid-cols-2">
                                    <div className="min-w-0 space-y-2">
                                        <label htmlFor={`${idPrefix}-expire-at`} className="text-sm text-card-foreground">{t('expireAt')}</label>
                                        <Input
                                            id={`${idPrefix}-expire-at`}
                                            type="datetime-local"
                                            className="h-11 w-full min-w-0 rounded-lg"
                                            value={formData.expire_at || ''}
                                            onChange={(e) => onFormDataChange({ ...formData, expire_at: e.target.value })}
                                        />
                                    </div>
                                </div>
                            )}
                        </div>
                        </div>
                </div>
            </details>
            </div>

            <div className={cn(
                'flex shrink-0 gap-3',
                isCreateLayout
                    ? 'justify-end border-t border-border bg-card px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6'
                    : `flex-col pt-4 ${onCancel ? 'sm:flex-row' : ''}`,
            )}>
                {onCancel && cancelText && (
                    <Button
                        type="button"
                        variant="secondary"
                        onClick={onCancel}
                        disabled={isPending}
                        className={cn('h-11 rounded-lg', isCreateLayout ? 'flex-1 sm:w-auto sm:flex-none sm:min-w-28' : 'w-full sm:flex-1')}
                    >
                        {cancelText}
                    </Button>
                )}
                <Button
                    type="submit"
                    disabled={isPending}
                    className={cn('h-11 rounded-lg', isCreateLayout ? 'flex-1 sm:w-auto sm:flex-none sm:min-w-28' : 'w-full sm:flex-1')}
                >
                    {isPending ? pendingText : submitText}
                </Button>
            </div>
        </form>
    );
}
