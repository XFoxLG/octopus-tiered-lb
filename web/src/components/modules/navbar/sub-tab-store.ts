import { create } from 'zustand';
import { createJSONStorage, persist } from 'zustand/middleware';

/**
 * 子标签顺序 / 可见性 store。
 *
 * 管理 Analytics / Ops 两个模块的子标签排序与隐藏。
 * 持久化方式与 nav-store 一致：localStorage 即时持久化 + DB 设置项跨设备同步
 * （由 app.tsx 在登录后 hydrate）。
 */

export type AnalyticsTab = 'cache' | 'utilization' | 'route-health' | 'channel-model' | 'evaluation' | 'latency';
export type OpsTab = 'telemetry' | 'quota' | 'health' | 'maintenance' | 'system' | 'audit';

export type ModuleId = 'analytics' | 'ops';
export type SubTab = AnalyticsTab | OpsTab;

export const DEFAULT_ANALYTICS_TABS: AnalyticsTab[] = ['cache', 'utilization', 'route-health', 'channel-model', 'evaluation', 'latency'];
export const DEFAULT_OPS_TABS: OpsTab[] = ['telemetry', 'quota', 'health', 'maintenance', 'system', 'audit'];

export const DEFAULT_SUB_TABS: Record<ModuleId, SubTab[]> = {
    analytics: [...DEFAULT_ANALYTICS_TABS],
    ops: [...DEFAULT_OPS_TABS],
};

export const MIN_VISIBLE_SUB_TABS = 1;

/** 各模块的合法子标签表，用于 normalize 过滤非法值。 */
const ALLOWED_TABS: Record<ModuleId, Record<string, boolean>> = {
    analytics: DEFAULT_ANALYTICS_TABS.reduce((acc, t) => { acc[t] = true; return acc; }, {} as Record<string, boolean>),
    ops: DEFAULT_OPS_TABS.reduce((acc, t) => { acc[t] = true; return acc; }, {} as Record<string, boolean>),
};

/** 将输入归一化为合法的有序子标签数组：去重、过滤非法值、补全缺失项。 */
export function normalizeSubTabOrder(module: ModuleId, input: Iterable<string> | null | undefined): SubTab[] {
    const defaults = DEFAULT_SUB_TABS[module];
    const seen = new Set<string>();
    const normalized: SubTab[] = [];

    if (input) {
        for (const item of input) {
            if (typeof item === 'string' && ALLOWED_TABS[module][item] === true && !seen.has(item)) {
                seen.add(item);
                normalized.push(item as SubTab);
            }
        }
    }

    // 补全缺失的默认项
    for (const item of defaults) {
        if (!seen.has(item)) {
            normalized.push(item);
        }
    }

    return normalized;
}

/** 归一化可见列表：只保留在 orderedTabs 中存在的项，并按 orderedTabs 顺序输出，去重。
 *  顺序必须跟随 orderedTabs，否则「拖到首位后默认显示首位」会失效——默认选中项取
 *  visibleTabs[0]，若可见列表顺序滞后于排序，渲染首位与默认选中会不一致。 */
export function normalizeSubTabVisible(module: ModuleId, input: Iterable<string> | null | undefined, orderedTabs: readonly SubTab[]): SubTab[] {
    const requested = new Set<string>();

    if (input) {
        for (const item of input) {
            if (typeof item === 'string') {
                requested.add(item);
            }
        }
    }

    // 按 orderedTabs 顺序过滤，保证可见列表与排序一致（首位即默认显示项）。
    const normalized = orderedTabs.filter((tab) => requested.has(tab));
    if (normalized.length === 0 && orderedTabs.length > 0) {
        normalized.push(orderedTabs[0]);
    }

    return normalized;
}

export function serializeSubTabOrder(module: ModuleId, items: readonly SubTab[]): string {
    return JSON.stringify(normalizeSubTabOrder(module, items));
}

export function serializeSubTabVisible(module: ModuleId, items: readonly SubTab[]): string {
    return JSON.stringify(items);
}

export function parseSubTabOrder(module: ModuleId, value: string | null | undefined): SubTab[] {
    if (!value) return [...DEFAULT_SUB_TABS[module]];
    try {
        const parsed = JSON.parse(value);
        if (!Array.isArray(parsed)) return [...DEFAULT_SUB_TABS[module]];
        return normalizeSubTabOrder(module, parsed);
    } catch {
        return [...DEFAULT_SUB_TABS[module]];
    }
}

