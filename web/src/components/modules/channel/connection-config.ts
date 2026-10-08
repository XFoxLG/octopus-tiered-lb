import type { Channel, ChannelEndpoint, ConnectionConfig } from '@/api/endpoints/channel';

export function newEndpoint(): ChannelEndpoint {
    return { id: crypto.randomUUID(), protocol: 'chat', url: '', url_mode: 'base', auth: 'default' };
}
export function newConnectionConfig(): ConnectionConfig {
    const endpoint = newEndpoint();
    return { version: 1, selection: 'same_protocol', endpoints: [endpoint], catalog: { format: 'openai', endpoint_id: endpoint.id } };
}
export function connectionURLPreview(endpoint: ChannelEndpoint, model = 'MODEL', stream = false): string {
    try {
        const url = new URL(endpoint.url);
        if (!['https:', 'http:'].includes(url.protocol)) return '';
        if (endpoint.protocol === 'cloudflare' && !model.startsWith('@')) model = '@cf/' + model;
        if (endpoint.url_mode === 'full') {
            url.pathname = decodeURI(url.pathname).replaceAll('{model}', model.replace(/^models\//, ''));
        } else {
            const root = url.pathname.replace(/\/+$/, '') || (endpoint.protocol === 'gemini' ? '/v1beta' : endpoint.protocol === 'cloudflare' ? '' : '/v1');
            const suffix = { chat: '/chat/completions', responses: '/responses', messages: '/messages', embeddings: '/embeddings', gemini: `/models/${model.replace(/^models\//, '')}:generateContent`, cloudflare: `/ai/run/${model}`, volcengine: '/responses', codex: '/responses' }[endpoint.protocol];
            url.pathname = root + suffix;
        }
        if (endpoint.protocol === 'gemini' && stream) {
            url.pathname = url.pathname.replace(/:(streamGenerateContent|generateContent)$/, '') + ':streamGenerateContent';
            url.searchParams.set('alt', 'sse');
        }
        // Do not display query credentials in the preview.
        for (const key of Array.from(url.searchParams.keys())) {
            if (/key|token|secret|signature|credential/i.test(key)) url.searchParams.set(key, '***');
        }
        return url.toString();
    } catch { return ''; }
}
export function hasConnectionAddress(config?: ConnectionConfig, urls?: Channel['base_urls']): boolean {
    return config ? config.endpoints.some((e) => !!e.url.trim()) : !!urls?.[0]?.url;
}
export function canFetchConnectionModels(config?: ConnectionConfig, urls?: Channel['base_urls']): boolean {
    return hasConnectionAddress(config, urls) && (!config || config.catalog.format !== 'manual');
}
