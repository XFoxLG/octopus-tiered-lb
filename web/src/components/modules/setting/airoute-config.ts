export interface AIRouteConfiguration {
    groupID: string;
    baseURL: string;
    apiKey: string;
    model: string;
    timeoutSeconds: string;
    parallelism: string;
    servicesJSON: string;
}

interface AIRouteGroup {
    name: string;
    endpoint_type?: string;
    items?: unknown[];
}

interface OctopusPresetOptions {
    publicBaseURL: string;
    consoleAPIBaseURL: string;
    browserOrigin: string;
    modelAlias: string;
}

export function isAIRouteHTTPURL(value: string): boolean {
    try {
        if (!/^https?:\/\//i.test(value.trim())) return false;
        const parsed = new URL(value.trim());
        return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && Boolean(parsed.hostname);
    } catch {
        return false;
    }
}

export function buildOctopusAIRoutePreset({
    publicBaseURL,
    consoleAPIBaseURL,
    browserOrigin,
    modelAlias,
}: OctopusPresetOptions): Pick<AIRouteConfiguration, 'baseURL' | 'apiKey' | 'model'> | null {
    const configuredBaseURL = publicBaseURL.trim() || consoleAPIBaseURL.trim() || '.';
    try {
        // An explicit public address takes precedence over the console's backend/origin.
        if (publicBaseURL.trim() && !isAIRouteHTTPURL(publicBaseURL)) return null;
        const parsed = new URL(configuredBaseURL, `${browserOrigin}/`);
        if (!isAIRouteHTTPURL(parsed.href) || parsed.username || parsed.password || parsed.search || parsed.hash) {
            return null;
        }
        const basePath = parsed.pathname.replace(/\/+$/, '');
        parsed.pathname = basePath.endsWith('/v1') ? basePath : `${basePath}/v1`;
        return {
            baseURL: parsed.href,
            model: modelAlias,
            // Never carry an external provider's secret over to a different API.
            apiKey: '',
        };
    } catch {
        return null;
    }
}

export function getAIRouteModelAliases(groups: AIRouteGroup[]): string[] {
    const conversationEndpoints = new Set(['chat', 'deepseek', 'mimo', 'responses', 'messages']);
    return [...new Set(groups
        .filter((group) => conversationEndpoints.has(group.endpoint_type?.trim().toLowerCase() || 'chat'))
        .filter((group) => group.items && group.items.length > 0)
        .map((group) => group.name.trim())
        .filter(Boolean))]
        .sort((left, right) => left.localeCompare(right));
}

export function inspectAIRouteServices(raw: string): {
    normalizedJSON: string;
    usesServicePool: boolean;
    valid: boolean;
} {
    const normalizedJSON = raw.trim() || '[]';
    try {
        const services: unknown = JSON.parse(normalizedJSON);
        if (!Array.isArray(services)) {
            return { normalizedJSON, usesServicePool: true, valid: false };
        }
        if (services.length === 0) {
            // The backend uses the exact string "[]" to select the single service.
            return { normalizedJSON: '[]', usesServicePool: false, valid: true };
        }
        const validServices = services.every((service: unknown) => {
            if (!service || typeof service !== 'object') return false;
            const entry = service as Record<string, unknown>;
            return typeof entry.base_url === 'string' && isAIRouteHTTPURL(entry.base_url)
                && typeof entry.api_key === 'string' && Boolean(entry.api_key.trim())
                && typeof entry.model === 'string' && Boolean(entry.model.trim())
                && (entry.enabled == null || typeof entry.enabled === 'boolean')
                && (entry.name == null || typeof entry.name === 'string');
        });
        const hasEnabledService = validServices && services.some((service) => service.enabled !== false);
        return { normalizedJSON, usesServicePool: true, valid: validServices && hasEnabledService };
    } catch {
        return { normalizedJSON, usesServicePool: true, valid: false };
    }
}

export function getAIRouteConfigurationIssues(configuration: AIRouteConfiguration): (keyof AIRouteConfiguration)[] {
    const issues: (keyof AIRouteConfiguration)[] = [];
    const services = inspectAIRouteServices(configuration.servicesJSON);
    if (!services.valid) issues.push('servicesJSON');
    if (!services.usesServicePool) {
        if (!isAIRouteHTTPURL(configuration.baseURL)) issues.push('baseURL');
        if (!configuration.apiKey.trim()) issues.push('apiKey');
        if (!configuration.model.trim()) issues.push('model');
    }
    for (const field of ['timeoutSeconds', 'parallelism'] as const) {
        const value = configuration[field].trim();
        if (!/^\d+$/.test(value) || !Number.isSafeInteger(Number(value)) || Number(value) < 1) {
            issues.push(field);
        }
    }
    return issues;
}
