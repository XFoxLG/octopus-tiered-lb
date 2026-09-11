'use client';

import { useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { ShieldAlert, ListFilter, MessageSquareWarning, X as RemoveIcon } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { SettingKey, useSetSetting, useSettingList } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';

const defaultSettingValues: Record<string, string> = {
    [SettingKey.RequestFilterEnabled]: 'false',
    [SettingKey.RequestFilterKeywords]: '[]',
    [SettingKey.RequestFilterErrorMessage]: 'The request contains blocked keywords and was not sent upstream.',
};

function parseKeywords(value: string): string[] {
    try {
        const storedKeywords: unknown = JSON.parse(value);
        return Array.isArray(storedKeywords) && storedKeywords.every((keyword) => typeof keyword === 'string')
            ? storedKeywords : [];
    } catch {
        return [];
    }
}

export function SettingRequestFilter() {
    const translate = useTranslations('setting');
    const fieldId = useId();
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [enabled, setEnabled] = useState(false);
    const [keywords, setKeywords] = useState<string[]>([]);
    const [newKeyword, setNewKeyword] = useState('');
    const [errorMessage, setErrorMessage] = useState(defaultSettingValues[SettingKey.RequestFilterErrorMessage]);
    const [saveFailed, setSaveFailed] = useState(false);

    const intendedValuesRef = useRef({ ...defaultSettingValues });
    const confirmedValuesRef = useRef({ ...defaultSettingValues });
    const loadedKeysRef = useRef(new Set<string>());
    const hasLocalIntentRef = useRef<Record<string, boolean>>({});
    const inFlightValuesRef = useRef<Record<string, string | undefined>>({});
    const errorMessageDraftRef = useRef(false);

    useEffect(() => {
        if (!settings) return;
        const acceptedValues: Record<string, string> = {};
        for (const [key, fallbackValue] of Object.entries(defaultSettingValues)) {
            const serverValue = settings.find((setting) => setting.key === key)?.value ?? fallbackValue;
            const isSaving = inFlightValuesRef.current[key] !== undefined;
            const isEditingErrorMessage = key === SettingKey.RequestFilterErrorMessage && errorMessageDraftRef.current;
            if (isSaving || isEditingErrorMessage || (loadedKeysRef.current.has(key) && hasLocalIntentRef.current[key]
                && intendedValuesRef.current[key] !== serverValue)) {
                continue;
            }
            loadedKeysRef.current.add(key);
            hasLocalIntentRef.current[key] = false;
            intendedValuesRef.current[key] = serverValue;
            confirmedValuesRef.current[key] = serverValue;
            acceptedValues[key] = serverValue;
        }
        queueMicrotask(() => {
            if (SettingKey.RequestFilterEnabled in acceptedValues) {
                setEnabled(acceptedValues[SettingKey.RequestFilterEnabled] === 'true');
            }
            if (SettingKey.RequestFilterKeywords in acceptedValues) {
                setKeywords(parseKeywords(acceptedValues[SettingKey.RequestFilterKeywords]));
            }
            if (SettingKey.RequestFilterErrorMessage in acceptedValues) {
                setErrorMessage(acceptedValues[SettingKey.RequestFilterErrorMessage]);
            }
        });
    }, [settings]);

    const flushSettingSave = async (key: string) => {
        if (inFlightValuesRef.current[key] !== undefined || !hasLocalIntentRef.current[key]) return;
        const value = intendedValuesRef.current[key];
        inFlightValuesRef.current[key] = value;
        setSaveFailed(false);
        // Per-call mutate callbacks only follow the latest mutation. Each save
        // needs its own promise so concurrent fields cannot strand this queue.
        try {
            await setSetting.mutateAsync({ key, value });
            confirmedValuesRef.current[key] = value;
            if (intendedValuesRef.current[key] === value) {
                toast.success(translate('saved'));
            }
        } catch {
            setSaveFailed(true);
            if (intendedValuesRef.current[key] === value) {
                hasLocalIntentRef.current[key] = false;
                const confirmedValue = confirmedValuesRef.current[key];
                intendedValuesRef.current[key] = confirmedValue;
                if (key === SettingKey.RequestFilterEnabled) setEnabled(confirmedValue === 'true');
                if (key === SettingKey.RequestFilterKeywords) setKeywords(parseKeywords(confirmedValue));
                if (key === SettingKey.RequestFilterErrorMessage && !errorMessageDraftRef.current) {
                    setErrorMessage(confirmedValue);
                }
            }
        } finally {
            delete inFlightValuesRef.current[key];
            if (hasLocalIntentRef.current[key] && intendedValuesRef.current[key] !== value) {
                void flushSettingSave(key);
            }
        }
    };

    const saveSetting = (key: string, value: string) => {
        if (value === intendedValuesRef.current[key]) return;
        intendedValuesRef.current[key] = value;
        hasLocalIntentRef.current[key] = true;
        void flushSettingSave(key);
    };

    const saveKeywords = (nextKeywords: string[]) => {
        setKeywords(nextKeywords);
        saveSetting(SettingKey.RequestFilterKeywords, JSON.stringify(nextKeywords));
    };

    const handleAddKeyword = () => {
        const candidateKeyword = newKeyword.trim();
        if (!candidateKeyword) return;
        if (keywords.some((keyword) => keyword.toLowerCase() === candidateKeyword.toLowerCase())) {
            toast.error(translate('requestFilter.keywords.duplicate'));
            return;
        }
        saveKeywords([...keywords, candidateKeyword]);
        setNewKeyword('');
    };

    return (
        <div className="min-w-0 space-y-5 rounded-xl border border-border/35 bg-card p-6 text-card-foreground" aria-busy={!settings}>
            <div className="space-y-1">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <ShieldAlert className="h-5 w-5" aria-hidden="true" />
                    {translate('requestFilter.title')}
                </h2>
                <p id={`${fieldId}-description`} className="text-sm text-muted-foreground">
                    {translate('requestFilter.description')}
                </p>
            </div>
            {saveFailed && (
                <p role="alert" className="text-sm text-destructive">{translate('requestFilter.saveFailed')}</p>
            )}

            <div className="flex min-w-0 flex-col gap-3 rounded-lg border border-border/30 bg-card p-4 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0 flex items-center gap-3">
                    <ShieldAlert className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                    <label htmlFor={`${fieldId}-enabled`} className="text-sm font-medium">
                        {translate('requestFilter.enabled.label')}
                    </label>
                </div>
                <Switch
                    id={`${fieldId}-enabled`}
                    checked={enabled}
                    disabled={!settings}
                    aria-describedby={`${fieldId}-description`}
                    onCheckedChange={(checked) => {
                        setEnabled(checked);
                        saveSetting(SettingKey.RequestFilterEnabled, checked ? 'true' : 'false');
                    }}
                />
            </div>

            <div className="space-y-3 rounded-lg border border-border/30 bg-card p-4">
                <div className="flex items-center gap-3">
                    <ListFilter className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                    <label htmlFor={`${fieldId}-keyword`} className="text-sm font-medium">
                        {translate('requestFilter.keywords.label')}
                    </label>
                    <Badge variant="secondary" className="text-xs">{keywords.length}</Badge>
                </div>
                <div className="flex gap-2 pl-8">
                    <Input
                        id={`${fieldId}-keyword`}
                        value={newKeyword}
                        onChange={(event) => setNewKeyword(event.target.value)}
                        onKeyDown={(event) => {
                            if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                                event.preventDefault();
                                handleAddKeyword();
                            }
                        }}
                        placeholder={translate('requestFilter.keywords.placeholder')}
                        aria-describedby={`${fieldId}-keyword-hint`}
                        disabled={!settings}
                        className="flex-1 rounded-xl"
                    />
                    <Button type="button" variant="outline" size="sm" className="shrink-0 rounded-xl"
                        onClick={handleAddKeyword} disabled={!settings || !newKeyword.trim()}>
                        {translate('requestFilter.keywords.add')}
                    </Button>
                </div>
                {keywords.length > 0 ? (
                    <ul className="flex flex-wrap gap-2 pl-8">
                        {keywords.map((keyword, keywordIndex) => (
                            <li key={keyword}>
                                <Button type="button" variant="outline" size="sm"
                                    className="gap-1.5 rounded-lg text-xs hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30"
                                    onClick={() => saveKeywords(keywords.filter((_keyword, index) => index !== keywordIndex))}
                                    aria-label={`${translate('requestFilter.keywords.removeHint')}: ${keyword}`}>
                                    {keyword}
                                    <RemoveIcon className="size-3" aria-hidden="true" />
                                </Button>
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="pl-8 text-xs text-muted-foreground">{translate('requestFilter.keywords.empty')}</p>
                )}
                <p id={`${fieldId}-keyword-hint`} className="pl-8 text-xs text-muted-foreground">
                    {translate('requestFilter.keywords.hint')}
                </p>
            </div>

            <div className="space-y-3 rounded-lg border border-border/30 bg-card p-4">
                <div className="flex items-center gap-3">
                    <MessageSquareWarning className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                    <label htmlFor={`${fieldId}-error-message`} className="text-sm font-medium">
                        {translate('requestFilter.errorMessage.label')}
                    </label>
                </div>
                <Input
                    id={`${fieldId}-error-message`}
                    value={errorMessage}
                    onChange={(event) => {
                        errorMessageDraftRef.current = true;
                        setErrorMessage(event.target.value);
                    }}
                    onBlur={() => {
                        errorMessageDraftRef.current = false;
                        saveSetting(SettingKey.RequestFilterErrorMessage, errorMessage);
                    }}
                    placeholder={translate('requestFilter.errorMessage.placeholder')}
                    aria-describedby={`${fieldId}-error-hint`}
                    disabled={!settings}
                    className="ml-8 w-[calc(100%-2rem)] rounded-xl"
                />
                <p id={`${fieldId}-error-hint`} className="pl-8 text-xs text-muted-foreground">
                    {translate('requestFilter.errorMessage.hint')}
                </p>
            </div>
        </div>
    );
}
