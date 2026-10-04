// AI 路由服务池的纯函数层：结构化行 <-> 后端 JSON（ai_route_services setting）。
// 对齐 internal/model/ai_route_service.go 的 AIRouteServiceConfig 字段：
// name/base_url/api_key/model + enabled（可选布尔，省略 = true）。

export interface AIRouteServiceRow {
    name: string;
    baseUrl: string;
    apiKey: string;
    model: string;
    enabled: boolean;
}

export function parseServicesJSON(raw: string): { rows: AIRouteServiceRow[] | null; invalid: boolean } {
    const trimmed = raw.trim();
    if (trimmed === '' || trimmed === '[]') {
        return { rows: [], invalid: false };
    }
    try {
        const parsed: unknown = JSON.parse(trimmed);
        if (!Array.isArray(parsed)) {
            return { rows: null, invalid: true };
        }
        const rows: AIRouteServiceRow[] = parsed.map((item) => {
            const record = (typeof item === 'object' && item !== null ? item : {}) as Record<string, unknown>;
            return {
                name: typeof record.name === 'string' ? record.name : '',
                baseUrl: typeof record.base_url === 'string' ? record.base_url : '',
                apiKey: typeof record.api_key === 'string' ? record.api_key : '',
                model: typeof record.model === 'string' ? record.model : '',
                enabled: record.enabled === undefined ? true : Boolean(record.enabled),
            };
        });
        return { rows, invalid: false };
    } catch {
        return { rows: null, invalid: true };
    }
}

export function serializeServicesRows(rows: AIRouteServiceRow[]): string {
    if (rows.length === 0) return '[]';
    return JSON.stringify(
        rows.map((row) => ({
            name: row.name.trim() || undefined,
            base_url: row.baseUrl.trim(),
            api_key: row.apiKey.trim(),
            model: row.model.trim(),
            enabled: row.enabled,
        })),
    );
}
