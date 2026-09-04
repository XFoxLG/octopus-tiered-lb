'use client';

import { useState, useEffect } from 'react';
import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Hint } from '@/components/ui/hint';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { ProxySelector } from '@/components/modules/proxy-pool/ProxySelector';
import type { ProxyMode } from '@/api/endpoints/proxy-pool';
import { toast } from '@/components/common/Toast';
import { apiClient } from '@/api/client';
import {
    useCreatePoolAccount,
    useUpdatePoolAccount,
    type PoolAccount,
    type PoolAccountRequest,
} from '@/api/endpoints/pool';
import {
    POOL_PLATFORM_OPTIONS,
    POOL_TYPE_OPTIONS_BY_PLATFORM,
    DEFAULT_BASE_URL_BY_PLATFORM,
    platformSupportsOAuth,
    type PoolPlatform,
    type PoolAccountType,
} from './type-options';
import type { PoolAccountExtra } from '@/api/endpoints/pool';

// 不豆名单（与后端 relay.applyHeaderOverrides 对齐）。静态查表 → Record。
const HEADER_OVERRIDE_BLOCKED: Record<string, true> = {
    authorization: true,
    'x-api-key': true,
    cookie: true,
    host: true,
    'content-length': true,
    'chatgpt-account-id': true,
    'x-claude-code-session-id': true,
    'x-client-request-id': true,
    'x-grok-conv-id': true,
};

// 同上 header override 资格条件（与 sub2api IsHeaderOverrideEligible 对齐）。
function isHeaderOverrideEligible(platform: string, acctType: string): boolean {
    return (
        ((platform === 'anthropic' || platform === 'openai') && acctType === 'apikey') ||
        (platform === 'grok' && (acctType === 'apikey' || acctType === 'oauth'))
    );
}

type AccountFormDialogProps = {
    poolId: number;
    account?: PoolAccount | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
};

const emptyForm: PoolAccountRequest = {
    name: '',
    platform: 'anthropic',
    type: 'apikey',
    models: '',
    credentials: '',
    base_url: '',
    priority: 0,
    concurrency: 0,
    weight: 0,
    load_factor: 0,
    auto_pause_on_expired: false,
    expires_at: 0,
    proxy_config_id: null,
    notes: '',
    extra: '',
};

function parseExtra(raw: string): PoolAccountExtra {
    if (!raw) return {};
    try {
        return JSON.parse(raw) as PoolAccountExtra;
    } catch {
        return {};
    }
}

