import { create } from 'zustand';

export type ModelView = 'market' | 'endpoints' | 'categories';

interface ModelViewState {
    modelView: ModelView;
    setModelView: (view: ModelView) => void;
}

/**
 * Shared store for the model module's market/endpoints view toggle.
 *
 * The toggle is rendered in the global Toolbar (title bar), so the active
 * view must live outside the Model page component itself.
 */
export const useModelViewStore = create<ModelViewState>((set) => ({
    modelView: 'market',
    setModelView: (view) => set({ modelView: view }),
}));
