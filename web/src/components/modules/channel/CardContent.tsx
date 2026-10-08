import { useMemo, useState } from 'react';
import {
    Trash2,
    CheckCircle2,
    XCircle,
    FileText,
    DollarSign,
    Clock,
    Activity,
    TrendingUp,
    Globe,
    Key,
    FlaskConical,
} from 'lucide-react';
import {
    useUpdateChannel,
    useDeleteChannel,
    type Channel,
    type UpdateChannelRequest,
    type TestChannelSummary,
} from '@/api/endpoints/channel';
import { useSettingList, SettingKey } from '@/api/endpoints/setting';
import {
    MorphingDialogTitle,
    MorphingDialogDescription,
    MorphingDialogClose,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Tabs, TabsContents, TabsContent } from '@/components/animate-ui/components/animate/tabs';
import { type StatsMetricsFormatted } from '@/api/endpoints/stats';
import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import {
    ChannelForm,
    deriveChannelProxyMode,
    getEffectiveRequestRewriteFormData,
    normalizeRequestRewriteFormData,
    type ChannelFormData,
} from './Form';
import { formatMoney } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { CCSwitchProviderLink } from './CCSwitchProviderLink';
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { TestDialog } from './TestDialog';
import { toast } from '@/components/common/Toast';

export function CardContent({ channel, stats }: { channel: Channel; stats: StatsMetricsFormatted }) {
    const { setIsOpen } = useMorphingDialog();
    const updateChannel = useUpdateChannel();
    const deleteChannel = useDeleteChannel();
    const { data: settings } = useSettingList();
    const [isEditing, setIsEditing] = useState(false);
    const [isConfirmingDelete, setIsConfirmingDelete] = useState(false);
    // 逐 Key 检查全部不可用时的结果；由测试弹窗回调写入，用于删除确认对话框的总数说明。
    const [checkResult, setCheckResult] = useState<TestChannelSummary | null>(null);
    const [showUnavailableDelete, setShowUnavailableDelete] = useState(false);
    // 统一的测试弹窗（手动触发，无后台定时）。
    const [isTestDialogOpen, setIsTestDialogOpen] = useState(false);

    // 可测试的模型列表：来自渠道自动同步的 model 与手动添加的 custom_model，去重。
    const availableModels = useMemo(() => {
        const splitModels = (models: string) =>
            models.split(',').map((m) => m.trim()).filter(Boolean);
        return Array.from(new Set([
            ...splitModels(channel.model),
            ...splitModels(channel.custom_model),
        ]));
    }, [channel.model, channel.custom_model]);

    const [formData, setFormData] = useState<ChannelFormData>({
        connection_config: channel.connection_config,
        name: channel.name,
        group_id: channel.group_id,
        type: channel.type,
        enabled: channel.enabled,
        base_urls: channel.base_urls?.length ? channel.base_urls : [{ url: '', delay: 0, suffix_mode: 'auto' }],
        custom_header: channel.custom_header ?? [],
        channel_proxy: channel.channel_proxy ?? '',
        param_override: channel.param_override ?? '',
        outbound_format_override: channel.outbound_format_override ?? '',
        upstream_protocols: channel.upstream_protocols ?? [],
        first_token_time_out: channel.first_token_time_out ?? 0,
        attempt_time_out: channel.attempt_time_out ?? 0,
        stream_idle_timeout: channel.stream_idle_timeout ?? 0,
        reasoning_buffer_strategy: channel.reasoning_buffer_strategy ?? '',
        request_rewrite: normalizeRequestRewriteFormData(channel.request_rewrite),
        relay_log_raw_sse_until: channel.relay_log_raw_sse_until ?? 0,
        keys: channel.keys.length > 0
            ? channel.keys.map((k) => ({
                id: k.id,
                enabled: k.enabled,
                channel_key: k.channel_key,
                status_code: k.status_code,
                last_use_time_stamp: k.last_use_time_stamp,
                total_cost: k.total_cost,
                priority: k.priority ?? 0,
                remark: k.remark,
                supported_models: k.supported_models ?? '',
            }))
            : [{ enabled: true, channel_key: '', priority: 0, remark: '' }],
        model: channel.model,
        custom_model: channel.custom_model,
        proxy_mode: deriveChannelProxyMode(channel),
        proxy_config_id: channel.proxy_config_id ?? null,
        auto_sync: channel.auto_sync,
        auto_sync_key_models: channel.auto_sync_key_models ?? false,
        auto_group: channel.auto_group,
        skip_model_test: channel.skip_model_test,
        disposable: channel.disposable ?? false,
        expire_at: channel.expire_at ? channel.expire_at.slice(0, 16) : '',
        key_selection_strategy: channel.key_selection_strategy,
        match_regex: channel.match_regex ?? '',
        max_concurrency: channel.max_concurrency ?? 0,
        rpm_limit: channel.rpm_limit ?? 0,
        retryable_status_codes: channel.retryable_status_codes ?? '',
        retryable_keywords: channel.retryable_keywords ?? '',
        non_retryable_status_codes: channel.non_retryable_status_codes ?? '',
        error_message_template: channel.error_message_template ?? '',
    });
    const t = useTranslations('channel.detail');
    const tProxy = useTranslations('proxyPool');

    const publicApiBaseUrl = settings?.find((item) => item.key === SettingKey.PublicAPIBaseURL)?.value?.trim() ?? '';

    const currentView = isEditing ? 'editing' : 'viewing';

    const baseUrlsEqual = (a: Channel['base_urls'] | undefined, b: Channel['base_urls'] | undefined) =>
        JSON.stringify(a ?? []) === JSON.stringify(b ?? []);
    const headersEqual = (a: Channel['custom_header'] | undefined, b: Channel['custom_header'] | undefined) =>
        JSON.stringify(a ?? []) === JSON.stringify(b ?? []);
    const requestRewriteEqual = (a: ChannelFormData['request_rewrite'], b?: Channel['request_rewrite']) =>
        JSON.stringify(a) === JSON.stringify(normalizeRequestRewriteFormData(b));

    const handleUpdate = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        // pool 模式必须选择代理配置（或在渠道代理框保留自定义地址），与后端校验一致
        if (formData.proxy_mode === 'pool' && !formData.proxy_config_id && !formData.channel_proxy.trim()) {
            toast.error(tProxy('selectRequired'));
            return;
        }
        const req: UpdateChannelRequest = { id: channel.id };
        if (formData.connection_config && JSON.stringify(formData.connection_config) !== JSON.stringify(channel.connection_config)) {
            req.connection_config = formData.connection_config;
        }
        const effectiveRequestRewrite = getEffectiveRequestRewriteFormData(formData.type, formData.request_rewrite, formData.connection_config);

        // only send changed fields to avoid accidental clears
        if (formData.name !== channel.name) req.name = formData.name;
        if (formData.group_id !== channel.group_id) req.group_id = formData.group_id;
        if (!formData.connection_config && formData.type !== channel.type) req.type = formData.type;
        if (formData.enabled !== channel.enabled) req.enabled = formData.enabled;
        if (!formData.connection_config && !baseUrlsEqual(formData.base_urls, channel.base_urls)) {
            req.base_urls = (formData.base_urls ?? []).filter((u) => u.url.trim()).map((u) => ({
                url: u.url.trim(),
                delay: Number(u.delay || 0),
                suffix_mode: u.suffix_mode && u.suffix_mode !== 'auto' ? u.suffix_mode : undefined,
                // 协议绑定必须原样带过去，否则多协议渠道会退回按延迟挑地址。
                protocol: u.protocol || undefined,
            }));
        }
        if (formData.model !== channel.model) req.model = formData.model;
        if (formData.custom_model !== channel.custom_model) req.custom_model = formData.custom_model;
        const curProxyMode = deriveChannelProxyMode(channel);
        const nextProxyConfigId = formData.proxy_mode === 'pool' ? formData.proxy_config_id : null;
        if (formData.proxy_mode !== curProxyMode || nextProxyConfigId !== (channel.proxy_config_id ?? null)) {
            // 显式发送 proxy_mode + proxy_config_id，后端不再走 legacy 推导（issue #195）
            req.proxy_mode = formData.proxy_mode;
            req.proxy_config_id = nextProxyConfigId;
        }
        if (formData.auto_sync !== channel.auto_sync) req.auto_sync = formData.auto_sync;
        if (formData.auto_sync_key_models !== (channel.auto_sync_key_models ?? false)) {
            req.auto_sync_key_models = formData.auto_sync_key_models;
        }
        if (formData.skip_model_test !== channel.skip_model_test) req.skip_model_test = formData.skip_model_test;
        if (formData.disposable !== (channel.disposable ?? false)) req.disposable = formData.disposable;
        const curExpireAt = channel.expire_at ? channel.expire_at.slice(0, 16) : '';
        if (formData.expire_at !== curExpireAt) {
            // datetime-local 返回无时区的 "YYYY-MM-DDTHH:mm"，浏览器按本地时区解释。
            // 转 ISO 字符串（带 Z 时区）发给后端，避免 Go 解析无时区字符串为 UTC 导致时区偏移。
            req.expire_at = formData.expire_at ? new Date(formData.expire_at).toISOString() : null;
        }
        if (formData.key_selection_strategy !== channel.key_selection_strategy) req.key_selection_strategy = formData.key_selection_strategy;
        if (formData.auto_group !== channel.auto_group) req.auto_group = formData.auto_group;

        if (!headersEqual(formData.custom_header, channel.custom_header)) {
            req.custom_header = (formData.custom_header ?? [])
                .map((h) => ({ header_key: h.header_key.trim(), header_value: h.header_value }))
                .filter((h) => h.header_key && h.header_value !== '');
        }

        const nextChannelProxy = formData.channel_proxy.trim();
        const curChannelProxy = channel.channel_proxy ?? '';
        if (nextChannelProxy !== curChannelProxy) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.channel_proxy = nextChannelProxy;
        }

        const nextParamOverride = formData.param_override.trim();
        const curParamOverride = channel.param_override ?? '';
        if (nextParamOverride !== curParamOverride) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.param_override = nextParamOverride;
        }

        const nextOutboundFormatOverride = formData.outbound_format_override.trim();
        const curOutboundFormatOverride = channel.outbound_format_override ?? '';
        if (!formData.connection_config && nextOutboundFormatOverride !== curOutboundFormatOverride) {
            // Empty string means "follow group outbound_format" (patch semantics).
            req.outbound_format_override = nextOutboundFormatOverride;
        }

        // 协议声明按顺序比较：顺序本身就是优先级，重排必须触发保存。
        const nextUpstreamProtocols = formData.upstream_protocols ?? [];
        const curUpstreamProtocols = channel.upstream_protocols ?? [];
        if (!formData.connection_config && nextUpstreamProtocols.join(',') !== curUpstreamProtocols.join(',')) {
            req.upstream_protocols = nextUpstreamProtocols;
        }

        // 超时三项只在真正改动时才写，避免每次保存都覆盖渠道级设置。
        const nextFirstTokenTimeout = formData.first_token_time_out ?? 0;
        if (nextFirstTokenTimeout !== (channel.first_token_time_out ?? 0)) {
            req.first_token_time_out = nextFirstTokenTimeout;
        }
        const nextAttemptTimeout = formData.attempt_time_out ?? 0;
        if (nextAttemptTimeout !== (channel.attempt_time_out ?? 0)) {
            req.attempt_time_out = nextAttemptTimeout;
        }
        const nextStreamIdleTimeout = formData.stream_idle_timeout ?? 0;
        if (nextStreamIdleTimeout !== (channel.stream_idle_timeout ?? 0)) {
            req.stream_idle_timeout = nextStreamIdleTimeout;
        }
        const nextReasoningBufferStrategy = formData.reasoning_buffer_strategy ?? '';
        if (nextReasoningBufferStrategy !== (channel.reasoning_buffer_strategy ?? '')) {
            req.reasoning_buffer_strategy = nextReasoningBufferStrategy;
        }

        if (!requestRewriteEqual(effectiveRequestRewrite, channel.request_rewrite)) {
            req.request_rewrite = effectiveRequestRewrite;
        }

        if (formData.relay_log_raw_sse_until !== (channel.relay_log_raw_sse_until ?? 0)) {
            req.relay_log_raw_sse_until = formData.relay_log_raw_sse_until;
        }

        const nextMatchRegex = formData.match_regex.trim();
        const curMatchRegex = channel.match_regex ?? '';
        if (nextMatchRegex !== curMatchRegex) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.match_regex = nextMatchRegex;
        }

        if (formData.max_concurrency !== (channel.max_concurrency ?? 0)) {
            req.max_concurrency = formData.max_concurrency;
        }
        if (formData.rpm_limit !== (channel.rpm_limit ?? 0)) {
            req.rpm_limit = formData.rpm_limit;
        }
        const nextRetryableCodes = formData.retryable_status_codes.trim();
        if (nextRetryableCodes !== (channel.retryable_status_codes ?? '')) {
            req.retryable_status_codes = nextRetryableCodes;
        }
        const nextRetryableKeywords = formData.retryable_keywords.trim();
        if (nextRetryableKeywords !== (channel.retryable_keywords ?? '')) {
            req.retryable_keywords = nextRetryableKeywords;
        }
        const nextNonRetryableCodes = formData.non_retryable_status_codes.trim();
        if (nextNonRetryableCodes !== (channel.non_retryable_status_codes ?? '')) {
            req.non_retryable_status_codes = nextNonRetryableCodes;
        }
        const nextErrorMessageTemplate = formData.error_message_template.trim();
        if (nextErrorMessageTemplate !== (channel.error_message_template ?? '')) {
            req.error_message_template = nextErrorMessageTemplate;
        }

        const originalKeys = channel.keys;
        const originalByID = new Map(originalKeys.map((k) => [k.id, k]));
        const nextKeys = formData.keys ?? [];

        const nextIDs = new Set(nextKeys.filter((k) => typeof k.id === 'number').map((k) => k.id as number));
        const keys_to_delete = originalKeys.filter((k) => !nextIDs.has(k.id)).map((k) => k.id);

        const keys_to_add = nextKeys
            .filter((k) => !k.id)
            .map((k) => ({ enabled: k.enabled, channel_key: k.channel_key.trim(), priority: Number(k.priority ?? 0), remark: k.remark ?? '', supported_models: k.supported_models ?? '' }));

        const keys_to_update = nextKeys
            .filter((k) => typeof k.id === 'number' && originalByID.has(k.id as number))
            .map((k) => {
                const orig = originalByID.get(k.id as number)!;
                const u: { id: number; enabled?: boolean; channel_key?: string; priority?: number; remark?: string; supported_models?: string } = { id: k.id as number };
                const nextPriority = Number(k.priority ?? 0);
                const origPriority = Number(orig.priority ?? 0);
                if (k.enabled !== orig.enabled) u.enabled = k.enabled;
                if (k.channel_key !== orig.channel_key) u.channel_key = k.channel_key;
                if (nextPriority !== origPriority) u.priority = nextPriority;
                if ((k.remark ?? '') !== orig.remark) u.remark = k.remark ?? '';
                if ((k.supported_models ?? '') !== (orig.supported_models ?? '')) u.supported_models = k.supported_models ?? '';
                return Object.keys(u).length > 1 ? u : null;
            })
            .filter((u) => u !== null) as Array<{ id: number; enabled?: boolean; channel_key?: string; priority?: number; remark?: string; supported_models?: string }>;

        if (keys_to_add.length > 0) req.keys_to_add = keys_to_add;
        if (keys_to_update.length > 0) req.keys_to_update = keys_to_update;
        if (keys_to_delete.length > 0) req.keys_to_delete = keys_to_delete;

        updateChannel.mutate(req, {
            onSuccess: () => {
                setIsEditing(false);
                setIsOpen(false);
            }
        });
    };

    const handleDeleteClick = () => {
        if (!isConfirmingDelete) {
            setIsConfirmingDelete(true);
            return;
        }

        setIsOpen(false);
        setTimeout(() => {
            deleteChannel.mutate(channel.id);
        }, 300);
    };

    // 测试弹窗完成逐 Key 检查：全部不可用时关闭弹窗并沿用原来的删除确认流程。
    const handleKeysUnavailable = (summary: TestChannelSummary) => {
        setCheckResult(summary);
        setIsTestDialogOpen(false);
        toast.error(t('actions.checkAllFailed'));
        setShowUnavailableDelete(true);
    };

    const handleConfirmDeleteUnavailable = () => {
        setShowUnavailableDelete(false);
        setIsOpen(false);
        setTimeout(() => {
            deleteChannel.mutate(channel.id);
        }, 300);
    };

    const sectionClassName = 'relative overflow-hidden rounded-lg border border-border/30 bg-card p-4';
    const itemClassName = 'rounded-lg border border-border/25 bg-card p-3';

    return (
        <>
            <MorphingDialogTitle className="shrink-0">
                <header className="relative flex items-center justify-between gap-4 px-1 pb-4 pt-1">
                    <div className="space-y-3">
                        <div className="flex items-center gap-2">
                            <span className="h-2.5 w-10 rounded-full bg-primary/18" />
                            <span className="h-2.5 w-24 rounded-full bg-card" />
                            <span className="h-2.5 w-14 rounded-full bg-card" />
                        </div>
                        <div className="space-y-1">
                            <h2 className="text-2xl font-semibold tracking-tight text-card-foreground">
                                {isEditing ? t('title.edit') : t('title.view')}
                            </h2>
                            <p className="text-sm text-muted-foreground">{channel.name}</p>
                        </div>
                    </div>
                    <MorphingDialogClose
                        className="relative top-0 right-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 }
                        }}
                    />
                </header>
            </MorphingDialogTitle>

            <MorphingDialogDescription disableLayoutAnimation className="flex min-h-0 flex-1 flex-col overflow-hidden px-1">
                <Tabs value={currentView} className="flex min-h-0 flex-1 flex-col">
                    <TabsContents className="flex min-h-0 flex-1 flex-col">
                        <TabsContent value="viewing" className="flex flex-1 min-h-0 flex-col">
                            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain space-y-4 pr-1 sm:space-y-5">
                                <dl className="grid grid-cols-3 gap-2 sm:gap-3">
                                    <div className="rounded-lg border border-chart-1/18 bg-linear-to-br from-chart-1/10 via-background/42 to-chart-1/5 p-3.5 shadow-sm sm:p-4">
                                        <dt className="flex items-center gap-2 mb-2 text-xs font-medium text-muted-foreground">
                                            <Activity className="size-4 text-chart-1" />
                                            {t('metrics.totalRequests')}
                                        </dt>
                                        <dd className="text-xl sm:text-2xl font-bold text-chart-1">
                                            {stats.request_count.formatted.value}
                                            <span className="text-xs font-normal ml-1 text-muted-foreground">{stats.request_count.formatted.unit}</span>
                                        </dd>
                                    </div>

                                    <div className="rounded-lg border border-chart-3/18 bg-linear-to-br from-chart-3/10 via-background/42 to-chart-3/5 p-3.5 shadow-sm sm:p-4">
                                        <dt className="flex items-center gap-2 mb-2 text-xs font-medium text-muted-foreground">
                                            <FileText className="size-4 text-chart-3" />
                                            {t('metrics.totalToken')}
                                        </dt>
                                        <dd className="text-xl sm:text-2xl font-bold text-chart-3">
                                            {stats.total_token.formatted.value}
                                            <span className="text-xs font-normal ml-1 text-muted-foreground">{stats.total_token.formatted.unit}</span>
                                        </dd>
                                    </div>

                                    <div className="rounded-lg border border-chart-5/18 bg-linear-to-br from-chart-5/10 via-background/42 to-chart-5/5 p-3.5 shadow-sm sm:p-4">
                                        <dt className="flex items-center gap-2 mb-2 text-xs font-medium text-muted-foreground">
                                            <DollarSign className="size-4 text-chart-5" />
                                            {t('metrics.totalCost')}
                                        </dt>
                                        <dd className="text-xl sm:text-2xl font-bold text-chart-5">
                                            {stats.total_cost.formatted.value}
                                            <span className="text-xs font-normal ml-1 text-muted-foreground">{stats.total_cost.formatted.unit}</span>
                                        </dd>
                                    </div>
                                </dl>

                                <section className={sectionClassName}>
                                    <h4 className="mb-3 flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                                        <TrendingUp className="size-3.5" />
                                        {t('sections.requests')}
                                    </h4>
                                    <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                                        <div className={itemClassName}>
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <CheckCircle2 className="size-4 text-accent" />
                                                {t('metrics.successRequests')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-accent">
                                                {stats.request_success.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.request_success.formatted.unit}</span>
                                            </dd>
                                        </div>

                                        <div className={itemClassName}>
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <XCircle className="size-4 text-destructive" />
                                                {t('metrics.failedRequests')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-destructive">
                                                {stats.request_failed.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.request_failed.formatted.unit}</span>
                                            </dd>
                                        </div>
                                    </dl>
                                </section>

                                <section className={sectionClassName}>
                                    <h4 className="mb-3 flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                                        <FileText className="size-3.5" />
                                        {t('sections.tokens')}
                                    </h4>
                                    <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                                        <div className={itemClassName}>
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-1" />
                                                {t('metrics.inputToken')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.input_token.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.input_token.formatted.unit}</span>
                                            </dd>
                                        </div>

                                        <div className={itemClassName}>
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-3" />
                                                {t('metrics.outputToken')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.output_token.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.output_token.formatted.unit}</span>
                                            </dd>
                                        </div>
                                    </dl>
                                </section>

                                <section className={sectionClassName}>
                                    <h4 className="mb-3 flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                                        <DollarSign className="size-3.5" />
                                        {t('sections.costs')}
                                    </h4>
                                    <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                                        <div className={itemClassName}>
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-2" />
                                                {t('metrics.inputCost')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.input_cost.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.input_cost.formatted.unit}</span>
                                            </dd>
                                        </div>

                                        <div className={itemClassName}>
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-5" />
                                                {t('metrics.outputCost')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.output_cost.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.output_cost.formatted.unit}</span>
                                            </dd>
                                        </div>
                                    </dl>
                                </section>

                                <section className={sectionClassName}>
                                    <h4 className="mb-3 flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                                        <Globe className="size-3.5" />
                                        {t('sections.baseUrls')}
                                    </h4>
                                    <div className="space-y-2">
                                        {channel.connection_config?.endpoints.map((endpoint) => <div key={endpoint.id} className="min-w-0 rounded-lg border border-border p-3"><span className="text-sm font-medium">{endpoint.protocol}</span><p className="break-all text-sm text-muted-foreground">{endpoint.url}</p></div>)}
                                        {!channel.connection_config && channel.base_urls?.map((url, i) => (
                                            <div key={i} className="flex items-center justify-between gap-3 rounded-lg border border-border/25 bg-card p-3 shadow-sm">
                                                <div className="flex flex-col gap-1 min-w-0">
                                                    <span className="font-mono text-sm truncate select-all">{url.url}</span>
                                                </div>
                                                <Badge
                                                    variant="secondary"
                                                    className={cn(
                                                        "h-5 px-1.5 text-xs",
                                                        url.delay < 300
                                                            ? "bg-green-500/15 text-green-700 dark:text-green-400"
                                                            : url.delay < 1000
                                                                ? "bg-orange-500/15 text-orange-700 dark:text-orange-400"
                                                                : "bg-red-500/15 text-red-700 dark:text-red-400"
                                                    )}
                                                >
                                                    {url.delay}ms
                                                </Badge>
                                            </div>
                                        ))}
                                        {!channel.connection_config && (!channel.base_urls || channel.base_urls.length === 0) && (
                                            <div className="rounded-lg border border-dashed border-border/30 bg-card p-4 text-center text-sm text-muted-foreground">{t('noBaseUrls')}</div>
                                        )}
                                    </div>
                                </section>

                                <section className={sectionClassName}>
                                    <h4 className="mb-3 flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                                        <Key className="size-3.5" />
                                        {t('sections.keys')}
                                    </h4>
                                    <div className="space-y-2">
                                        {channel.keys?.map((key) => (
                                            <div key={key.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-border/25 bg-card p-3 shadow-sm sm:gap-3">
                                                <div className={cn("size-2 shrink-0 rounded-full", key.enabled ? "bg-emerald-500" : "bg-destructive")} />

                                                <span className="font-mono text-sm truncate min-w-0 flex-1">
                                                    {key.channel_key.length > 10
                                                        ? `${key.channel_key.slice(0, 4)}...${key.channel_key.slice(-4)}`
                                                        : key.channel_key}
                                                </span>

                                                {key.remark && (
                                                    <span className="text-xs text-muted-foreground truncate max-w-24" title={key.remark}>
                                                        {key.remark}
                                                    </span>
                                                )}

                                                <div className="flex items-center gap-2 shrink-0">
                                                    {key.last_use_time_stamp > 0 && (
                                                        <span className="text-xs text-muted-foreground whitespace-nowrap hidden sm:inline-block">
                                                            {new Date(key.last_use_time_stamp * 1000).toLocaleString()}
                                                        </span>
                                                    )}

                                                    {key.status_code !== 0 && (
                                                        <Badge
                                                            variant="secondary"
                                                            className={cn(
                                                                "h-5 px-1.5 text-[10px]",
                                                                key.status_code === 200
                                                                    ? "bg-green-500/15 text-green-700 dark:text-green-400"
                                                                    : key.status_code === 401 ||
                                                                        key.status_code === 403 ||
                                                                        key.status_code === 429 ||
                                                                        key.status_code >= 500
                                                                        ? "bg-red-500/15 text-red-700 dark:text-red-400"
                                                                        : "bg-orange-500/15 text-orange-700 dark:text-orange-400"
                                                            )}
                                                        >
                                                            {key.status_code}
                                                        </Badge>
                                                    )}

                                                    <Badge variant="secondary" className="h-5 px-1.5 text-[10px]">
                                                        {t('priority')}: {key.priority ?? 0}
                                                    </Badge>

                                                    <Badge variant="secondary" className="h-5 px-1.5 text-[10px]">
                                                        {formatMoney(key.total_cost).formatted.value}
                                                        {formatMoney(key.total_cost).formatted.unit}
                                                    </Badge>
                                                </div>
                                            </div>
                                        ))}
                                        {(!channel.keys || channel.keys.length === 0) && (
                                            <div className="rounded-lg border border-dashed border-border/30 bg-card p-4 text-center text-sm text-muted-foreground">{t('noKeys')}</div>
                                        )}
                                    </div>
                                </section>

                                <section className={sectionClassName}>
                                    <h4 className="mb-3 flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                                        <FlaskConical className="size-3.5" />
                                        {t('sections.testModel')}
                                    </h4>
                                    {channel.skip_model_test && (
                                        <div className="mb-3 rounded-lg border border-amber-500/30 bg-amber-500/8 px-3 py-2 text-sm text-amber-700 dark:text-amber-300">
                                            {t('testModel.skipped')}
                                        </div>
                                    )}
                                    {availableModels.length === 0 ? (
                                        <div className="rounded-lg border border-dashed border-border/30 bg-card p-4 text-center text-sm text-muted-foreground">
                                            {t('testModel.noModels')}
                                        </div>
                                    ) : (
                                        <div className="space-y-3">
                                            {/* 三段检查（模型应答 / 协议与能力 / 逐 Key）合并进同一个测试弹窗 */}
                                            <Button
                                                type="button"
                                                onClick={() => setIsTestDialogOpen(true)}
                                                disabled={channel.keys.length === 0 && availableModels.length === 0}
                                                variant="outline"
                                                className="h-10 w-full sm:w-auto"
                                            >
                                                <FlaskConical className="size-4" />
                                                {t('actions.test')}
                                            </Button>
                                            <p className="text-xs leading-5 text-muted-foreground">{t('testModel.entryHint')}</p>
                                        </div>
                                    )}
                                </section>

                                <CCSwitchProviderLink
                                    channel={channel}
                                    publicApiBaseUrl={publicApiBaseUrl}
                                    sectionClassName={sectionClassName}
                                />

                                <dl className={sectionClassName}>
                                    <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                        <Clock className="size-4 text-primary" />
                                        {t('metrics.avgWaitTime')}
                                    </dt>
                                    <dd className="text-2xl font-bold text-primary">
                                        {stats.wait_time.formatted.value}
                                        <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.wait_time.formatted.unit}</span>
                                    </dd>
                                </dl>
                            </div>

                                <div className="grid shrink-0 gap-3 border-t border-border/30 pt-3 sm:grid-cols-2">
                                <Button
                                    onClick={() => (isConfirmingDelete ? setIsConfirmingDelete(false) : setIsEditing(true))}
                                    variant={isConfirmingDelete ? 'secondary' : 'default'}
                                    className="h-12 w-full rounded-lg"
                                >
                                    {isConfirmingDelete ? t('actions.cancel') : t('actions.edit')}
                                </Button>
                                <Button
                                    onClick={handleDeleteClick}
                                    disabled={deleteChannel.isPending}
                                    variant="destructive"
                                    className="h-12 w-full rounded-lg"
                                >
                                    <Trash2 className={`size-4 transition-transform ${isConfirmingDelete ? 'scale-110' : ''}`} />
                                    {deleteChannel.isPending
                                        ? t('actions.deleting')
                                        : isConfirmingDelete
                                            ? t('actions.confirmDelete')
                                            : t('actions.delete')}
                                </Button>
                                </div>
                        </TabsContent>

                        <TabsContent value="editing" className="flex flex-1 min-h-0 flex-col">
                            <ChannelForm
                                channelId={channel.id}
                                formData={formData}
                                onFormDataChange={setFormData}
                                onSubmit={handleUpdate}
                                isPending={updateChannel.isPending}
                                submitText={t('actions.save')}
                                pendingText={t('actions.saving')}
                                onCancel={() => setIsEditing(false)}
                                cancelText={t('actions.cancel')}
                                idPrefix="channel"
                            />
                        </TabsContent>
                    </TabsContents>
                </Tabs>
            </MorphingDialogDescription>

            {/*
              统一测试弹窗。刻意挂在 AlertDialog 之外、用独立的 Dialog：
              它内容较长（三段检查 + 逐行结果），且需要滚动查看，
              与"确认删除"这类短确认弹窗的交互模型不同。
            */}
            <Dialog open={isTestDialogOpen} onOpenChange={setIsTestDialogOpen}>
                <DialogContent className="max-h-[85vh] overflow-y-auto rounded-xl sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>{t('testDialog.title')}</DialogTitle>
                        <DialogDescription>{t('testDialog.description')}</DialogDescription>
                    </DialogHeader>
                    <TestDialog
                        channel={channel}
                        availableModels={availableModels}
                        onClose={() => setIsTestDialogOpen(false)}
                        onKeysUnavailable={handleKeysUnavailable}
                    />
                </DialogContent>
            </Dialog>

            <AlertDialog open={showUnavailableDelete} onOpenChange={setShowUnavailableDelete}>
                <AlertDialogContent className="rounded-xl">
                    <AlertDialogHeader>
                        <AlertDialogTitle>{t('actions.deleteUnavailableTitle')}</AlertDialogTitle>
                        <AlertDialogDescription className="whitespace-pre-line">
                            {t('actions.deleteUnavailableDescription', { total: checkResult?.results.length ?? channel.keys.length })}
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel disabled={deleteChannel.isPending}>
                            {t('actions.cancel')}
                        </AlertDialogCancel>
                        <AlertDialogAction
                            disabled={deleteChannel.isPending}
                            onClick={(event) => {
                                event.preventDefault();
                                handleConfirmDeleteUnavailable();
                            }}
                        >
                            {deleteChannel.isPending ? t('actions.deleting') : t('actions.deleteUnavailable')}
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </>
    );
}
