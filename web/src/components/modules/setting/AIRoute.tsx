'use client';

import { useState, type FormEvent } from 'react';
import { Bot, Clock3, KeyRound, Link2, Sparkles } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { API_BASE_URL } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { useGroupList } from '@/api/endpoints/group';
import { SettingKey, useSetSetting, useSettingList } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import {
    buildOctopusAIRoutePreset,
    getAIRouteConfigurationIssues,
    getAIRouteModelAliases,
    inspectAIRouteServices,
    type AIRouteConfiguration,
} from './airoute-config';

const DEFAULT_CONFIGURATION: AIRouteConfiguration = {
    groupID: '0',
    baseURL: '',
    apiKey: '',
    model: '',
    timeoutSeconds: '180',
    parallelism: '3',
    servicesJSON: '[]',
};

const CONFIGURATION_SETTINGS = {
    groupID: SettingKey.AIRouteGroupID,
    baseURL: SettingKey.AIRouteBaseURL,
    apiKey: SettingKey.AIRouteAPIKey,
    model: SettingKey.AIRouteModel,
    timeoutSeconds: SettingKey.AIRouteTimeoutSeconds,
    parallelism: SettingKey.AIRouteParallelism,
    servicesJSON: SettingKey.AIRouteServices,
} satisfies Record<keyof AIRouteConfiguration, string>;

const CONFIGURATION_FIELDS = Object.keys(CONFIGURATION_SETTINGS) as (keyof AIRouteConfiguration)[];

