import type { ChannelProbeResult, EndpointProtocol } from '@/api/endpoints/channel';

export const TEST_PROTOCOL_NAMES: Record<EndpointProtocol, string> = {
    chat: 'Chat Completions', responses: 'Responses API', messages: 'Anthropic Messages',
    gemini: 'Gemini generateContent', embeddings: 'Embeddings', cloudflare: 'Cloudflare Workers AI',
    volcengine: 'Volcengine Responses', codex: 'Codex Responses',
};

export type ProbeResultGroup = { endpointId?: string; protocol?: EndpointProtocol; rows: ChannelProbeResult[]; catalog: boolean };

/** The verification row is historical evidence; current endpoint order is not protocol identity. */
export function groupProbeResults(rows: ChannelProbeResult[]): ProbeResultGroup[] {
    const groups = new Map<string, ProbeResultGroup>();
    for (const row of rows) {
        const catalog = row.item === 'models';
        const verifiedProtocol = row.kind === 'protocol' && row.item.startsWith('protocol_')
            ? row.item.slice('protocol_'.length) as EndpointProtocol : undefined;
        const protocol = row.protocol ?? verifiedProtocol;
        const key = catalog ? 'catalog' : row.endpoint_id ? 'endpoint:' + row.endpoint_id
            : protocol ? 'protocol:' + protocol : 'unrecorded';
        const group = groups.get(key) ?? { endpointId: row.endpoint_id, protocol, rows: [], catalog };
        if (row.protocol || verifiedProtocol) group.protocol = protocol;
        else if (!group.protocol && protocol) group.protocol = protocol;
        group.rows.push(row);
        groups.set(key, group);
    }
    return Array.from(groups.values());
}

export function formatProbeDetail(detail: string): string {
    try { return JSON.stringify(JSON.parse(detail), null, 2); } catch { return detail; }
}
