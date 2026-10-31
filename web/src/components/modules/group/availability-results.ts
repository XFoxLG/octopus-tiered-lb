import type { GroupTestResult } from '@/api/endpoints/group';
import type { SelectedMember } from './ItemList';

export type AvailabilityRowStatus = 'pending' | 'testing' | 'passed' | 'failed';

export type AvailabilityRow = {
    id: string;
    modelName: string;
    channelName: string;
    status: AvailabilityRowStatus;
    result?: GroupTestResult;
    historical?: boolean;
};

export function buildAvailabilityRows(
    members: SelectedMember[],
    results: GroupTestResult[],
    isTesting: boolean,
): AvailabilityRow[] {
    const byClientId = new Map<string, GroupTestResult>();
    const byModelChannel = new Map<string, GroupTestResult>();
    results.forEach((result) => {
        if (result.client_id) byClientId.set(result.client_id, result);
        byModelChannel.set(`${result.channel_id}:${result.model_name}`, result);
    });

    const matched = new Set<GroupTestResult>();
    const rows: AvailabilityRow[] = members.map((member) => {
        const result = byClientId.get(member.id) ?? byModelChannel.get(`${member.channel_id}:${member.name}`);
        if (result) matched.add(result);
        return {
            id: member.id,
            modelName: member.name,
            channelName: member.channel_name,
            status: result ? (result.passed ? 'passed' : 'failed') : (isTesting ? 'testing' : 'pending'),
            result,
        };
    });
    results.forEach((result, index) => {
        if (matched.has(result)) return;
        rows.push({
            id: 'result:' + (result.client_id ?? result.item_id) + ':' + index,
            modelName: result.model_name,
            channelName: result.channel_name,
            status: result.passed ? 'passed' : 'failed',
            result,
            historical: true,
        });
    });
    return rows;
}
