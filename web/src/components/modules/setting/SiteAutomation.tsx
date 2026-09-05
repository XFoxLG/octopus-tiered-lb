'use client';

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { CalendarCheck2, Clock3, Globe2, RefreshCw, Layers } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { useSettingList, useSetSetting, SettingKey } from '@/api/endpoints/setting';
import { useCheckinAllSites, useSyncAllSites } from '@/api/endpoints/site';
import { toast } from '@/components/common/Toast';
import { useSettingStore } from '@/stores/setting';
import { translateSiteMessage } from '@/components/modules/site/site-message';

enum AutoGroupType {
    None = 0,
    Fuzzy = 1,
    Exact = 2,
    Regex = 3,
}

function getErrorMessage(error: unknown, fallback: string) {
    if (error instanceof Error && error.message.trim()) {
        return error.message;
    }
    if (error && typeof error === 'object' && 'message' in error) {
        const message = (error as { message?: unknown }).message;
        if (typeof message === 'string' && message.trim()) {
            return message;
        }
    }
    return fallback;
}

export function SettingSiteAutomation() {
    const t = useTranslations('setting');
    const locale = useSettingStore((state) => state.locale);
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const syncAllSites = useSyncAllSites();
    const checkinAllSites = useCheckinAllSites();

    const [syncInterval, setSyncInterval] = useState('');
    const [checkinInterval, setCheckinInterval] = useState('');
    const [autoGroupMode, setAutoGroupMode] = useState(String(AutoGroupType.None));
    const initialSyncInterval = useRef('');
    const initialCheckinInterval = useRef('');
    const initialAutoGroupMode = useRef(String(AutoGroupType.None));

    useEffect(() => {
        if (!settings) return;

        const siteSync = settings.find((item) => item.key === SettingKey.SiteSyncInterval);
        const siteCheckin = settings.find((item) => item.key === SettingKey.SiteCheckinInterval);
        const autoGroupSetting = settings.find((item) => item.key === SettingKey.ProjectedChannelAutoGroupEnabled);

        if (siteSync) {
            queueMicrotask(() => setSyncInterval(siteSync.value));
            initialSyncInterval.current = siteSync.value;
        }
        if (siteCheckin) {
            queueMicrotask(() => setCheckinInterval(siteCheckin.value));
            initialCheckinInterval.current = siteCheckin.value;
        }
        if (autoGroupSetting) {
            // The value can be "0"/"1"/"2"/"3", or "true"/"false"/""
            const val = autoGroupSetting.value;
            let modeValue: string;
            if (val === 'true') {
                modeValue = String(AutoGroupType.Fuzzy);
            } else if (val === 'false' || val === '') {
                modeValue = String(AutoGroupType.None);
            } else {
                modeValue = val;
            }
            queueMicrotask(() => setAutoGroupMode(modeValue));
            initialAutoGroupMode.current = modeValue;
        }
    }, [settings]);

    function handleSave(key: string, value: string, initialValue: string, onSaved: (next: string) => void, onError?: () => void) {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                onSaved(value);
                toast.success(t('saved'));
            },
            onError: (error) => {
                onError?.();
                toast.error(translateSiteMessage(locale, getErrorMessage(error, t('siteAutomation.saveFailed')), t));
            },
        });
    }

    function handleManualSync() {
        syncAllSites.mutate(undefined, {
            onSuccess: () => {
                toast.success(t('siteAutomation.syncTriggered'));
            },
            onError: (error) => {
                toast.error(translateSiteMessage(locale, getErrorMessage(error, t('siteAutomation.syncFailed')), t));
            },
        });
    }

    function handleManualCheckin() {
        checkinAllSites.mutate(undefined, {
            onSuccess: () => {
                toast.success(t('siteAutomation.checkinTriggered'));
            },
            onError: (error) => {
                toast.error(translateSiteMessage(locale, getErrorMessage(error, t('siteAutomation.checkinFailed')), t));
            },
        });
    }

    return (
        <div className="rounded-xl border-border/35 bg-card p-6 space-y-5 text-card-foreground shadow-md">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Globe2 className="h-5 w-5" />
                {t('siteAutomation.title')}
            </h2>

            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Layers className="h-5 w-5 text-muted-foreground" />
                    <div className="flex flex-col">
                        <span className="text-sm font-medium">{t('siteAutomation.autoGroupMode.label')}</span>
                        <span className="text-xs text-muted-foreground">{t('siteAutomation.autoGroupMode.description')}</span>
                    </div>
                </div>
                <Select
                    value={autoGroupMode}
                    onValueChange={(value) => {
                        setAutoGroupMode(value);
                        handleSave(SettingKey.ProjectedChannelAutoGroupEnabled, value, initialAutoGroupMode.current, (next) => {
                            initialAutoGroupMode.current = next;
                        });
                    }}
                >
                    <SelectTrigger className="w-48 rounded-xl">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={String(AutoGroupType.None)}>{t('siteAutomation.autoGroupMode.none')}</SelectItem>
                        <SelectItem value={String(AutoGroupType.Fuzzy)}>{t('siteAutomation.autoGroupMode.fuzzy')}</SelectItem>
                        <SelectItem value={String(AutoGroupType.Exact)}>{t('siteAutomation.autoGroupMode.exact')}</SelectItem>
                        <SelectItem value={String(AutoGroupType.Regex)}>{t('siteAutomation.autoGroupMode.regex')}</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Clock3 className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('siteAutomation.syncInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={syncInterval}
                    onChange={(event) => setSyncInterval(event.target.value)}
                    onBlur={() => handleSave(SettingKey.SiteSyncInterval, syncInterval, initialSyncInterval.current, (next) => {
                        initialSyncInterval.current = next;
                    })}
                    placeholder={t('siteAutomation.syncInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Clock3 className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('siteAutomation.checkinInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={checkinInterval}
                    onChange={(event) => setCheckinInterval(event.target.value)}
                    onBlur={() => handleSave(SettingKey.SiteCheckinInterval, checkinInterval, initialCheckinInterval.current, (next) => {
                        initialCheckinInterval.current = next;
                    })}
                    placeholder={t('siteAutomation.checkinInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <RefreshCw className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('siteAutomation.manualSync.label')}</span>
                </div>
                <Button variant="outline" size="sm" onClick={handleManualSync} disabled={syncAllSites.isPending} className="rounded-xl">
                    {syncAllSites.isPending ? t('siteAutomation.manualSync.syncing') : t('siteAutomation.manualSync.button')}
                </Button>
            </div>

            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <CalendarCheck2 className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('siteAutomation.manualCheckin.label')}</span>
                </div>
                <Button variant="outline" size="sm" onClick={handleManualCheckin} disabled={checkinAllSites.isPending} className="rounded-xl">
                    {checkinAllSites.isPending ? t('siteAutomation.manualCheckin.checking') : t('siteAutomation.manualCheckin.button')}
                </Button>
            </div>
        </div>
    );
}