export function SettingAIRoute() {
    const t = useTranslations('setting');
    const { data: settings, isLoading: settingsLoading, isError: settingsError, refetch } = useSettingList();
    const { data: groups = [], isLoading: groupsLoading, isError: groupsError } = useGroupList();
    const setSetting = useSetSetting();
    const [draft, setDraft] = useState<Partial<AIRouteConfiguration>>({});
    const [presetModel, setPresetModel] = useState('');
    const [presetError, setPresetError] = useState(false);
    const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');

    const savedConfiguration = { ...DEFAULT_CONFIGURATION };
    for (const field of CONFIGURATION_FIELDS) {
        const storedValue = settings?.find((setting) => setting.key === CONFIGURATION_SETTINGS[field])?.value;
        savedConfiguration[field] = storedValue || DEFAULT_CONFIGURATION[field];
    }
    const configuration = { ...savedConfiguration, ...draft };
    const services = inspectAIRouteServices(configuration.servicesJSON);
    const issues = getAIRouteConfigurationIssues(configuration);
    const modelAliases = getAIRouteModelAliases(groups);
    const selectedPresetModel = modelAliases.includes(presetModel) ? presetModel : '';
    const isSaving = saveStatus === 'saving';
    const unavailable = settingsLoading || settingsError || !settings;
    const hasChanges = CONFIGURATION_FIELDS.some((field) => {
        const nextValue = field === 'servicesJSON' ? services.normalizedJSON : configuration[field];
        return nextValue !== savedConfiguration[field];
    });
    const publicBaseURL = settings?.find((setting) => setting.key === SettingKey.PublicAPIBaseURL)?.value || '';

    const updateField = (field: keyof AIRouteConfiguration, value: string) => {
        setDraft((current) => ({ ...current, [field]: value }));
        setSaveStatus('idle');
    };

    const applyOctopusPreset = () => {
        if (unavailable || isSaving || !selectedPresetModel || services.usesServicePool) return;
        const preset = buildOctopusAIRoutePreset({
            publicBaseURL,
            consoleAPIBaseURL: API_BASE_URL,
            browserOrigin: window.location.origin,
            modelAlias: selectedPresetModel,
        });
        setPresetError(!preset);
        if (!preset) return;
        setDraft((current) => ({ ...current, ...preset }));
        setSaveStatus('idle');
    };

    const handleSave = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (unavailable || isSaving || issues.length > 0 || !hasChanges) return;
        const nextConfiguration = { ...configuration, servicesJSON: services.normalizedJSON };
        for (const field of ['baseURL', 'model', 'timeoutSeconds', 'parallelism'] as const) {
            if (field in draft) nextConfiguration[field] = nextConfiguration[field].trim();
        }
        const changedFields = CONFIGURATION_FIELDS.filter((field) => nextConfiguration[field] !== savedConfiguration[field]);
        setSaveStatus('saving');
        try {
            // The existing setting endpoint saves one field at a time, not atomically.
            for (const field of changedFields) {
                await setSetting.mutateAsync({ key: CONFIGURATION_SETTINGS[field], value: nextConfiguration[field] });
            }
            const refreshed = await refetch();
            if (refreshed.isError) throw new Error('settings-refresh-failed');
            setDraft({});
            setSaveStatus('saved');
            toast.success(t('saved'));
        } catch {
            // Keep the draft visible so a partially saved configuration can be retried.
            setSaveStatus('error');
        }
    };

    const presetDisabledReason = services.usesServicePool
        ? t('aiRoute.selfApi.poolActive')
        : groupsLoading
            ? t('aiRoute.status.loading')
            : groupsError
                ? t('aiRoute.selfApi.groupsError')
                : modelAliases.length === 0
                    ? t('aiRoute.selfApi.noGroups')
                    : !selectedPresetModel
                        ? t('aiRoute.selfApi.selectModel')
                        : '';
    const statusMessage = unavailable
        ? t(settingsError ? 'aiRoute.status.loadError' : 'aiRoute.status.loading')
        : isSaving
            ? t('aiRoute.save.saving')
            : saveStatus === 'error'
                ? t('aiRoute.save.error')
                : issues.length > 0
                    ? t('aiRoute.status.incomplete')
                    : hasChanges
                        ? t('aiRoute.save.unsaved')
                        : saveStatus === 'saved'
                            ? t('saved')
                            : t('aiRoute.save.unchanged');

    return (
        <div className="relative overflow-hidden rounded-xl border-border/35 bg-card p-6 text-card-foreground shadow-md ">
            <form className="space-y-5" onSubmit={handleSave} noValidate aria-busy={isSaving || settingsLoading}>
                <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="space-y-1.5">
                        <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                            <Bot className="h-5 w-5" aria-hidden="true" />
                            {t('aiRoute.title')}
                        </h2>
                        <p className="text-sm text-muted-foreground">{t('aiRoute.description')}</p>
                    </div>
                    <div className="w-fit rounded-full border-border/25 bg-card px-3 py-1.5 text-xs font-medium text-muted-foreground shadow-sm">
                        {t('aiRoute.badge')}
                    </div>
                </div>
                <p className="rounded-lg border border-border/50 bg-muted/40 p-3 text-sm">
                    {t('aiRoute.writeWarning')}
                </p>
                <p id="airoute-save-help" className="text-sm text-muted-foreground">{t('aiRoute.save.hint')}</p>

                <fieldset disabled={unavailable || isSaving} className="min-w-0 space-y-4">
                    <div className="space-y-3 rounded-lg border-border/30 bg-card p-4 shadow-sm">
                        <label htmlFor="airoute-target-group" className="flex items-center gap-3 text-sm font-medium">
                            <Sparkles className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                            {t('aiRoute.group.label')}
                        </label>
                        <Select
                            value={configuration.groupID}
                            onValueChange={(value) => updateField('groupID', value)}
                            disabled={unavailable || isSaving || groupsLoading || groupsError}
                        >
                            <SelectTrigger id="airoute-target-group" className="w-full rounded-lg" aria-describedby="airoute-target-help">
                                <SelectValue placeholder={t('aiRoute.group.placeholder')} />
                            </SelectTrigger>
                            <SelectContent className="rounded-lg">
                                <SelectItem value="0">{t('aiRoute.group.placeholder')}</SelectItem>
                                {groups.map((group) => (
                                    <SelectItem key={group.id} value={String(group.id)}>
                                        {group.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <p id="airoute-target-help" className="text-sm text-muted-foreground">{t('aiRoute.group.description')}</p>
                    </div>

                    <div className="space-y-3 rounded-lg border border-border/50 p-4">
                        <p id="airoute-preset-help" className="text-sm text-muted-foreground">{t('aiRoute.selfApi.description')}</p>
                        <label htmlFor="airoute-preset-model" className="block text-sm font-medium">{t('aiRoute.selfApi.modelLabel')}</label>
                        <Select
                            value={selectedPresetModel}
                            onValueChange={setPresetModel}
                            disabled={unavailable || isSaving || groupsLoading || groupsError || modelAliases.length === 0 || services.usesServicePool}
                        >
                            <SelectTrigger id="airoute-preset-model" className="w-full" aria-describedby="airoute-preset-help airoute-preset-status">
                                <SelectValue placeholder={t('aiRoute.selfApi.selectModel')} />
                            </SelectTrigger>
                            <SelectContent>
                                {modelAliases.map((alias) => <SelectItem key={alias} value={alias}>{alias}</SelectItem>)}
                            </SelectContent>
                        </Select>
                        <Button type="button" variant="outline" onClick={applyOctopusPreset} disabled={Boolean(presetDisabledReason)} aria-describedby="airoute-preset-help airoute-preset-status">
                            {t('aiRoute.selfApi.apply')}
                        </Button>
                        <p id="airoute-preset-status" className="text-sm text-muted-foreground" role="status">
                            {presetError ? t('aiRoute.selfApi.invalidURL') : presetDisabledReason}
                        </p>
                    </div>

                    <p className="text-sm font-medium">{t(services.usesServicePool ? 'aiRoute.services.active' : 'aiRoute.services.single')}</p>
                    <fieldset disabled={services.usesServicePool} className="grid min-w-0 gap-4 xl:grid-cols-2">
                        <div className="space-y-3 rounded-lg bg-card p-4 shadow-sm">
                            <label htmlFor="airoute-base-url" className="flex items-center gap-3 text-sm font-medium">
                                <Link2 className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                                {t('aiRoute.baseUrl.label')}
                            </label>
                            <Input
                                id="airoute-base-url"
                                type="url"
                                value={configuration.baseURL}
                                onChange={(event) => updateField('baseURL', event.target.value)}
                                placeholder={t('aiRoute.baseUrl.placeholder')}
                                required={!services.usesServicePool}
                                aria-invalid={issues.includes('baseURL')}
                                aria-describedby={`airoute-url-help${issues.includes('baseURL') ? ' airoute-error-baseURL' : ''}`}
                            />
                            <p id="airoute-url-help" className="text-sm text-muted-foreground">{t('aiRoute.baseUrl.description')}</p>
                        </div>
                        <div className="space-y-3 rounded-lg bg-card p-4 shadow-sm">
                            <label htmlFor="airoute-model" className="flex items-center gap-3 text-sm font-medium">
                                <Bot className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                                {t('aiRoute.model.label')}
                            </label>
                            <Input
                                id="airoute-model"
                                value={configuration.model}
                                onChange={(event) => updateField('model', event.target.value)}
                                placeholder={t('aiRoute.model.placeholder')}
                                required={!services.usesServicePool}
                                aria-invalid={issues.includes('model')}
                                aria-describedby={`airoute-model-help${issues.includes('model') ? ' airoute-error-model' : ''}`}
                            />
                            <p id="airoute-model-help" className="text-sm text-muted-foreground">{t('aiRoute.model.description')}</p>
                        </div>
                        <div className="space-y-3 rounded-lg bg-card p-4 shadow-sm xl:col-span-2">
                            <label htmlFor="airoute-api-key" className="flex items-center gap-3 text-sm font-medium">
                                <KeyRound className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                                {t('aiRoute.apiKey.label')}
                            </label>
                            <Input
                                id="airoute-api-key"
                                type="password"
                                autoComplete="new-password"
                                spellCheck={false}
                                value={configuration.apiKey}
                                onChange={(event) => updateField('apiKey', event.target.value)}
                                placeholder={t('aiRoute.apiKey.placeholder')}
                                required={!services.usesServicePool}
                                aria-invalid={issues.includes('apiKey')}
                                aria-describedby={`airoute-key-help${issues.includes('apiKey') ? ' airoute-error-apiKey' : ''}`}
                            />
                            <p id="airoute-key-help" className="text-sm text-muted-foreground">{t('aiRoute.apiKey.description')}</p>
                        </div>
                    </fieldset>
                    <p className="text-sm text-muted-foreground">{t('aiRoute.networkHint')}</p>

                    <div className="grid gap-4 xl:grid-cols-2">
                        {(['timeoutSeconds', 'parallelism'] as const).map((field) => (
                            <div key={field} className="space-y-3 rounded-lg bg-card p-4 shadow-sm">
                                <label htmlFor={`airoute-${field}`} className="flex items-center gap-3 text-sm font-medium">
                                    <Clock3 className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
                                    {t(`aiRoute.${field}.label`)}
                                </label>
                                <Input
                                    id={`airoute-${field}`}
                                    type="number"
                                    min="1"
                                    step="1"
                                    required
                                    value={configuration[field]}
                                    onChange={(event) => updateField(field, event.target.value)}
                                    aria-invalid={issues.includes(field)}
                                    aria-describedby={`airoute-${field}-help${issues.includes(field) ? ` airoute-error-${field}` : ''}`}
                                />
                                <p id={`airoute-${field}-help`} className="text-sm text-muted-foreground">{t(`aiRoute.${field}.hint`)}</p>
                            </div>
                        ))}
                    </div>

                    <details className="rounded-lg border border-border/50 p-4">
                        <summary className="cursor-pointer rounded text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring">
                            {t('aiRoute.services.advanced')}
                        </summary>
                        <div className="mt-4 space-y-3">
                            <p id="airoute-services-help" className="text-sm text-muted-foreground">{t('aiRoute.services.description')}</p>
                            <label htmlFor="airoute-services" className="block text-sm font-medium">{t('aiRoute.services.label')}</label>
                            <textarea
                                id="airoute-services"
                                value={configuration.servicesJSON}
                                onChange={(event) => updateField('servicesJSON', event.target.value)}
                                placeholder={t('aiRoute.services.placeholder')}
                                autoComplete="off"
                                spellCheck={false}
                                aria-invalid={issues.includes('servicesJSON')}
                                aria-describedby={`airoute-services-help${issues.includes('servicesJSON') ? ' airoute-error-servicesJSON' : ''}`}
                                className="min-h-44 w-full rounded-lg border border-border/35 bg-card px-4 py-3 font-mono text-sm text-foreground shadow-inner outline-none focus-visible:border-ring focus-visible:ring-4 focus-visible:ring-ring/20"
                            />
                        </div>
                    </details>
                </fieldset>

                {!unavailable && issues.length > 0 && (
                    <ul className="list-inside list-disc space-y-1 text-sm text-destructive" aria-live="polite">
                        {issues.map((field) => <li id={`airoute-error-${field}`} key={field}>{t(`aiRoute.validation.${field}`)}</li>)}
                    </ul>
                )}
                <div className="flex flex-wrap items-center gap-3">
                    <Button type="submit" disabled={unavailable || isSaving || issues.length > 0 || !hasChanges} aria-describedby="airoute-save-help airoute-save-status">
                        {t(isSaving ? 'aiRoute.save.saving' : 'aiRoute.save.action')}
                    </Button>
                    <p id="airoute-save-status" role="status" className="text-sm text-muted-foreground">{statusMessage}</p>
                </div>
                <p className="text-sm text-muted-foreground">{t('aiRoute.status.notTested')}</p>
            </form>
        </div>
    );
}
