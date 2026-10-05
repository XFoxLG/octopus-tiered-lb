import { create } from 'zustand';
import type { ModelCapabilityFilter } from './filters';

export type ModelView = 'market' | 'endpoints' | 'categories';

interface ModelViewState {
  modelView: ModelView;
  capability: ModelCapabilityFilter;
  provider: string;
  dedupe: boolean;
  dedupeInitialized: boolean;
  setModelView: (view: ModelView) => void;
  setCapability: (capability: ModelCapabilityFilter) => void;
  setProvider: (provider: string) => void;
  setDedupe: (dedupe: boolean) => void;
  initializeDedupe: (dedupe: boolean) => void;
  resetFilters: () => void;
}

export const useModelViewStore = create<ModelViewState>((set) => ({
  modelView: 'market',
  capability: 'all',
  provider: '',
  dedupe: false,
  dedupeInitialized: false,
  setModelView: (modelView) => set({ modelView }),
  setCapability: (capability) => set({ capability }),
  setProvider: (provider) => set({ provider }),
  setDedupe: (dedupe) => set({ dedupe, dedupeInitialized: true }),
  initializeDedupe: (dedupe) => set((state) => state.dedupeInitialized ? state : { dedupe, dedupeInitialized: true }),
  resetFilters: () => set({ capability: 'all', provider: '', dedupe: false, dedupeInitialized: true }),
}));
