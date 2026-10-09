export interface ReachEndTriggerInput {
    enabled: boolean;
    itemRowCount: number;
    lastVirtualIndex: number;
    reachEndOffset: number;
    lastItemKey: string | number;
    triggeredKey: string | number | null;
}

export interface ReachEndTriggerResult {
    shouldTrigger: boolean;
    nextTriggeredKey: string | number | null;
}

export function resolveReachEndTrigger({
    enabled,
    itemRowCount,
    lastVirtualIndex,
    reachEndOffset,
    lastItemKey,
    triggeredKey,
}: ReachEndTriggerInput): ReachEndTriggerResult {
    if (!enabled || itemRowCount === 0) {
        return { shouldTrigger: false, nextTriggeredKey: null };
    }

    const triggerIndex = Math.max(0, itemRowCount - 1 - reachEndOffset);
    if (lastVirtualIndex < triggerIndex) {
        return { shouldTrigger: false, nextTriggeredKey: null };
    }
    if (triggeredKey === lastItemKey) {
        return { shouldTrigger: false, nextTriggeredKey: triggeredKey };
    }
    return { shouldTrigger: true, nextTriggeredKey: lastItemKey };
}
