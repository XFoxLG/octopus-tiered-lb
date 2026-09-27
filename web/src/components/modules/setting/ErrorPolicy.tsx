'use client';

import { useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { ShieldAlert, ListFilter, MessageSquareWarning, Plus, Trash2 } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { Hint } from '@/components/ui/hint';
import { SettingKey, useSetSetting, useSettingList } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';

// ErrorRule mirrors relay.CustomErrorRule on the backend. Code and Keyword are
// OR-matched; ChannelType empty means "all channel types".
type ErrorRule = {
    channel_type?: string;
    code?: number;
    keyword?: string;
    passthrough_status?: boolean;
    custom_status?: number;
    passthrough_message?: boolean;
    message?: string;
};

const CHANNEL_TYPES = ['', 'chat', 'response', 'anthropic', 'gemini', 'embedding', 'codex'] as const;

const EMPTY_DRAFT: ErrorRule = {
    channel_type: '',
    code: 0,
    keyword: '',
    passthrough_status: false,
    custom_status: 400,
    passthrough_message: false,
    message: '',
};

function parseRules(value: string): ErrorRule[] {
    try {
        const parsed: unknown = JSON.parse(value);
        if (!Array.isArray(parsed)) return [];
        return parsed.filter((item): item is ErrorRule => typeof item === 'object' && item !== null);
    } catch {
        return [];
    }
}

function sanitizeCode(value: number | undefined): number {
    return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.trunc(value) : 0;
}

export function SettingErrorPolicy() {
    const translate = useTranslations('setting');
    const fieldId = useId();
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [retryableCodes, setRetryableCodes] = useState('');
    const [rules, setRules] = useState<ErrorRule[]>([]);
    const [draft, setDraft] = useState<ErrorRule>(EMPTY_DRAFT);
    const [saveFailed, setSaveFailed] = useState(false);

    const intendedValuesRef = useRef<Record<string, string>>({
        [SettingKey.CustomRetryableCodes]: '',
        [SettingKey.CustomErrorRules]: '[]',
    });
    const confirmedValuesRef = useRef<Record<string, string>>({ ...intendedValuesRef.current });
    const inFlightValuesRef = useRef<Record<string, string | undefined>>({});
    const loadedKeysRef = useRef(new Set<string>());
    const hasLocalIntentRef = useRef<Record<string, boolean>>({});

    useEffect(() => {
        if (!settings) return;
        const defaults: Record<string, string> = {
            [SettingKey.CustomRetryableCodes]: '',
            [SettingKey.CustomErrorRules]: '[]',
        };
        const accepted: Record<string, string> = {};
        for (const [key, fallback] of Object.entries(defaults)) {
            const serverValue = settings.find((setting) => setting.key === key)?.value ?? fallback;
            const isSaving = inFlightValuesRef.current[key] !== undefined;
            if (
                isSaving ||
                (loadedKeysRef.current.has(key) &&
                    hasLocalIntentRef.current[key] &&
                    intendedValuesRef.current[key] !== serverValue)
            ) {
                continue;
            }
            loadedKeysRef.current.add(key);
            hasLocalIntentRef.current[key] = false;
            intendedValuesRef.current[key] = serverValue;
            confirmedValuesRef.current[key] = serverValue;
            accepted[key] = serverValue;
        }
        queueMicrotask(() => {
            if (SettingKey.CustomRetryableCodes in accepted) {
                setRetryableCodes(accepted[SettingKey.CustomRetryableCodes]);
            }
            if (SettingKey.CustomErrorRules in accepted) {
                setRules(parseRules(accepted[SettingKey.CustomErrorRules]));
            }
        });
    }, [settings]);

    // Per-call mutate promises so two fields saving at once cannot strand the
    // queue (same hardening as RequestFilter).
    const flushSettingSave = async (key: string) => {
        if (inFlightValuesRef.current[key] !== undefined || !hasLocalIntentRef.current[key]) return;
        const value = intendedValuesRef.current[key];
        inFlightValuesRef.current[key] = value;
        setSaveFailed(false);
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
                if (key === SettingKey.CustomRetryableCodes) setRetryableCodes(confirmedValue);
                if (key === SettingKey.CustomErrorRules) setRules(parseRules(confirmedValue));
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

    const saveRules = (nextRules: ErrorRule[]) => {
        setRules(nextRules);
        saveSetting(SettingKey.CustomErrorRules, JSON.stringify(nextRules));
    };

    const canAddRule = sanitizeCode(draft.code) !== 0 || (draft.keyword ?? '').trim() !== '';

    const handleAddRule = () => {
        if (!canAddRule) {
            toast.error(translate('errorPolicy.rule.invalid'));
            return;
        }
        const nextRule: ErrorRule = {
            channel_type: (draft.channel_type ?? '').trim(),
            code: sanitizeCode(draft.code),
            keyword: (draft.keyword ?? '').trim(),
            passthrough_status: Boolean(draft.passthrough_status),
            custom_status: sanitizeCode(draft.custom_status),
            passthrough_message: Boolean(draft.passthrough_message),
            message: (draft.message ?? '').trim(),
        };
        saveRules([...rules, nextRule]);
        setDraft(EMPTY_DRAFT);
    };

    return (
        <div className="min-w-0 space-y-5 rounded-xl border border-border/35 bg-card p-6 text-card-foreground" aria-busy={!settings}>
            <div className="space-y-1">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <ShieldAlert className="h-5 w-5" aria-hidden="true" />
                    {translate('errorPolicy.title')}
                </h2>
                <p id={`${fieldId}-description`} className="text-sm text-muted-foreground">
                    {translate('errorPolicy.description')}
                </p>
            </div>
            {saveFailed && (
                <p role="alert" className="text-sm text-destructive">{translate('errorPolicy.saveFailed')}</p>
            )}

            <div className="space-y-3 rounded-lg border border-border/30 bg-card p-4">
                <div className="flex items-center gap-3">
                    <ListFilter className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                    <label htmlFor={`${fieldId}-retryable-codes`} className="text-sm font-medium">
                        {translate('errorPolicy.retryableCodes.label')}
                        <Hint text={translate('errorPolicy.retryableCodes.hint')} />
                    </label>
                </div>
                <Input
                    id={`${fieldId}-retryable-codes`}
                    value={retryableCodes}
                    onChange={(event) => setRetryableCodes(event.target.value)}
                    onBlur={() => saveSetting(SettingKey.CustomRetryableCodes, retryableCodes.trim())}
                    placeholder={translate('errorPolicy.retryableCodes.placeholder')}
                    aria-describedby={`${fieldId}-description`}
                    disabled={!settings}
                    className="rounded-xl"
                />
            </div>

            <div className="space-y-3 rounded-lg border border-border/30 bg-card p-4">
                <div className="flex items-center gap-3">
                    <MessageSquareWarning className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                    <span className="text-sm font-medium">
                        {translate('errorPolicy.rules.label')}
                        <Hint text={translate('errorPolicy.rules.hint')} />
                    </span>
                </div>
                {rules.length > 0 ? (
                    <ul className="space-y-2">
                        {rules.map((rule, ruleIndex) => (
                            <li
                                key={`${rule.channel_type ?? ''}-${rule.code ?? 0}-${rule.keyword ?? ''}-${ruleIndex}`}
                                className="flex items-center justify-between gap-3 rounded-lg border border-border/30 bg-card px-3 py-2"
                            >
                                <div className="min-w-0 flex-1 truncate text-xs">
                                    <span className="font-medium">
                                        {translate('errorPolicy.rules.channelType')}: {rule.channel_type || translate('errorPolicy.rules.anyChannel')}
                                    </span>
                                    {rule.code ? <span className="ml-2">{translate('errorPolicy.rules.code')}: {rule.code}</span> : null}
                                    {rule.keyword ? <span className="ml-2">{translate('errorPolicy.rules.keyword')}: {rule.keyword}</span> : null}
                                    <span className="ml-2">
                                        {translate('errorPolicy.rules.customStatus')}:{' '}
                                        {rule.passthrough_status ? translate('errorPolicy.rules.passthrough') : (rule.custom_status || 502)}
                                    </span>
                                    <span className="ml-2">
                                        {translate('errorPolicy.rules.message')}:{' '}
                                        {rule.passthrough_message
                                            ? translate('errorPolicy.rules.passthrough')
                                            : (rule.message || translate('errorPolicy.rules.defaultMessage'))}
                                    </span>
                                </div>
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    className="shrink-0 rounded-lg text-xs hover:border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
                                    onClick={() => saveRules(rules.filter((_rule, index) => index !== ruleIndex))}
                                    aria-label={`${translate('errorPolicy.rules.removeHint')}: ${rule.keyword || rule.code || rule.channel_type || ''}`}
                                >
                                    <Trash2 className="size-3" aria-hidden="true" />
                                </Button>
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="text-xs text-muted-foreground">{translate('errorPolicy.rules.empty')}</p>
                )}

                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
                    <div className="flex flex-col gap-1">
                        <label htmlFor={`${fieldId}-rule-channel-type`} className="text-xs text-muted-foreground">
                            {translate('errorPolicy.rules.channelType')}
                        </label>
                        <Select
                            value={draft.channel_type || 'all'}
                            onValueChange={(value) => setDraft((prev) => ({ ...prev, channel_type: value === 'all' ? '' : value }))}
                        >
                            <SelectTrigger id={`${fieldId}-rule-channel-type`} className="rounded-lg">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="rounded-xl">
                                {CHANNEL_TYPES.map((channelType) => (
                                    <SelectItem key={channelType || 'all'} className="rounded-lg" value={channelType || 'all'}>
                                        {channelType || translate('errorPolicy.rules.anyChannel')}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1">
                        <label htmlFor={`${fieldId}-rule-code`} className="text-xs text-muted-foreground">
                            {translate('errorPolicy.rules.code')}
                        </label>
                        <Input
                            id={`${fieldId}-rule-code`}
                            type="number"
                            min={0}
                            max={599}
                            value={draft.code || ''}
                            onChange={(event) => setDraft((prev) => ({ ...prev, code: Number(event.target.value || 0) }))}
                            className="h-9 rounded-lg"
                        />
                    </div>
                    <div className="flex flex-col gap-1">
                        <label htmlFor={`${fieldId}-rule-keyword`} className="text-xs text-muted-foreground">
                            {translate('errorPolicy.rules.keyword')}
                        </label>
                        <Input
                            id={`${fieldId}-rule-keyword`}
                            value={draft.keyword ?? ''}
                            onChange={(event) => setDraft((prev) => ({ ...prev, keyword: event.target.value }))}
                            className="h-9 rounded-lg"
                        />
                    </div>
                    <div className="flex flex-col gap-1">
                        <label htmlFor={`${fieldId}-rule-status`} className="text-xs text-muted-foreground">
                            {translate('errorPolicy.rules.customStatus')}
                        </label>
                        <Input
                            id={`${fieldId}-rule-status`}
                            type="number"
                            min={100}
                            max={599}
                            value={draft.custom_status || ''}
                            disabled={Boolean(draft.passthrough_status)}
                            onChange={(event) => setDraft((prev) => ({ ...prev, custom_status: Number(event.target.value || 0) }))}
                            className="h-9 rounded-lg"
                        />
                    </div>
                    <div className="flex flex-col gap-1">
                        <label htmlFor={`${fieldId}-rule-message`} className="text-xs text-muted-foreground">
                            {translate('errorPolicy.rules.message')}
                        </label>
                        <Input
                            id={`${fieldId}-rule-message`}
                            value={draft.message ?? ''}
                            disabled={Boolean(draft.passthrough_message)}
                            onChange={(event) => setDraft((prev) => ({ ...prev, message: event.target.value }))}
                            placeholder={translate('errorPolicy.rules.messagePlaceholder')}
                            className="h-9 rounded-lg"
                        />
                    </div>
                    <label className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Switch
                            checked={Boolean(draft.passthrough_status)}
                            onCheckedChange={(checked) => setDraft((prev) => ({ ...prev, passthrough_status: checked }))}
                        />
                        {translate('errorPolicy.rules.passthroughStatus')}
                    </label>
                    <label className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Switch
                            checked={Boolean(draft.passthrough_message)}
                            onCheckedChange={(checked) => setDraft((prev) => ({ ...prev, passthrough_message: checked }))}
                        />
                        {translate('errorPolicy.rules.passthroughMessage')}
                    </label>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="h-9 shrink-0 rounded-lg self-end"
                        onClick={handleAddRule}
                        disabled={!settings || !canAddRule}
                    >
                        <Plus className="mr-1.5 size-3.5" aria-hidden="true" />
                        {translate('errorPolicy.rules.add')}
                    </Button>
                </div>
                <p className="text-xs text-muted-foreground">{translate('errorPolicy.rules.addHint')}</p>
            </div>
        </div>
    );
}
