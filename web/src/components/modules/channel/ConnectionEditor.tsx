'use client';

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { ArrowDown, ArrowUp, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useChannelConnectionPreview, type ChannelEndpoint, type ConnectionConfig } from '@/api/endpoints/channel';
import { connectionURLPreview, newEndpoint } from './connection-config';

const selectClass = 'h-11 w-full min-w-0 rounded-lg border border-border bg-card px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';

export function ConnectionEditor({ value, onChange, channelId, idPrefix }: {
    value?: ConnectionConfig; onChange: (config: ConnectionConfig) => void;
    channelId?: number; idPrefix: string;
}) {
    const t = useTranslations('channel.connection');
    // 旧渠道（无 connection_config、但有渠道 id）打开编辑时，自动读取迁移预览并把结果静默作为草稿；
    // 保存前旧渠道的运行时行为不变，这里只影响编辑器的显示层。
    const needsPrefill = !value && channelId != null;
    const preview = useChannelConnectionPreview(needsPrefill ? channelId : undefined);
    const [notice, setNotice] = useState('');
    const [legacyReviewRequired, setLegacyReviewRequired] = useState(false);
    const prefillApplied = useRef(false);
    useEffect(() => {
        if (!needsPrefill || !preview.data?.config) return;
        if (preview.data.automatic === false) setLegacyReviewRequired(true);
        if (prefillApplied.current) return;
        prefillApplied.current = true;
        onChange(preview.data.config);
    }, [needsPrefill, onChange, preview.data]);
    // 预填结果先直接作为本次渲染的草稿，避免父级状态回写前闪一帧加载态。
    const config = value ?? (needsPrefill ? preview.data?.config : undefined);
    if (!config) {
        if (needsPrefill && preview.isError) return (
            <div className="min-w-0 space-y-3 rounded-lg border border-border p-4">
                <p role="alert" className="text-sm text-destructive">{t('previewFailed')}</p>
                <Button type="button" variant="outline" size="sm" onClick={() => { void preview.refetch(); }}>{t('previewRetry')}</Button>
            </div>
        );
        return <div role="status" aria-live="polite" className="flex min-h-24 items-center justify-center gap-2 rounded-lg border border-border p-4">
            <RefreshCw className="size-4 animate-spin text-muted-foreground" aria-hidden="true" />
            <span className="text-sm text-muted-foreground">{t('previewLoading')}</span>
        </div>;
    }
    const update = (id: string, patch: Partial<ChannelEndpoint>) => onChange({ ...config, endpoints: config.endpoints.map((e) => e.id === id ? { ...e, ...patch } : e) });
    const move = (index: number, delta: number) => {
        const next = [...config.endpoints];
        [next[index], next[index + delta]] = [next[index + delta], next[index]];
        onChange({ ...config, endpoints: next });
        setNotice(t('moved', { position: index + delta + 1 }));
    };
    return <div className="min-w-0 space-y-4">
        {legacyReviewRequired && <p role="status" className="text-sm text-muted-foreground">{t('autoPrefillReview')}</p>}
        <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="font-semibold">{t('title')}</h3>
            <Button type="button" variant="outline" disabled={config.endpoints.length >= 32} onClick={() => onChange({ ...config, endpoints: [...config.endpoints, newEndpoint()] })}><Plus className="size-4" />{t('add')}</Button>
        </div>
        <p className="text-sm text-muted-foreground">{t('hint')}</p>
        <label className="block space-y-1 text-sm"><span>{t('selection')}</span>
            <select className={selectClass} value={config.selection} onChange={(e) => onChange({ ...config, selection: e.target.value as ConnectionConfig['selection'] })}>
                <option value="same_protocol">{t('sameProtocol')}</option><option value="configured">{t('configured')}</option>
            </select>
        </label>
        <div aria-live="polite" className="sr-only">{notice}</div>
        <ol className="min-w-0 space-y-3">
            {config.endpoints.map((endpoint, index) => {
                const prefix = `${idPrefix}-endpoint-${endpoint.id}`;
                const previewURL = connectionURLPreview(endpoint);
                const supportsForward = ['chat', 'responses', 'messages'].includes(endpoint.protocol);
                const keepLegacyOption = ['cloudflare', 'volcengine', 'codex'].includes(endpoint.protocol) || endpoint.compatibility === 'legacy' || endpoint.compatibility === 'legacy_mimo';
                const canSwitchToStandard = endpoint.compatibility === 'legacy' || endpoint.compatibility === 'legacy_mimo';
                return <li key={endpoint.id} className="min-w-0 space-y-3 rounded-lg border border-border p-3">
                    <div className="flex items-center justify-between gap-2">
                        <span className="text-sm font-medium">{t('endpoint', { index: index + 1 })}</span>
                        <div className="flex shrink-0 gap-1">
                            <Button type="button" variant="ghost" size="icon" aria-label={t('up', { index: index + 1 })} disabled={index === 0} onClick={() => move(index, -1)}><ArrowUp className="size-4" /></Button>
                            <Button type="button" variant="ghost" size="icon" aria-label={t('down', { index: index + 1 })} disabled={index === config.endpoints.length - 1} onClick={() => move(index, 1)}><ArrowDown className="size-4" /></Button>
                            <Button type="button" variant="ghost" size="icon" aria-label={t('remove', { index: index + 1 })} disabled={config.endpoints.length === 1} onClick={() => {
                                const endpoints = config.endpoints.filter((e) => e.id !== endpoint.id);
                                onChange({ ...config, endpoints, catalog: config.catalog.endpoint_id === endpoint.id ? { format: 'manual' } : config.catalog });
                            }}><Trash2 className="size-4" /></Button>
                        </div>
                    </div>
                    <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                        <label className="min-w-0 space-y-1 text-sm" htmlFor={`${prefix}-protocol`}><span>{t('protocol')}</span>
                            <select id={`${prefix}-protocol`} className={selectClass} value={endpoint.protocol} onChange={(e) => update(endpoint.id, { protocol: e.target.value as ChannelEndpoint['protocol'], compatibility: '', forward_mode: 'convert', auth: 'default' })}>
                                <option value="chat">Chat Completions</option><option value="responses">Responses</option><option value="messages">Anthropic Messages</option><option value="gemini">Gemini generateContent</option><option value="embeddings">Embeddings</option>
                                {['cloudflare', 'volcengine', 'codex'].includes(endpoint.protocol) && <optgroup label={t('legacy')}><option value={endpoint.protocol}>{endpoint.protocol}</option></optgroup>}
                            </select>
                        </label>
                        <label className="min-w-0 space-y-1 text-sm" htmlFor={`${prefix}-url`}><span>{t('url')}</span>
                            <Input id={`${prefix}-url`} className="min-w-0 w-full" value={endpoint.url} type="url" required placeholder="https://example.com/v1" onChange={(e) => update(endpoint.id, { url: e.target.value })} />
                        </label>
                    </div>
                    {previewURL && endpoint.forward_mode !== 'raw' && <p className="break-all text-xs text-muted-foreground">{t('preview')}: {previewURL}</p>}
                    {endpoint.url_mode === 'base' && /\/(chat\/completions|responses|messages|embeddings)(\?|$)/.test(endpoint.url) && <div className="space-y-1 text-sm"><p>{t('fullSuggestion')}</p><Button type="button" variant="outline" size="sm" onClick={() => update(endpoint.id, { url_mode: 'full', forward_mode: 'convert' })}>{t('useFull')}</Button></div>}
                    <details className="min-w-0 rounded-md bg-muted/30 p-2">
                        <summary className="cursor-pointer text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{t('advanced')}</summary>
                        <div className="mt-3 grid min-w-0 gap-3 sm:grid-cols-2">
                            <label className="space-y-1 text-sm"><span>{t('urlMode')}</span><select className={selectClass} value={endpoint.url_mode} onChange={(e) => update(endpoint.id, { url_mode: e.target.value as ChannelEndpoint['url_mode'], forward_mode: 'convert' })}><option value="base">{t('base')}</option><option value="full">{t('full')}</option></select></label>
                            <label className="space-y-1 text-sm"><span>{t('auth')}</span><select className={selectClass} value={endpoint.auth} onChange={(e) => update(endpoint.id, { auth: e.target.value as ChannelEndpoint['auth'] })}>
                                <option value="default">{t('default')}</option><option value="bearer">Authorization: Bearer</option><option value="api_key">api-key</option><option value="x_api_key">x-api-key</option><option value="google">x-goog-api-key</option><option value="none">{t('noAuth')}</option>{endpoint.auth === 'legacy' && <option value="legacy">{t('legacy')}</option>}
                            </select></label>
                            <div className="space-y-1 text-sm">
                                <label className="block space-y-1"><span>{t('compatibility')}</span><select className={selectClass} value={endpoint.compatibility || ''} onChange={(e) => update(endpoint.id, { compatibility: e.target.value as ChannelEndpoint['compatibility'] })}><option value="">{t('standard')}</option>{endpoint.protocol === 'chat' && <option value="mimo">MiMo</option>}{keepLegacyOption && <option value="legacy">{t('legacy')}</option>}{endpoint.compatibility === 'legacy_mimo' && <option value="legacy_mimo">{t('legacy')} MiMo</option>}</select></label>
                                {canSwitchToStandard && <Button type="button" variant="outline" size="sm" onClick={() => update(endpoint.id, { compatibility: '' })}>{t('switchToStandard')}</Button>}
                            </div>
                            {supportsForward && endpoint.url_mode === 'base' && <label className="space-y-1 text-sm"><span>{t('forwardMode')}</span><select className={selectClass} value={endpoint.forward_mode || 'convert'} onChange={(e) => update(endpoint.id, { forward_mode: e.target.value as ChannelEndpoint['forward_mode'] })}><option value="convert">{t('convert')}</option><option value="passthrough">{t('passthrough')}</option><option value="raw">{t('raw')}</option></select></label>}
                        </div>
                        <p className="mt-2 text-xs text-muted-foreground">{t('advancedHint')}</p>
                        {endpoint.protocol === 'gemini' && endpoint.url_mode === 'full' && <code className="text-xs">/models/{'{model}'}:generateContent</code>}
                        <div className="mt-3 space-y-2">
                            <span className="text-sm">{t('headers')}</span>
                            {(endpoint.headers || []).map((header, h) => <div key={h} className="flex min-w-0 flex-wrap gap-2">
                                <Input className="min-w-0 flex-1" aria-label={t('headerName')} value={header.header_key} onChange={(e) => update(endpoint.id, { headers: endpoint.headers?.map((entry, i) => i === h ? { ...entry, header_key: e.target.value } : entry) })} />
                                <Input className="min-w-0 flex-1" type="password" autoComplete="off" aria-label={t('headerValue')} value={header.header_value} onChange={(e) => update(endpoint.id, { headers: endpoint.headers?.map((entry, i) => i === h ? { ...entry, header_value: e.target.value } : entry) })} />
                                <Button type="button" variant="ghost" aria-label={t('removeHeader')} onClick={() => update(endpoint.id, { headers: endpoint.headers?.filter((_, i) => i !== h) })}><Trash2 className="size-4" /></Button>
                            </div>)}
                            <Button type="button" variant="outline" size="sm" disabled={(endpoint.headers?.length || 0) >= 32} onClick={() => update(endpoint.id, { headers: [...(endpoint.headers || []), { header_key: '', header_value: '' }] })}>{t('addHeader')}</Button>
                        </div>
                    </details>
                </li>;
            })}
        </ol>
        <details className="rounded-lg border border-border p-3" open={config.catalog.format === 'manual'}>
            <summary className="cursor-pointer text-sm font-medium">{t('catalog')}</summary>
            <div className="mt-3 space-y-3">
                <p className="text-xs text-muted-foreground">{t('catalogHint')}</p>
                <label className="block space-y-1 text-sm"><span>{t('catalogFormat')}</span><select className={selectClass} value={config.catalog.format} onChange={(e) => onChange({ ...config, catalog: { ...config.catalog, format: e.target.value as ConnectionConfig['catalog']['format'], endpoint_id: config.catalog.endpoint_id || config.endpoints[0]?.id } })}>
                    <option value="manual">{t('manual')}</option><option value="openai">OpenAI data[]</option><option value="anthropic">Anthropic data[]</option><option value="gemini">Gemini models[]</option>{config.catalog.format === 'cloudflare' && <option value="cloudflare">Cloudflare result[] ({t('legacy')})</option>}
                </select></label>
                {config.catalog.format !== 'manual' && <>
                    <label className="block space-y-1 text-sm"><span>{t('catalogEndpoint')}</span><select className={selectClass} value={config.catalog.endpoint_id || ''} required onChange={(e) => onChange({ ...config, catalog: { ...config.catalog, endpoint_id: e.target.value } })}>
                        <option value="" disabled>{t('selectEndpoint')}</option>{config.endpoints.map((endpoint, i) => <option key={endpoint.id} value={endpoint.id}>{i + 1}. {endpoint.protocol}</option>)}
                    </select></label>
                    <label className="block space-y-1 text-sm"><span>{t('catalogURL')}</span><Input type="url" value={config.catalog.url || ''} placeholder="https://example.com/v1/models" required={config.endpoints.find((e) => e.id === config.catalog.endpoint_id)?.url_mode === 'full'} onChange={(e) => onChange({ ...config, catalog: { ...config.catalog, url: e.target.value } })} /></label>
                </>}
            </div>
        </details>
    </div>;
}
