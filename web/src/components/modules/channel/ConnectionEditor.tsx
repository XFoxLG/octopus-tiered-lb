'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useChannelConnectionPreview, type ChannelEndpoint, type ConnectionConfig } from '@/api/endpoints/channel';
import { connectionURLPreview, newConnectionConfig, newEndpoint } from './connection-config';
import type { ChannelFormData } from './Form';

const selectClass = 'h-11 w-full min-w-0 rounded-lg border border-border bg-card px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';

export function ConnectionEditor({ value, onChange, legacy, channelId, idPrefix }: {
    value?: ConnectionConfig; onChange: (config: ConnectionConfig) => void;
    legacy: ChannelFormData; channelId?: number; idPrefix: string;
}) {
    const t = useTranslations('channel.connection');
    const preview = useChannelConnectionPreview(!value ? channelId : undefined);
    const [notice, setNotice] = useState('');
    if (!value) return (
        <div className="min-w-0 space-y-3 rounded-lg border border-border p-4">
            <h3 className="font-semibold">{t('legacyTitle')}</h3>
            <p className="text-sm text-muted-foreground">{t('legacyHint')}</p>
            <dl className="text-sm">
                <dt>{t('legacyType')}</dt><dd>{legacy.type}</dd>
                {legacy.base_urls.map((entry, i) => <dd key={i} className="break-all">{entry.url} · {entry.suffix_mode || 'auto'} · {entry.protocol || t('inherited')}</dd>)}
            </dl>
            {preview.isError && <p role="alert" className="text-sm text-destructive">{t('previewFailed')}</p>}
            {preview.data?.groups?.map((group, i) => <p key={i} className="text-sm break-words">{group.name} · {group.outbound_format || t('inherited')} · {group.endpoint_type}</p>)}
            <p className="text-sm text-muted-foreground">{preview.data?.automatic ? t('equivalent') : t('reviewRequired')}</p>
            <Button type="button" variant="outline" disabled={!!channelId && !preview.data} onClick={() => onChange(preview.data?.config ?? newConnectionConfig())}>{t('configure')}</Button>
        </div>
    );
    const update = (id: string, patch: Partial<ChannelEndpoint>) => onChange({ ...value, endpoints: value.endpoints.map((e) => e.id === id ? { ...e, ...patch } : e) });
    const move = (index: number, delta: number) => {
        const next = [...value.endpoints];
        [next[index], next[index + delta]] = [next[index + delta], next[index]];
        onChange({ ...value, endpoints: next });
        setNotice(t('moved', { position: index + delta + 1 }));
    };
    return <div className="min-w-0 space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="font-semibold">{t('title')}</h3>
            <Button type="button" variant="outline" disabled={value.endpoints.length >= 32} onClick={() => onChange({ ...value, endpoints: [...value.endpoints, newEndpoint()] })}><Plus className="size-4" />{t('add')}</Button>
        </div>
        <p className="text-sm text-muted-foreground">{t('hint')}</p>
        <label className="block space-y-1 text-sm"><span>{t('selection')}</span>
            <select className={selectClass} value={value.selection} onChange={(e) => onChange({ ...value, selection: e.target.value as ConnectionConfig['selection'] })}>
                <option value="same_protocol">{t('sameProtocol')}</option><option value="configured">{t('configured')}</option>
            </select>
        </label>
        <div aria-live="polite" className="sr-only">{notice}</div>
        <ol className="min-w-0 space-y-3">
            {value.endpoints.map((endpoint, index) => {
                const prefix = `${idPrefix}-endpoint-${endpoint.id}`;
                const previewURL = connectionURLPreview(endpoint);
                const supportsForward = ['chat', 'responses', 'messages'].includes(endpoint.protocol);
                return <li key={endpoint.id} className="min-w-0 space-y-3 rounded-lg border border-border p-3">
                    <div className="flex items-center justify-between gap-2">
                        <span className="text-sm font-medium">{t('endpoint', { index: index + 1 })}</span>
                        <div className="flex shrink-0 gap-1">
                            <Button type="button" variant="ghost" size="icon" aria-label={t('up', { index: index + 1 })} disabled={index === 0} onClick={() => move(index, -1)}><ArrowUp className="size-4" /></Button>
                            <Button type="button" variant="ghost" size="icon" aria-label={t('down', { index: index + 1 })} disabled={index === value.endpoints.length - 1} onClick={() => move(index, 1)}><ArrowDown className="size-4" /></Button>
                            <Button type="button" variant="ghost" size="icon" aria-label={t('remove', { index: index + 1 })} disabled={value.endpoints.length === 1} onClick={() => {
                                const endpoints = value.endpoints.filter((e) => e.id !== endpoint.id);
                                onChange({ ...value, endpoints, catalog: value.catalog.endpoint_id === endpoint.id ? { format: 'manual' } : value.catalog });
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
                            <label className="space-y-1 text-sm"><span>{t('compatibility')}</span><select className={selectClass} value={endpoint.compatibility || ''} onChange={(e) => update(endpoint.id, { compatibility: e.target.value as ChannelEndpoint['compatibility'] })}><option value="">{t('standard')}</option>{endpoint.protocol === 'chat' && <option value="mimo">MiMo</option>}<option value="legacy">{t('legacy')}</option>{endpoint.compatibility === 'legacy_mimo' && <option value="legacy_mimo">{t('legacy')} MiMo</option>}</select></label>
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
        <details className="rounded-lg border border-border p-3" open={value.catalog.format === 'manual'}>
            <summary className="cursor-pointer text-sm font-medium">{t('catalog')}</summary>
            <div className="mt-3 space-y-3">
                <p className="text-xs text-muted-foreground">{t('catalogHint')}</p>
                <label className="block space-y-1 text-sm"><span>{t('catalogFormat')}</span><select className={selectClass} value={value.catalog.format} onChange={(e) => onChange({ ...value, catalog: { ...value.catalog, format: e.target.value as ConnectionConfig['catalog']['format'], endpoint_id: value.catalog.endpoint_id || value.endpoints[0]?.id } })}>
                    <option value="manual">{t('manual')}</option><option value="openai">OpenAI data[]</option><option value="anthropic">Anthropic data[]</option><option value="gemini">Gemini models[]</option>{value.catalog.format === 'cloudflare' && <option value="cloudflare">Cloudflare result[] ({t('legacy')})</option>}
                </select></label>
                {value.catalog.format !== 'manual' && <>
                    <label className="block space-y-1 text-sm"><span>{t('catalogEndpoint')}</span><select className={selectClass} value={value.catalog.endpoint_id || ''} required onChange={(e) => onChange({ ...value, catalog: { ...value.catalog, endpoint_id: e.target.value } })}>
                        <option value="" disabled>{t('selectEndpoint')}</option>{value.endpoints.map((endpoint, i) => <option key={endpoint.id} value={endpoint.id}>{i + 1}. {endpoint.protocol}</option>)}
                    </select></label>
                    <label className="block space-y-1 text-sm"><span>{t('catalogURL')}</span><Input type="url" value={value.catalog.url || ''} placeholder="https://example.com/v1/models" required={value.endpoints.find((e) => e.id === value.catalog.endpoint_id)?.url_mode === 'full'} onChange={(e) => onChange({ ...value, catalog: { ...value.catalog, url: e.target.value } })} /></label>
                </>}
            </div>
        </details>
    </div>;
}
