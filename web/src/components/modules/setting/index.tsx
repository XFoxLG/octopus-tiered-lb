'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import {
    Sun, User, Database,
    ScrollText, Monitor, RefreshCw, ChevronsUpDown,
    Info, Bot, Sparkles, Cloud, Fingerprint, Wand2, Layers, Network,
} from 'lucide-react';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { SettingAppearance } from './Appearance';
import { SettingAccount } from './Account';
import { SettingBackup } from './Backup';
import { SettingCache } from './Cache';
import { SettingSystem } from './System';
import { SettingInfo } from './Info';
import { SettingLLMSync } from './LLMSync';
import { SettingLog } from './Log';
import { SettingAutoStrategy } from './AutoStrategy';
import { SettingAIRoute } from './AIRoute';
import { SettingSemanticCache } from './SemanticCache';
import { SettingWebDAV } from './WebDAV';
import { SettingWebAuthn } from './WebAuthn';
import { SettingNormalize } from './Normalize';
import { useProxyPoolDialogStore } from '@/components/modules/proxy-pool/dialog-store';
import { DEFAULT_SETTING_ORDER } from './SettingOrder';

type SettingItemDef = {
    id: string;
    icon: React.ReactNode;
    titleKey: string;
    component: React.ReactNode;
};

const SETTING_ITEM_DEFS: SettingItemDef[] = [
    { id: 'info',              icon: <Info className="h-5 w-5" />,              titleKey: 'info.title',           component: <SettingInfo /> },
    { id: 'appearance',        icon: <Sun className="h-5 w-5" />,              titleKey: 'appearance',           component: <SettingAppearance /> },
    { id: 'ai-route',          icon: <Bot className="h-5 w-5" />,              titleKey: 'aiRoute.title',        component: <SettingAIRoute /> },
    { id: 'auto-strategy',     icon: <Sparkles className="h-5 w-5" />,         titleKey: 'autoStrategy.title',   component: <SettingAutoStrategy /> },
    { id: 'account',           icon: <User className="h-5 w-5" />,              titleKey: 'account.title',         component: <SettingAccount /> },
    { id: 'semantic-cache',    icon: <Database className="h-5 w-5" />,          titleKey: 'semanticCache.title',  component: <SettingSemanticCache /> },
    { id: 'log',               icon: <ScrollText className="h-5 w-5" />,        titleKey: 'log.title',           component: <SettingLog /> },
    { id: 'system',            icon: <Monitor className="h-5 w-5" />,           titleKey: 'system',               component: <SettingSystem /> },
    { id: 'llmsync',           icon: <RefreshCw className="h-5 w-5" />,        titleKey: 'llmSync.title',        component: <SettingLLMSync /> },
    { id: 'backup',            icon: <Database className="h-5 w-5" />,          titleKey: 'backup.title',         component: <SettingBackup /> },
    { id: 'redis',              icon: <Database className="h-5 w-5" />,          titleKey: 'redis.title',          component: <SettingCache /> },
    { id: 'webdav',            icon: <Cloud className="h-5 w-5" />,             titleKey: 'webdav.title',         component: <SettingWebDAV /> },
    { id: 'webauthn',          icon: <Fingerprint className="h-5 w-5" />,      titleKey: 'webauthn.title',       component: <SettingWebAuthn /> },
    { id: 'normalize',         icon: <Wand2 className="h-5 w-5" />,           titleKey: 'normalize.title',      component: <SettingNormalize /> },
    { id: 'proxy-pool',        icon: <Network className="h-5 w-5" />,          titleKey: 'proxyPool.title',      component: null },
];

const SETTING_ITEM_MAP = new Map<string, SettingItemDef>(
    SETTING_ITEM_DEFS.map((def) => [def.id, def])
);

function getOrderedItems(order: string[]): SettingItemDef[] {
    const seen = new Set<string>();
    const result: SettingItemDef[] = [];
    for (const id of order) {
        const def = SETTING_ITEM_MAP.get(id);
        if (def && !seen.has(id)) {
            seen.add(id);
            result.push(def);
        }
    }
    // append any missing defaults
    for (const def of SETTING_ITEM_DEFS) {
        if (!seen.has(def.id)) {
            result.push(def);
        }
    }
    return result;
}

function loadOrder(): string[] {
    try {
        const raw = localStorage.getItem('octopus-setting-order');
        if (!raw) return [...DEFAULT_SETTING_ORDER];
        const parsed = JSON.parse(raw);
        if (!Array.isArray(parsed)) return [...DEFAULT_SETTING_ORDER];
        const filtered = parsed.filter((id: unknown) =>
            typeof id === 'string' && SETTING_ITEM_MAP.has(id)
        );
        const missing = DEFAULT_SETTING_ORDER.filter((id) => !filtered.includes(id));
        return [...filtered, ...missing];
    } catch {
        return [...DEFAULT_SETTING_ORDER];
    }
}

export function Setting() {
    const t = useTranslations('setting');
    const [openId, setOpenId] = useState<string | null>(null);
    const items = getOrderedItems(loadOrder());
    const activeItem = items.find((item) => item.id === openId);

    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-xl">
            <div className="pb-3 md:pb-6 px-4 md:px-6 pt-4">
                <div className="space-y-2 max-w-2xl mx-auto">
                    {items.map((item) => (
                        <button
                            key={item.id}
                            type="button"
                            onClick={() => {
                                if (item.id === 'proxy-pool') {
                                    useProxyPoolDialogStore.getState().open();
                                    return;
                                }
                                setOpenId(item.id);
                            }}
                            className="w-full flex items-center gap-3 rounded-xl border border-border/35 bg-card px-4 py-3.5 min-h-[3.25rem] text-left shadow-sm transition-colors hover:bg-accent/40 active:bg-accent/60"
                        >
                            <span className="shrink-0 text-muted-foreground">{item.icon}</span>
                            <span className="flex-1 text-sm font-semibold text-card-foreground truncate">
                                {t(item.titleKey)}
                            </span>
                            <ChevronsUpDown className="h-4 w-4 shrink-0 text-muted-foreground" />
                        </button>
                    ))}
                </div>
            </div>

            <Dialog open={openId !== null} onOpenChange={(open) => { if (!open) setOpenId(null); }}>
                <DialogContent aria-describedby={undefined} className="w-[100vw] sm:w-[min(95vw,720px)] lg:w-[min(95vw,1040px)] sm:max-w-[min(95vw,720px)] lg:max-w-[min(95vw,1040px)] max-h-[100dvh] sm:max-h-[90vh] overflow-y-auto p-0 gap-0 rounded-none sm:rounded-2xl">
                    <DialogTitle className="sr-only">{activeItem ? t(activeItem.titleKey) : ''}</DialogTitle>
                    {activeItem && activeItem.component}
                </DialogContent>
            </Dialog>
        </div>
    );
}
