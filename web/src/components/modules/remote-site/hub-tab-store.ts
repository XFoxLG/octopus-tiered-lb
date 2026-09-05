import { create } from 'zustand';

export type HubTab = 'sites' | 'site-channels' | 'automation';

interface HubTabState {
    activeTab: HubTab;
    setActiveTab: (tab: HubTab) => void;
}

export const useHubTabStore = create<HubTabState>((set) => ({
    activeTab: 'sites',
    setActiveTab: (tab) => set({ activeTab: tab }),
}));