export function AccountFormDialog({ poolId, account, open, onOpenChange }: AccountFormDialogProps) {
    const t = useTranslations('pool');
    const createAccount = useCreatePoolAccount(poolId);
    const updateAccount = useUpdatePoolAccount(poolId);
    const [proxyValue, setProxyValue] = useState<{ proxy_mode: ProxyMode; proxy_config_id: number | null }>({ proxy_mode: 'direct', proxy_config_id: null });
    const [form, setForm] = useState<PoolAccountRequest>(emptyForm);

    useEffect(() => {
        if (open) {
            if (account) {
                setForm({
                    name: account.name,
                    platform: account.platform || 'custom',
                    type: account.type || 'apikey',
                    models: account.models || '',
                    credentials: '',
                    base_url: account.base_url || '',
                    priority: account.priority,
                    concurrency: account.concurrency,
                    weight: account.weight ?? 0,
                    load_factor: account.load_factor ?? 0,
                    auto_pause_on_expired: account.auto_pause_on_expired ?? false,
                    expires_at: account.expires_at ?? 0,
                    proxy_config_id: account.proxy_config_id ?? null,
                    notes: account.notes || '',
                    extra: account.extra || '',
                });
                setProxyValue({ proxy_mode: account.proxy_config_id ? 'pool' : 'direct', proxy_config_id: account.proxy_config_id ?? null });
            } else {
                setForm({ ...emptyForm, base_url: DEFAULT_BASE_URL_BY_PLATFORM.anthropic });
                setProxyValue({ proxy_mode: 'direct', proxy_config_id: null });
            }
        }
    }, [open, account]);

    // 平台切换时更新默认 base_url 与可用类型。
    const handlePlatformChange = (platform: string) => {
        const types = POOL_TYPE_OPTIONS_BY_PLATFORM[platform as PoolPlatform] || ['apikey'];
        const newType = types.includes(form.type as PoolAccountType) ? form.type : types[0];
        setForm({
            ...form,
            platform,
            type: newType,
            base_url: DEFAULT_BASE_URL_BY_PLATFORM[platform as PoolPlatform] || '',
        });
    };

    const handleTypeChange = (type: string) => {
        setForm({ ...form, type });
    };

    const handleOAuthLogin = async () => {
        const platform = form.platform as PoolPlatform;
        if (!platformSupportsOAuth(platform)) return;
        try {
            // 先通过管理 API 发起 initiate（带 JWT），拿到 auth_url 后再跳转授权页。
            const data = await apiClient.get<{ auth_url: string }>(
                '/api/v1/pool/oauth/initiate',
                { platform, pool_id: poolId },
            );
            if (!data?.auth_url) {
                throw new Error(t('oauthInitiateFailed'));
            }
            window.location.href = data.auth_url;
        } catch (e) {
            toast.error(String(e));
        }
    };

    const handleSubmit = () => {
        const payload: PoolAccountRequest = {
            ...form,
            proxy_config_id: proxyValue.proxy_mode === 'pool' ? proxyValue.proxy_config_id : null,
        };
        const onSuccess = () => {
            onOpenChange(false);
            toast.success(account ? t('accountUpdated') : t('accountCreated'));
        };
        const onError = (e: unknown) => toast.error(String(e));

        if (account) {
            updateAccount.mutate(
                { accountId: account.id, data: payload },
                { onSuccess, onError },
            );
        } else {
            createAccount.mutate(payload, { onSuccess, onError });
        }
    };

    const platform = form.platform as PoolPlatform;
    const acctType = form.type as PoolAccountType;
    const typeOptions = POOL_TYPE_OPTIONS_BY_PLATFORM[platform] || ['apikey'];
    const isOAuth = acctType === 'oauth';
    const isPending = createAccount.isPending || updateAccount.isPending;

    if (!open) return null;

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={() => onOpenChange(false)}>
            <div className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-lg bg-card p-6 shadow-lg" onClick={(e) => e.stopPropagation()}>
                <h3 className="mb-4 text-lg font-semibold">{account ? t('editAccount') : t('addAccount')}</h3>
                <div className="space-y-4">
                    {/* 平台分段选择器 */}
                    <div>
                        <Label>{t('platform')}</Label>
                        <div className="mt-2 flex flex-wrap gap-2">
                            {POOL_PLATFORM_OPTIONS.map((opt) => (
                                <Button
                                    key={opt.value}
                                    type="button"
                                    variant={form.platform === opt.value ? 'default' : 'outline'}
                                    size="sm"
                                    onClick={() => handlePlatformChange(opt.value)}
                                >
                                    {opt.label}
                                </Button>
                            ))}
                        </div>
                    </div>

                    {/* 类型选择 */}
                    <div>
                        <Label>{t('type')}</Label>
                        <Select value={form.type} onValueChange={handleTypeChange}>
                            <SelectTrigger className="mt-1"><SelectValue /></SelectTrigger>
                            <SelectContent>
                                {typeOptions.map((tp) => (
                                    <SelectItem key={tp} value={tp}>{t(`typeLabels.${tp}`)}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    {/* 账号名 */}
                    <div>
                        <Label>{t('accountName')}</Label>
                        <Input
                            className="mt-1"
                            value={form.name}
                            onChange={(e) => setForm({ ...form, name: e.target.value })}
                            placeholder="claude-01"
                        />
                    </div>

                    {/* 凭据区：按 type 条件渲染 */}
                    {isOAuth ? (
                        <div className="space-y-2 rounded-lg border border-primary/30 bg-primary/5 p-3">
                            <div className="flex items-center justify-between">
                                <Label className="text-sm">
    {t('oauthLogin')}
    <Hint text={t('oauthLoginHint')} />
</Label>
                                <Button type="button" size="sm" onClick={handleOAuthLogin}>
                                    {t('oauthLoginBtn')}
                                </Button>
                            </div>
                            <div>
                                <Label className="text-xs">{t('oauthManualPaste')}</Label>
                                <textarea
                                    className="mt-1 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs"
                                    rows={3}
                                    value={form.credentials}
                                    onChange={(e) => setForm({ ...form, credentials: e.target.value })}
                                    placeholder='{"access_token":"...","refresh_token":"...","account_id":"..."}'
                                />
                            </div>
                        </div>
                    ) : acctType === 'apikey' ? (
                        <div>
                            <Label>
    {t('apiKey')}
    <Hint text={`${t('credentialsHint')} {"type":"apikey","api_key":"sk-..."}`} />
</Label>
                            <Input
                                className="mt-1 font-mono"
                                type="password"
                                value={form.credentials}
                                onChange={(e) => setForm({ ...form, credentials: e.target.value })}
                                placeholder="sk-..."
                            />
                        </div>
                    ) : acctType === 'cookie' ? (
                        <div>
                            <Label>{t('cookie')}</Label>
                            <textarea
                                className="mt-1 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs"
                                rows={2}
                                value={form.credentials}
                                onChange={(e) => setForm({ ...form, credentials: e.target.value })}
                                placeholder="sessionKey=... (volcengine: Cookie|||csrf-token)"
                            />
                        </div>
                    ) : acctType === 'upstream' ? (
                        <div>
                            <Label>{t('apiKey')}</Label>
                            <Input
                                className="mt-1 font-mono"
                                type="password"
                                value={form.credentials}
                                onChange={(e) => setForm({ ...form, credentials: e.target.value })}
                                placeholder="sk-..."
                            />
                            <div className="mt-2">
                                <Label>{t('baseUrl')}</Label>
                                <Input
                                    className="mt-1"
                                    value={form.base_url}
                                    onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                                    placeholder="https://your-upstream.com"
                                />
                            </div>
                        </div>
                    ) : null}

                    {/* base_url（upstream 已在上面渲染，其余在此显示） */}
                    {acctType !== 'upstream' && (
                        <div>
                            <Label>{t('baseUrl')}</Label>
                            <Input
                                className="mt-1"
                                value={form.base_url}
                                onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                                placeholder={DEFAULT_BASE_URL_BY_PLATFORM[platform] || 'https://...'}
                            />
                        </div>
                    )}

                    {/* 模型绑定 */}
                    <div>
                        <Label>{t('models')}</Label>
                        <Input
                            className="mt-1"
                            value={form.models}
                            onChange={(e) => setForm({ ...form, models: e.target.value })}
                            placeholder={t('modelsPlaceholder')}
                        />
                        {form.models && (
                            <div className="mt-2 flex flex-wrap gap-1">
                                {form.models.split(',').filter(Boolean).map((m, i) => (
                                    <Badge key={i} variant="secondary" className="text-xs">{m.trim()}</Badge>
                                ))}
                            </div>
                        )}
                    </div>

                    {/* 代理 */}
                    <div>
                        <Label>{t('proxyConfig')}</Label>
                        <div className="mt-1">
                            <ProxySelector
                                value={proxyValue}
                                onChange={(v) => setProxyValue({ proxy_mode: v.proxy_mode, proxy_config_id: v.proxy_config_id ?? null })}
                            />
                        </div>
                    </div>

                    {/* priority / concurrency / weight / load_factor */}
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <Label>{t('priority')}</Label>
                            <Input
                                type="number"
                                className="mt-1"
                                value={form.priority}
                                onChange={(e) => setForm({ ...form, priority: Number(e.target.value) })}
                            />
                        </div>
                        <div>
                            <Label>{t('concurrency')} (0={t('inheritPool')})</Label>
                            <Input
                                type="number"
                                className="mt-1"
                                value={form.concurrency}
                                onChange={(e) => setForm({ ...form, concurrency: Number(e.target.value) })}
                            />
                        </div>
                        <div>
                            <Label>
    {t('weight')}
    <Hint text={t('weightHint')} />
</Label>
                            <Input
                                type="number"
                                className="mt-1"
                                value={form.weight ?? 0}
                                onChange={(e) => setForm({ ...form, weight: Number(e.target.value) })}
                            />
                        </div>
                        <div>
                            <Label>
    {t('loadFactor')} (0={t('inheritPool')})
    <Hint text={t('loadFactorHint')} />
</Label>
                            <Input
                                type="number"
                                className="mt-1"
                                value={form.load_factor ?? 0}
                                onChange={(e) => setForm({ ...form, load_factor: Number(e.target.value) })}
                            />
                        </div>
                    </div>

                    {/* auto-pause lifecycle */}
                    <div className="rounded-lg border border-border/60 p-3">
                        <div className="flex items-center justify-between">
                            <Label>{t('autoPauseOnExpired')}</Label>
                            <input
                                type="checkbox"
                                className="h-4 w-4"
                                checked={form.auto_pause_on_expired ?? false}
                                onChange={(e) => setForm({ ...form, auto_pause_on_expired: e.target.checked })}
                            />
                        </div>
                        {form.auto_pause_on_expired && (
                            <div className="mt-2">
                                <Label className="text-xs">{t('expiresAt')}</Label>
                                <Input
                                    type="datetime-local"
                                    className="mt-1"
                                    value={form.expires_at ? new Date(form.expires_at * 1000).toISOString().slice(0, 16) : ''}
                                    onChange={(e) => {
                                        const v = e.target.value;
                                        setForm({ ...form, expires_at: v ? Math.floor(new Date(v).getTime() / 1000) : 0 });
                                    }}
                                />
                            </div>
                        )}
                    </div>

                    {/* Platform extras + header overrides */}
                    <ExtrasEditor form={form} setForm={setForm} platform={platform} acctType={acctType} />
                    <div>
                        <Label>{t('notes')}</Label>
                        <Input
                            className="mt-1"
                            value={form.notes}
                            onChange={(e) => setForm({ ...form, notes: e.target.value })}
                        />
                    </div>

                    {account?.error_message && (
                        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">
                            {account.error_message}
                        </div>
                    )}
                </div>

                <div className="mt-6 flex justify-end gap-2">
                    <Button variant="outline" onClick={() => onOpenChange(false)}>{t('cancel')}</Button>
                    <Button onClick={handleSubmit} disabled={isPending || (!account && !form.credentials && !isOAuth)}>
                        {account ? t('save') : t('addAccount')}
                    </Button>
                </div>
            </div>
        </div>
    );
}

// ExtrasEditor 平台附加字段与自定义请求头编辑器。按 platform 显隐 gemini/openai 区。
function ExtrasEditor({ form, setForm, platform, acctType }: {
    form: PoolAccountRequest;
    setForm: (next: PoolAccountRequest) => void;
    platform: PoolPlatform;
    acctType: PoolAccountType;
}) {
    const t = useTranslations('pool');
    const extra = parseExtra(form.extra ?? '');

    const updateExtra = (patch: Partial<PoolAccountExtra>) => {
        const next: PoolAccountExtra = { ...extra, ...patch };
        // 删除空字段让东西干净
        Object.keys(next).forEach((k) => {
            const key = k as keyof PoolAccountExtra;
            const v = next[key];
            if (v === '' || v === undefined) delete next[key];
        });
        setForm({ ...form, extra: JSON.stringify(next) });
    };

    const overrideEligible = isHeaderOverrideEligible(platform, acctType);
    const headerEntries: Array<{ k: string; v: string }> = Object.entries(extra.header_overrides ?? {}).map(([k, v]) => ({ k, v }));

    const addHeaderRow = () => {
        if (headerEntries.length >= 20) return;
        const next = { ...(extra.header_overrides ?? {}) };
        next[`x-custom-${headerEntries.length + 1}`] = '';
        updateExtra({ header_overrides: next, header_overrides_enabled: true });
    };
    const setHeaderRow = (idx: number, key: string, value: string) => {
        const entries = [...headerEntries];
        entries[idx] = { k: key, v: value };
        const next: Record<string, string> = {};
        for (const { k, v } of entries) if (k) next[k] = v;
        updateExtra({ header_overrides: next, header_overrides_enabled: true });
    };
    const removeHeaderRow = (idx: number) => {
        const entries = headerEntries.filter((_, i) => i !== idx);
        const next: Record<string, string> = {};
        for (const { k, v } of entries) if (k) next[k] = v;
        updateExtra({ header_overrides: next, header_overrides_enabled: true });
    };

    return (
        <div className="space-y-3 rounded-lg border border-border/60 p-3">
            <Label>{t('extras.section')}</Label>

            {platform === 'gemini' && (
                <div className="grid grid-cols-2 gap-3">
                    <div>
                        <Label className="text-xs">{t('extras.projectId')}</Label>
                        <Input
                            className="mt-1"
                            value={extra.project_id ?? ''}
                            onChange={(e) => updateExtra({ project_id: e.target.value })}
                        />
                    </div>
                    <div>
                        <Label className="text-xs">{t('extras.tierId')}</Label>
                        <Input
                            className="mt-1"
                            value={extra.tier_id ?? ''}
                            onChange={(e) => updateExtra({ tier_id: e.target.value })}
                        />
                    </div>
                    <div>
                        <Label className="text-xs">{t('extras.oauthType')}</Label>
                        <Select
                            value={extra.oauth_type ?? ''}
                            onValueChange={(v) => updateExtra({ oauth_type: v })}
                        >
                            <SelectTrigger className="mt-1"><SelectValue /></SelectTrigger>
                            <SelectContent>
                                <SelectItem value="">{t('extras.oauthTypeNone')}</SelectItem>
                                <SelectItem value="code_assist">code_assist</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                </div>
            )}

            {platform === 'openai' && (
                <div>
                    <Label className="text-xs">{t('extras.authMode')}</Label>
                    <Select
                        value={extra.auth_mode ?? ''}
                        onValueChange={(v) => updateExtra({ auth_mode: v })}
                    >
                        <SelectTrigger className="mt-1"><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value="">{t('extras.authModeDefault')}</SelectItem>
                            <SelectItem value="personalAccessToken">personalAccessToken</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
            )}

            <div>
                <Label className="text-xs">{t('extras.tlsFingerprint')} ({t('extras.reserved')})</Label>
                <Input
                    className="mt-1"
                    value={extra.tls_fingerprint_profile ?? ''}
                    onChange={(e) => updateExtra({ tls_fingerprint_profile: e.target.value })}
                    placeholder="chrome_120"
                />
            </div>

            {overrideEligible && (
                <div className="border-t border-border/60 pt-3">
                    <div className="flex items-center justify-between">
                        <Label className="text-xs">{t('headerOverride.section')}</Label>
                        <input
                            type="checkbox"
                            className="h-4 w-4"
                            checked={extra.header_overrides_enabled ?? false}
                            onChange={(e) => updateExtra({ header_overrides_enabled: e.target.checked })}
                        />
                    </div>
                    {extra.header_overrides_enabled && (
                        <div className="mt-2 space-y-2">
                            {headerEntries.map(({ k, v }, idx) => {
                                const blocked = HEADER_OVERRIDE_BLOCKED[k.toLowerCase()]
                                    || k.toLowerCase().startsWith('x-codex-');
                                return (
                                    <div key={idx} className="flex gap-2">
                                        <Input
                                            placeholder={t('headerOverride.row.key')}
                                            value={k}
                                            onChange={(e) => setHeaderRow(idx, e.target.value, v)}
                                            className={blocked ? 'border-destructive/50' : ''}
                                        />
                                        <Input
                                            placeholder={t('headerOverride.row.value')}
                                            value={v}
                                            onChange={(e) => setHeaderRow(idx, k, e.target.value)}
                                        />
                                        <Button type="button" variant="outline" size="sm" onClick={() => removeHeaderRow(idx)}>
                                            {t('headerOverride.remove')}
                                        </Button>
                                    </div>
                                );
                            })}
                            <Button type="button" variant="outline" size="sm" onClick={addHeaderRow} disabled={headerEntries.length >= 20}>
                                {t('headerOverride.add')}
                            </Button>
                            <p className="text-xs text-muted-foreground">{t('headerOverride.blockedWarning')}</p>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
