import type { Channel, ChannelEndpoint, ConnectionConfig } from '@/api/endpoints/channel';

export function newEndpoint(): ChannelEndpoint {
    return { id: crypto.randomUUID(), protocol: 'chat', url: '', url_mode: 'base', auth: 'default' };
}
export function newConnectionConfig(): ConnectionConfig {
    const endpoint = newEndpoint();
    return { version: 1, selection: 'same_protocol', endpoints: [endpoint], catalog: { format: 'openai', endpoint_id: endpoint.id } };
}

export function normalizeConnectionConfig(config: ConnectionConfig): ConnectionConfig {
    return {
        ...config,
        version: config.endpoints.some((endpoint) => endpoint.transport === 'catie_tasks') ? 2 : 1,
    };
}

export function canUseTaskTransport(endpoint: ChannelEndpoint): boolean {
    return endpoint.protocol === 'chat' && (!endpoint.forward_mode || endpoint.forward_mode === 'convert')
        && !endpoint.compatibility && ['default', 'bearer'].includes(endpoint.auth);
}

export function taskURLProblem(endpoint: ChannelEndpoint): 'required' | 'invalid' | 'origin' | 'query' | undefined {
    if (!endpoint.task_api_url?.trim()) return 'required';
    try {
        const task = new URL(endpoint.task_api_url);
        if (!['http:', 'https:'].includes(task.protocol) || task.username || task.password || task.hash) return 'invalid';
        if (task.search || endpoint.task_api_url.endsWith('?')) return 'query';
        if (task.origin !== new URL(endpoint.url).origin) return 'origin';
    } catch { return 'invalid'; }
    return undefined;
}

function safePreview(url: URL): string {
    url.username = ''; url.password = '';
    for (const key of Array.from(url.searchParams.keys())) {
        if (/key|token|secret|signature|credential/i.test(key)) url.searchParams.set(key, '***');
    }
    return url.toString();
}

const CATALOG_FORMAT_BY_PROTOCOL: Partial<Record<ChannelEndpoint['protocol'], ConnectionConfig['catalog']['format']>> = {
    chat: 'openai',
    responses: 'openai',
    embeddings: 'openai',
    rerank: 'openai',
    messages: 'anthropic',
    gemini: 'gemini',
    cloudflare: 'cloudflare',
};

export function catalogFormatForEndpointProtocol(protocol: ChannelEndpoint['protocol']): ConnectionConfig['catalog']['format'] | undefined {
    return CATALOG_FORMAT_BY_PROTOCOL[protocol];
}

/**
 * 目录格式与生成接口协议是独立设置；只有当前格式确实是“旧接口协议的默认格式”时，
 * 切换来源接口才自动跟随，避免覆盖用户特意选好的解析器。
 */
export function autoCatalogFormatOnEndpointChange(
    previousProtocol: ChannelEndpoint['protocol'],
    nextProtocol: ChannelEndpoint['protocol'],
    currentFormat: ConnectionConfig['catalog']['format'],
): ConnectionConfig['catalog']['format'] {
    const nextFormat = catalogFormatForEndpointProtocol(nextProtocol);
    const previousFormat = catalogFormatForEndpointProtocol(previousProtocol);
    if (!nextFormat || currentFormat !== previousFormat) return currentFormat;
    return nextFormat;
}
export function connectionURLPreview(endpoint: ChannelEndpoint, model = 'MODEL', stream = false): string {
    try {
        if (endpoint.transport === 'catie_tasks') {
            return endpoint.task_api_url?.trim() ? safePreview(new URL(endpoint.task_api_url)) : '';
        }
        const url = new URL(endpoint.url);
        if (!['https:', 'http:'].includes(url.protocol)) return '';
        if (endpoint.protocol === 'cloudflare' && !model.startsWith('@')) model = '@cf/' + model;
        if (endpoint.url_mode === 'full') {
            url.pathname = decodeURI(url.pathname).replaceAll('{model}', model.replace(/^models\//, ''));
        } else {
            const root = url.pathname.replace(/\/+$/, '') || (endpoint.protocol === 'gemini' ? '/v1beta' : endpoint.protocol === 'cloudflare' ? '' : '/v1');
            const suffix = { chat: '/chat/completions', responses: '/responses', messages: '/messages', embeddings: '/embeddings', rerank: '/rerank', gemini: `/models/${model.replace(/^models\//, '')}:generateContent`, cloudflare: `/ai/run/${model}`, volcengine: '/responses', codex: '/responses' }[endpoint.protocol];
            url.pathname = root + suffix;
        }
        if (endpoint.protocol === 'gemini' && stream) {
            url.pathname = url.pathname.replace(/:(streamGenerateContent|generateContent)$/, '') + ':streamGenerateContent';
            url.searchParams.set('alt', 'sse');
        }
        // Do not display query credentials in the preview.
        return safePreview(url);
    } catch { return ''; }
}

export function taskTransportPreviews(endpoint: ChannelEndpoint): { submit: string; query: string; cancel: string } | null {
    if (endpoint.transport !== 'catie_tasks' || !endpoint.task_api_url?.trim()) return null;
    try {
        const url = new URL(endpoint.task_api_url);
        if (!['http:', 'https:'].includes(url.protocol)) return null;
        const submit = safePreview(url).replace(/\/+$/, '');
        url.search = ''; url.hash = '';
        const root = safePreview(url).replace(/\/+$/, '');
        return { submit, query: `${root}/{task_id}?after={cursor}`, cancel: `${root}/{task_id}/cancel` };
    } catch { return null; }
}

export function hasConnectionAddress(config?: ConnectionConfig, urls?: Channel['base_urls']): boolean {
    return config ? config.endpoints.some((e) => !!e.url.trim()) : !!urls?.[0]?.url;
}
export function canFetchConnectionModels(config?: ConnectionConfig, urls?: Channel['base_urls']): boolean {
    return hasConnectionAddress(config, urls) && (!config || config.catalog.format !== 'manual');
}
