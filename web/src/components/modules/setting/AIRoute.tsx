'use client';

import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Bot, Clock3, KeyRound, Link2, Plus, Sparkles, Trash2 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { API_BASE_URL } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
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
    type AIRouteConfiguration,
} from './airoute-config';
import {
    parseServicesJSON,
    serializeServicesRows,
    type AIRouteServiceRow,
} from './ai-route-services';

const DEFAULT_CONFIGURATION: AIRouteConfiguration = {
    groupID: '0',
    baseURL: '',
    apiKey: '',
    model: '',
    timeoutSeconds: '180',
    parallelism: '3',
    maxModels: '120',
    servicesJSON: '[]',
};

const CONFIGURATION_SETTINGS = {
    groupID: SettingKey.AIRouteGroupID,
    baseURL: SettingKey.AIRouteBaseURL,
    apiKey: SettingKey.AIRouteAPIKey,
    model: SettingKey.AIRouteModel,
    timeoutSeconds: SettingKey.AIRouteTimeoutSeconds,
    parallelism: SettingKey.AIRouteParallelism,
    maxModels: SettingKey.AIRouteMaxModelsPerRequest,
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
    const [serviceRows, setServiceRows] = useState<AIRouteServiceRow[]>([]);
    const [servicesHydrated, setServicesHydrated] = useState(false);
    const [servicesInvalid, setServicesInvalid] = useState(false);
    const [servicesRawFallback, setServicesRawFallback] = useState('');
    const baselineServicesJSON = useRef('[]');

    const savedConfiguration = { ...DEFAULT_CONFIGURATION };
    for (const field of CONFIGURATION_FIELDS) {
        const storedValue = settings?.find((setting) => setting.key === CONFIGURATION_SETTINGS[field])?.value;
        savedConfiguration[field] = storedValue || DEFAULT_CONFIGURATION[field];
    }
    const configuration = { ...savedConfiguration, ...draft };

    // 服务池结构化编辑器水合：settings 首次到位时解析一次存量 JSON。
    // 之后以本地 rows/原文为准，避免保存后的 refetch 覆盖正在编辑的内容。
    useEffect(() => {
        if (!settings || servicesHydrated) return;
        const stored = settings.find((setting) => setting.key === SettingKey.AIRouteServices)?.value || '[]';
        const { rows, invalid } = parseServicesJSON(stored);
        if (invalid || !rows) {
            setServicesInvalid(true);
            setServicesRawFallback(stored);
            baselineServicesJSON.current = stored.trim();
        } else {
            setServiceRows(rows);
            baselineServicesJSON.current = serializeServicesRows(rows);
        }
        setServicesHydrated(true);
    }, [settings, servicesHydrated]);

    // 出站的服务池 JSON：结构化路径由 rows 序列化；降级路径使用原文（此时 issues 会拦截保存）。
    const effectiveServicesJSON = servicesInvalid ? servicesRawFallback : serializeServicesRows(serviceRows);
    const usesServicePool = servicesInvalid || serviceRows.length > 0;
    const issues = getAIRouteConfigurationIssues({ ...configuration, servicesJSON: effectiveServicesJSON });
    const modelAliases = getAIRouteModelAliases(groups);
    const selectedPresetModel = modelAliases.includes(presetModel) ? presetModel : '';
    const isSaving = saveStatus === 'saving';
    const unavailable = settingsLoading || settingsError || !settings;
    const hasChanges = CONFIGURATION_FIELDS.some((field) => {
        if (field === 'servicesJSON') return effectiveServicesJSON.trim() !== baselineServicesJSON.current;
        return configuration[field] !== savedConfiguration[field];
    });
    const publicBaseURL = settings?.find((setting) => setting.key === SettingKey.PublicAPIBaseURL)?.value || '';

    const updateField = (field: keyof AIRouteConfiguration, value: string) => {
        setDraft((current) => ({ ...current, [field]: value }));
        setSaveStatus('idle');
    };

    const applyOctopusPreset = () => {
        if (unavailable || isSaving || !selectedPresetModel || usesServicePool) return;
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

    const updateServiceRow = (index: number, patch: Partial<AIRouteServiceRow>) => {
        setServiceRows((current) => current.map((row, i) => (i === index ? { ...row, ...patch } : row)));
        setSaveStatus('idle');
    };

    const addServiceRow = () => {
        setServiceRows((current) => [...current, { name: '', baseUrl: '', apiKey: '', model: '', enabled: true }]);
        setSaveStatus('idle');
    };

    const removeServiceRow = (index: number) => {
        setServiceRows((current) => current.filter((_, i) => i !== index));
        setSaveStatus('idle');
    };

    // 降级路径手动修复：重新解析 textarea 原文，合法则切回结构化编辑器。
    const retryParseServicesFallback = () => {
        const raw = servicesRawFallback.trim() || '[]';
        const { rows, invalid } = parseServicesJSON(raw);
        if (invalid || !rows) {
            toast.error(t('aiRoute.services.invalid'));
            return;
        }
        setServiceRows(rows);
        setServicesInvalid(false);
        setServicesRawFallback('');
        setSaveStatus('idle');
    };

    const handleSave = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (unavailable || isSaving || issues.length > 0 || !hasChanges) return;
        const nextConfiguration = { ...configuration, servicesJSON: effectiveServicesJSON.trim() };
        for (const field of ['baseURL', 'model', 'timeoutSeconds', 'parallelism', 'maxModels'] as const) {
            if (field in draft) nextConfiguration[field] = nextConfiguration[field].trim();
        }
        const changedFields = CONFIGURATION_FIELDS.filter((field) => {
            if (field === 'servicesJSON') return nextConfiguration.servicesJSON !== baselineServicesJSON.current;
            return nextConfiguration[field] !== savedConfiguration[field];
        });
        setSaveStatus('saving');
        try {
            // The existing setting endpoint saves one field at a time, not atomically.
            for (const field of changedFields) {
                await setSetting.mutateAsync({ key: CONFIGURATION_SETTINGS[field], value: nextConfiguration[field] });
            }
            const refreshed = await refetch();
            if (refreshed.isError) throw new Error('settings-refresh-failed');
            baselineServicesJSON.current = nextConfiguration.servicesJSON;
            setDraft({});
            setSaveStatus('saved');
            toast.success(t('saved'));
        } catch {
            // Keep the draft visible so a partially saved configuration can be retried.
            setSaveStatus('error');
        }
    };

    const presetDisabledReason = usesServicePool
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
                            disabled={unavailable || isSaving || groupsLoading || groupsError || modelAliases.length === 0 || usesServicePool}
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

                    <p className="text-sm font-medium">{t(usesServicePool ? 'aiRoute.services.active' : 'aiRoute.services.single')}</p>
                    <fieldset disabled={usesServicePool} className="grid min-w-0 gap-4 xl:grid-cols-2">
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
                                required={!usesServicePool}
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
                                required={!usesServicePool}
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
                                required={!usesServicePool}
                                aria-invalid={issues.includes('apiKey')}
                                aria-describedby={`airoute-key-help${issues.includes('apiKey') ? ' airoute-error-apiKey' : ''}`}
                            />
                            <p id="airoute-key-help" className="text-sm text-muted-foreground">{t('aiRoute.apiKey.description')}</p>
                        </div>
                    </fieldset>
                    <p className="text-sm text-muted-foreground">{t('aiRoute.networkHint')}</p>

                    <div className="grid gap-4 xl:grid-cols-2">
                        {(['timeoutSeconds', 'parallelism', 'maxModels'] as const).map((field) => (
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

                    <details open={servicesInvalid || undefined} className="rounded-lg border border-border/50 p-4">
                        <summary className="cursor-pointer rounded text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring">
                            {t('aiRoute.services.advanced')}
                        </summary>
                        <div className="mt-4 space-y-3">
                            <p id="airoute-services-help" className="text-sm text-muted-foreground">{t('aiRoute.services.description')}</p>
                            <p className="text-sm text-muted-foreground">{t('aiRoute.services.hint')}</p>
                            <p className="text-sm font-medium">{t('aiRoute.services.label')}</p>
                            {servicesInvalid ? (
                                <div className="space-y-3">
                                    <p className="rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive" role="alert">
                                        {t('aiRoute.services.invalidStored')}
                                    </p>
                                    <textarea
                                        id="airoute-services"
                                        value={servicesRawFallback}
                                        onChange={(event) => {
                                            setServicesRawFallback(event.target.value);
                                            setSaveStatus('idle');
                                        }}
                                        placeholder={t('aiRoute.services.placeholder')}
                                        autoComplete="off"
                                        spellCheck={false}
                                        aria-invalid={issues.includes('servicesJSON')}
                                        aria-describedby={`airoute-services-help${issues.includes('servicesJSON') ? ' airoute-error-servicesJSON' : ''}`}
                                        className="min-h-44 w-full rounded-lg border border-border/35 bg-card px-4 py-3 font-mono text-sm text-foreground shadow-inner outline-none focus-visible:border-ring focus-visible:ring-4 focus-visible:ring-ring/20"
                                    />
                                    <div className="flex items-center justify-end gap-2">
                                        <Button type="button" variant="outline" size="sm" onClick={retryParseServicesFallback}>
                                            {t('aiRoute.services.retryParse')}
                                        </Button>
                                    </div>
                                </div>
                            ) : (
                                <div className="space-y-3">
                                    {serviceRows.length === 0 && (
                                        <p className="text-sm text-muted-foreground">{t('aiRoute.services.empty')}</p>
                                    )}
                                    {serviceRows.map((row, index) => (
                                        <div key={index} className="space-y-2.5 rounded-lg border border-border/30 bg-card p-3.5 shadow-sm">
                                            <div className="flex items-center justify-between gap-3">
                                                <span className="text-xs font-medium text-muted-foreground">
                                                    {t('aiRoute.services.serviceIndex', { index: index + 1 })}
                                                    {row.enabled ? '' : ` · ${t('aiRoute.services.disabledTag')}`}
                                                </span>
                                                <div className="flex items-center gap-2">
                                                    <Switch
                                                        checked={row.enabled}
                                                        onCheckedChange={(checked) => updateServiceRow(index, { enabled: checked })}
                                                        aria-label={t('aiRoute.services.serviceIndex', { index: index + 1 })}
                                                    />
                                                    <Button
                                                        type="button"
                                                        variant="ghost"
                                                        size="icon"
                                                        onClick={() => removeServiceRow(index)}
                                                        aria-label={t('aiRoute.services.removeService', { index: index + 1 })}
                                                    >
                                                        <Trash2 className="h-4 w-4" aria-hidden="true" />
                                                    </Button>
                                                </div>
                                            </div>
                                            <div className="grid gap-2.5 sm:grid-cols-2">
                                                <Input
                                                    value={row.name}
                                                    onChange={(event) => updateServiceRow(index, { name: event.target.value })}
                                                    placeholder={t('aiRoute.services.namePlaceholder')}
                                                    className="w-full rounded-lg"
                                                />
                                                <Input
                                                    value={row.model}
                                                    onChange={(event) => updateServiceRow(index, { model: event.target.value })}
                                                    placeholder={t('aiRoute.services.modelPlaceholder')}
                                                    className="w-full rounded-lg"
                                                />
                                                <Input
                                                    value={row.baseUrl}
                                                    onChange={(event) => updateServiceRow(index, { baseUrl: event.target.value })}
                                                    placeholder={t('aiRoute.services.baseUrlPlaceholder')}
                                                    className="w-full rounded-lg sm:col-span-2"
                                                />
                                                <Input
                                                    type="password"
                                                    autoComplete="new-password"
                                                    spellCheck={false}
                                                    value={row.apiKey}
                                                    onChange={(event) => updateServiceRow(index, { apiKey: event.target.value })}
                                                    placeholder={t('aiRoute.services.apiKeyPlaceholder')}
                                                    className="w-full rounded-lg sm:col-span-2"
                                                />
                                            </div>
                                        </div>
                                    ))}
                                    <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
                                        <Button type="button" variant="outline" size="sm" onClick={addServiceRow}>
                                            <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
                                            {t('aiRoute.services.add')}
                                        </Button>
                                        {serviceRows.length > 0 && (
                                            <span className="text-xs text-muted-foreground">
                                                {t('aiRoute.services.enabledCount', {
                                                    enabled: serviceRows.filter((row) => row.enabled).length,
                                                    total: serviceRows.length,
                                                })}
                                            </span>
                                        )}
                                    </div>
                                </div>
                            )}
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