export function parseSubTabVisible(module: ModuleId, value: string | null | undefined, orderedTabs?: readonly SubTab[]): SubTab[] {
    const fallbackOrdered = orderedTabs ?? [...DEFAULT_SUB_TABS[module]];
    if (!value) return [...fallbackOrdered];
    try {
        const parsed = JSON.parse(value);
        if (!Array.isArray(parsed)) return [...fallbackOrdered];
        return normalizeSubTabVisible(module, parsed, fallbackOrdered);
    } catch {
        return [...fallbackOrdered];
    }
}

interface SubTabState {
    analytics: { orderedTabs: SubTab[]; visibleTabs: SubTab[] };
    ops: { orderedTabs: SubTab[]; visibleTabs: SubTab[] };
    setOrderedTabs: (module: ModuleId, tabs: SubTab[]) => void;
    setVisibleTabs: (module: ModuleId, tabs: SubTab[]) => void;
    setTabVisible: (module: ModuleId, tab: SubTab, visible: boolean) => void;
    resetModule: (module: ModuleId) => void;
    resetAll: () => void;
}

export const useSubTabStore = create<SubTabState>()(
    persist(
        (set) => ({
            analytics: { orderedTabs: [...DEFAULT_ANALYTICS_TABS], visibleTabs: [...DEFAULT_ANALYTICS_TABS] },
            ops: { orderedTabs: [...DEFAULT_OPS_TABS], visibleTabs: [...DEFAULT_OPS_TABS] },
            setOrderedTabs: (module, tabs) => {
                set((state) => {
                    const orderedTabs = normalizeSubTabOrder(module, tabs);
                    const visibleTabs = normalizeSubTabVisible(module, state[module].visibleTabs, orderedTabs);
                    return { [module]: { orderedTabs, visibleTabs } };
                });
            },
            setVisibleTabs: (module, tabs) => {
                set((state) => {
                    const visibleTabs = normalizeSubTabVisible(module, tabs, state[module].orderedTabs);
                    return { [module]: { ...state[module], visibleTabs } };
                });
            },
            setTabVisible: (module, tab, visible) => {
                set((state) => {
                    const current = state[module];
                    const isVisible = current.visibleTabs.includes(tab);
                    if (visible === isVisible) return state;
                    if (!visible && current.visibleTabs.length <= MIN_VISIBLE_SUB_TABS) return state;

                    const nextVisible = visible
                        ? Array.from(new Set([...current.visibleTabs, tab]))
                        : current.visibleTabs.filter((t) => t !== tab);
                    const visibleTabs = normalizeSubTabVisible(module, nextVisible, current.orderedTabs);
                    return { [module]: { ...current, visibleTabs } };
                });
            },
            resetModule: (module) => {
                set({ [module]: { orderedTabs: [...DEFAULT_SUB_TABS[module]], visibleTabs: [...DEFAULT_SUB_TABS[module]] } });
            },
            resetAll: () => {
                set({
                    analytics: { orderedTabs: [...DEFAULT_ANALYTICS_TABS], visibleTabs: [...DEFAULT_ANALYTICS_TABS] },
                    ops: { orderedTabs: [...DEFAULT_OPS_TABS], visibleTabs: [...DEFAULT_OPS_TABS] },
                });
            },
        }),
        {
            name: 'sub-tab-storage',
            storage: createJSONStorage(() => localStorage),
            merge: (persistedState, currentState) => {
                const typed = (persistedState as Partial<SubTabState> | null) ?? null;
                const result = { ...currentState, ...typed };
                for (const modId of ['analytics', 'ops'] as ModuleId[]) {
                    const stored = (typed as Record<ModuleId, { orderedTabs?: SubTab[]; visibleTabs?: SubTab[] } | undefined> | null)?.[modId];
                    const orderedTabs = normalizeSubTabOrder(modId, stored?.orderedTabs);
                    const visibleTabs = normalizeSubTabVisible(modId, stored?.visibleTabs, orderedTabs);
                    result[modId] = { orderedTabs, visibleTabs };
                }
                return result;
            },
        }
    )
);
