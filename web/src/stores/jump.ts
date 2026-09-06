import { create } from 'zustand';
import { useNavStore, type NavItem } from '@/components/modules/navbar';

export type ChannelJumpTarget = { kind: 'channel-card'; channelId: number };

export type JumpTarget = ChannelJumpTarget;

export type PendingJump = {
    requestId: number;
    target: JumpTarget;
};

interface JumpState {
    sequence: number;
    pending: PendingJump | null;
    requestJump: (target: JumpTarget) => void;
    clearPending: (requestId?: number) => void;
}

export function getJumpTargetRoute(target: JumpTarget): NavItem {
    switch (target.kind) {
        case 'channel-card':
            return 'channel';
        default:
            return 'home';
    }
}

export function isChannelJumpTarget(target: JumpTarget): target is ChannelJumpTarget {
    return target.kind === 'channel-card';
}

export const useJumpStore = create<JumpState>((set, get) => ({
    sequence: 0,
    pending: null,
    requestJump: (target) => {
        const route = getJumpTargetRoute(target);
        const navState = useNavStore.getState();
        if (navState.activeItem !== route) {
            navState.setActiveItem(route);
        }

        const nextSequence = get().sequence + 1;
        set({
            sequence: nextSequence,
            pending: {
                requestId: nextSequence,
                target,
            },
        });
    },
    clearPending: (requestId) =>
        set((state) => {
            if (!state.pending) return state;
            if (typeof requestId === 'number' && state.pending.requestId !== requestId) {
                return state;
            }

            return { ...state, pending: null };
        }),
}));
