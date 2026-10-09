import type { GroupTestResult } from '@/api/endpoints/group';
import type { SelectedMember } from './ItemList';

export type AvailabilityRowStatus = 'pending' | 'testing' | 'passed' | 'failed';

export type AvailabilityRow = {
    id: string;
    modelName: string;
    channelName: string;
    status: AvailabilityRowStatus;
    result?: GroupTestResult;
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

    return members.map((member) => {
        const result = byClientId.get(member.id) ?? byModelChannel.get(`${member.channel_id}:${member.name}`);
        return {
            id: member.id,
            modelName: member.name,
            channelName: member.channel_name,
            status: result ? (result.passed ? 'passed' : 'failed') : (isTesting ? 'testing' : 'pending'),
            result,
        };
    });
}
