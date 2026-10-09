import { useState } from 'react';
import {
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Button } from '@/components/ui/button';
import {
    AutoGroupType,
    useCreateChannel,
} from '@/api/endpoints/channel';
import { X } from 'lucide-react';
import { useTranslations } from 'next-intl';
import {
    ChannelForm,
    createDefaultRequestRewriteFormData,
    getEffectiveRequestRewriteFormData,
    type ChannelFormData,
} from './Form';
import { newConnectionConfig } from './connection-config';
import { DEFAULT_CHANNEL_TYPE } from './type-options';
import { toast } from '@/components/common/Toast';

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const createChannel = useCreateChannel();
    const [formData, setFormData] = useState<ChannelFormData>({
        name: '',
        connection_config: newConnectionConfig(),
        group_id: 0,
        type: DEFAULT_CHANNEL_TYPE,
        base_urls: [{ url: '', delay: 0, suffix_mode: 'auto' }],
        custom_header: [],
        channel_proxy: '',
        param_override: '',
        outbound_format_override: '',
        upstream_protocols: [],
        first_token_time_out: 0,
        attempt_time_out: 0,
        stream_idle_timeout: 0,
        reasoning_buffer_strategy: '',
        request_rewrite: createDefaultRequestRewriteFormData(),
        relay_log_raw_sse_until: 0,
        keys: [{ enabled: true, channel_key: '', priority: 0, remark: '' }],
        model: '',
        custom_model: '',
        auto_sync: false,
        auto_sync_key_models: false,
        auto_group: AutoGroupType.None,
        skip_model_test: false,
        disposable: false,
        expire_at: '',
        key_selection_strategy: '',
        enabled: true,
        proxy_mode: 'direct',
        proxy_config_id: null,
        match_regex: '',
        max_concurrency: 0,
        rpm_limit: 0,
        retryable_status_codes: '',
        retryable_keywords: '',
        non_retryable_status_codes: '',
        error_message_template: '',
    });
    const t = useTranslations('channel.create');
    const tForm = useTranslations('channel.form');
    const tProxy = useTranslations('proxyPool');

    const resetFormData = () => {
        setFormData({
            name: '',
            connection_config: newConnectionConfig(),
            group_id: 0,
            type: DEFAULT_CHANNEL_TYPE,
            base_urls: [{ url: '', delay: 0, suffix_mode: 'auto' }],
            custom_header: [],
            channel_proxy: '',
            param_override: '',
            outbound_format_override: '',
            upstream_protocols: [],
            first_token_time_out: 0,
            attempt_time_out: 0,
            stream_idle_timeout: 0,
            reasoning_buffer_strategy: '',
            request_rewrite: createDefaultRequestRewriteFormData(),
            relay_log_raw_sse_until: 0,
            keys: [{ enabled: true, channel_key: '', priority: 0, remark: '' }],
            model: '',
            custom_model: '',
            auto_sync: false,
            auto_sync_key_models: false,
            auto_group: AutoGroupType.None,
            skip_model_test: false,
            disposable: false,
            expire_at: '',
            key_selection_strategy: '',
            enabled: true,
            proxy_mode: 'direct',
            proxy_config_id: null,
            match_regex: '',
            max_concurrency: 0,
            rpm_limit: 0,
            retryable_status_codes: '',
            retryable_keywords: '',
            non_retryable_status_codes: '',
            error_message_template: '',
        });
    };

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        // pool 模式必须选择代理配置（或填写自定义渠道代理地址），与后端校验一致
        if (formData.proxy_mode === 'pool' && !formData.proxy_config_id && !formData.channel_proxy.trim()) {
            toast.error(tProxy('selectRequired'));
            return;
        }
        const normalizedBaseUrls = (formData.base_urls ?? []).filter((u) => u.url.trim()).map((u) => ({
            url: u.url.trim(),
            delay: Number(u.delay || 0),
            suffix_mode: u.suffix_mode && u.suffix_mode !== 'auto' ? u.suffix_mode : undefined,
            // 协议绑定必须原样带过去：丢了它就等于悄悄退回"按延迟挑地址"，
            // 多协议渠道会打错端点。
            protocol: u.protocol || undefined,
        }));
        const normalizedKeys = formData.keys.map((k) => ({
            enabled: k.enabled,
            channel_key: k.channel_key.trim(),
            priority: Number(k.priority ?? 0),
            remark: k.remark ?? '',
            supported_models: k.supported_models ?? '',
        }));
        const normalizedHeaders = (formData.custom_header ?? [])
            .map((h) => ({ header_key: h.header_key.trim(), header_value: h.header_value }))
            .filter((h) => h.header_key && h.header_value !== '');

        const channelProxy = formData.channel_proxy.trim();
        const paramOverride = formData.param_override.trim();
        const outboundFormatOverride = formData.outbound_format_override.trim();
        const requestRewrite = getEffectiveRequestRewriteFormData(formData.type, formData.request_rewrite, formData.connection_config);
        createChannel.mutate(
            {
                name: formData.name,
                connection_config: formData.connection_config,
                group_id: formData.group_id || undefined,
                type: formData.type,
                enabled: formData.enabled,
                base_urls: normalizedBaseUrls,
                keys: normalizedKeys,
                model: formData.model,
                custom_model: formData.custom_model,
                proxy_mode: formData.proxy_mode,
                proxy_config_id: formData.proxy_mode === 'pool' ? formData.proxy_config_id : null,
                proxy: formData.proxy_mode !== 'direct',
                auto_sync: formData.auto_sync,
                auto_sync_key_models: formData.auto_sync_key_models,
                auto_group: formData.auto_group,
                key_selection_strategy: formData.key_selection_strategy,
                skip_model_test: formData.skip_model_test,
                disposable: formData.disposable,
                // datetime-local 返回无时区的 "YYYY-MM-DDTHH:mm"，浏览器按本地时区解释。
                // 转 ISO 字符串（带 Z 时区）发给后端，避免 Go 按解析无时区字符串为 UTC 导致时区偏移。
                expire_at: formData.disposable && formData.expire_at ? new Date(formData.expire_at).toISOString() : undefined,
                custom_header: normalizedHeaders,
                channel_proxy: channelProxy,
                param_override: paramOverride,
                outbound_format_override: outboundFormatOverride,
                upstream_protocols: formData.upstream_protocols,
                first_token_time_out: formData.first_token_time_out,
                attempt_time_out: formData.attempt_time_out,
                stream_idle_timeout: formData.stream_idle_timeout,
                reasoning_buffer_strategy: formData.reasoning_buffer_strategy,
                request_rewrite: requestRewrite.enabled ? requestRewrite : undefined,
                relay_log_raw_sse_until: formData.relay_log_raw_sse_until,
                match_regex: formData.match_regex.trim(),
                max_concurrency: formData.max_concurrency,
                rpm_limit: formData.rpm_limit,
                retryable_status_codes: formData.retryable_status_codes.trim(),
                retryable_keywords: formData.retryable_keywords.trim(),
                non_retryable_status_codes: formData.non_retryable_status_codes.trim(),
                error_message_template: formData.error_message_template.trim(),
            },
            {
                onSuccess: () => {
                    resetFormData();
                    setIsOpen(false);
                }
            });
    };

    return (
        <div className="flex h-full w-full min-h-0 flex-col overflow-hidden bg-card text-card-foreground">
            <MorphingDialogTitle className="shrink-0">
                <header className="flex min-h-16 items-center justify-between gap-3 border-b border-border px-4 py-3 sm:px-6">
                    <h2 className="min-w-0 text-lg font-semibold">{t('dialogTitle')}</h2>
                    <div className="flex shrink-0 items-center gap-2">

                    {createChannel.isPending ? (
                        <Button
                            type="button"
                            variant="outline"
                            size="icon"
                            disabled
                            aria-label={tForm('modelPicker.cancel')}
                            className="h-9 w-9 rounded-md border-border bg-card opacity-80 transition-all duration-150 hover:bg-muted hover:opacity-100"
                        >
                            <X className="size-5" />
                        </Button>
                    ) : (
                        <MorphingDialogClose
                            className="relative inset-auto size-10 shrink-0 p-2 sm:inset-auto sm:size-10 sm:p-2"
                            variants={{
                                initial: { opacity: 0, scale: 0.8 },
                                animate: { opacity: 1, scale: 1 },
                                exit: { opacity: 0, scale: 0.8 }
                            }}
                        />
                    )}
                    </div>
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription disableLayoutAnimation className="flex min-h-0 flex-1 flex-col overflow-hidden">
                <ChannelForm
                            formData={formData}
                            onFormDataChange={setFormData}
                            onSubmit={handleSubmit}
                            isPending={createChannel.isPending}
                            submitText={t('submit')}
                            pendingText={t('submitting')}
                            idPrefix="new-channel"
                            layout="create"
                            onCancel={() => setIsOpen(false)}
                            cancelText={tForm('modelPicker.cancel')}
                        />
            </MorphingDialogDescription>
        </div>
    );
}
