'use client';

import { useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Database, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
    useGetCacheConfig, usePreviewCacheConfig, useTestCacheConnection, useSaveCacheConfig,
    type CacheRedisSummary,
} from '@/api/endpoints/setting';
import {
    allowsCacheCertificate, buildCacheConfigRequest, createCacheDraft, isCacheUrlInput, validateCacheDraft,
    type CacheDraft, type CacheFieldErrors,
} from './cache-config';

type CacheAction = 'preview' | 'test' | 'save';

export function SettingCache() {
    const translate = useTranslations('setting');
    const formId = useId();
    const { data: cacheConfig, isLoading, isError } = useGetCacheConfig();
    const previewCache = usePreviewCacheConfig();
    const testCache = useTestCacheConnection();
    const saveCache = useSaveCacheConfig();
    const [draft, setDraft] = useState<CacheDraft>(() => createCacheDraft(cacheConfig));
    const [initialized, setInitialized] = useState(Boolean(cacheConfig));
    const [fieldErrors, setFieldErrors] = useState<CacheFieldErrors>({});
    const [preview, setPreview] = useState<CacheRedisSummary | null>(null);
    const [feedback, setFeedback] = useState<{ message: string; error: boolean; restartNeeded?: boolean } | null>(null);
    const [pendingAction, setPendingAction] = useState<CacheAction | null>(null);
    const pendingActionRef = useRef<CacheAction | null>(null);
    const draftRevision = useRef(0);
    const advancedDetailsRef = useRef<HTMLDetailsElement>(null);

    const source = cacheConfig?.config_source;
    const editableSource = source === 'database' || source === 'file';
    const knownSource = editableSource || source === 'environment' || source === 'deployment';
    const formReady = initialized && !isLoading && !isError && Boolean(cacheConfig);
    const urlInput = isCacheUrlInput(draft.address);
    const manualIdentityDisabled = !draft.address.trim() || urlInput;
    const busy = pendingAction !== null;

    // A query refresh may update diagnostics, but must never overwrite an unsaved draft.
    useEffect(() => {
        if (!cacheConfig || initialized) return;
        setDraft(createCacheDraft(cacheConfig));
        setInitialized(true);
    }, [cacheConfig, initialized]);

    const editDraft = <Field extends keyof CacheDraft>(field: Field, value: CacheDraft[Field]) => {
        draftRevision.current += 1;
        setDraft((currentDraft) => {
            const nextDraft = { ...currentDraft, [field]: value };
            return field === 'address'
                ? { ...nextDraft, username: '', password: '', database: '0', tls: false, caFile: '' }
                : nextDraft;
        });
        setPreview(null);
        setFeedback(null);
        setFieldErrors({});
    };

    const runAction = async (action: CacheAction) => {
        if (pendingActionRef.current || !formReady) return;
        if (action === 'save' && !editableSource) return;
        if (action !== 'save' && draft.cacheType !== 'redis') return;

        const errors = validateCacheDraft(draft, Boolean(cacheConfig?.has_saved_connection));
        setFieldErrors(errors);
        setFeedback(null);
        if (Object.keys(errors).length > 0) {
            if ((errors.password || errors.database || errors.poolSize) && advancedDetailsRef.current) {
                advancedDetailsRef.current.open = true;
            }
            setFeedback({ message: translate('redis.validationFailed'), error: true });
            return;
        }

        const submittedRevision = draftRevision.current;
        const request = buildCacheConfigRequest(draft);
        pendingActionRef.current = action;
        setPendingAction(action);
        if (action === 'preview') setPreview(null);

        try {
            if (action === 'preview') {
                const summary = await previewCache.mutateAsync(request);
                if (draftRevision.current !== submittedRevision) return;
                setPreview(summary);
                setFeedback({ message: translate('redis.previewSuccess'), error: false });
            } else if (action === 'test') {
                const connected = await testCache.mutateAsync(request);
                if (draftRevision.current !== submittedRevision) return;
                setFeedback({
                    message: connected ? translate('redis.testSuccess') : translate('redis.testFailed'),
                    error: !connected,
                });
            } else {
                const result = await saveCache.mutateAsync(request);
                if (draftRevision.current !== submittedRevision) return;
                setPreview(null);
                setDraft((currentDraft) => ({
                    ...currentDraft, address: '', username: '', password: '', database: '0', tls: false, caFile: '',
                }));
                setFeedback({ message: translate('redis.saved'), error: false, restartNeeded: result.restart_needed });
            }
        } catch {
            // Do not echo server exceptions: they may contain a submitted credential URI.
            if (draftRevision.current !== submittedRevision) return;
            setFeedback({
                message: action === 'preview' ? translate('redis.previewFailed')
                    : action === 'test' ? translate('redis.testFailed') : translate('redis.saveFailed'),
                error: true,
            });
        } finally {
            previewCache.reset();
            testCache.reset();
            saveCache.reset();
            pendingActionRef.current = null;
            setPendingAction(null);
        }
    };

    const renderSummary = (summary: CacheRedisSummary) => translate('redis.connectionSummary', {
        value: summary.addr,
        count: summary.db,
        tls: summary.tls ? 'TLS' : translate('redis.noTls'),
    });

    return (
        <div className="space-y-4 p-4 sm:p-6" aria-busy={isLoading || busy}>
            <div className="space-y-1">
                <div className="flex items-center gap-2">
                    <Database aria-hidden="true" className="size-5 text-muted-foreground" />
                    <h2 className="text-lg font-semibold text-card-foreground">{translate('redis.title')}</h2>
                </div>
                <p className="text-xs leading-5 text-muted-foreground">{translate('redis.description')}</p>
            </div>

            {cacheConfig && (
                <div className="space-y-2 rounded-lg border border-border/30 p-3 text-xs leading-5" role="status">
                    <p>{translate('redis.runtimeSummary', {
                        backend: cacheConfig.runtime_backend === 'redis' ? 'Redis / Valkey' : translate('redis.type.memory'),
                        health: cacheConfig.runtime_healthy ? translate('redis.healthy') : translate('redis.unhealthy'),
                        tls: cacheConfig.runtime_tls ? 'TLS' : translate('redis.noTls'),
                    })}</p>
                    {knownSource && <p>{translate(`redis.source.${source}`)}</p>}
                    {cacheConfig.reconnecting && <p>{translate('redis.reconnecting')}</p>}
                    {cacheConfig.restart_needed && <p>{translate('redis.restartNotice')}</p>}
                </div>
            )}

            <div className="space-y-3 rounded-lg border border-border/30 bg-card p-3 shadow-sm sm:p-4">
                {cacheConfig && (
                    <div className="space-y-1 text-xs leading-5">
                        <h3 className="font-medium">{translate('redis.savedConnection')}</h3>
                        <p>{translate('redis.configuredType', {
                            value: cacheConfig.type === 'redis' ? 'Redis / Valkey' : translate('redis.type.memory'),
                        })}</p>
                        {cacheConfig.has_saved_connection ? (
                            <>
                                <p className="break-all">{renderSummary(cacheConfig.redis)}</p>
                                <p className="text-muted-foreground">{translate(cacheConfig.has_password ? 'redis.passwordSaved' : 'redis.noPasswordSaved')}</p>
                            </>
                        ) : <p className="text-muted-foreground">{translate('redis.noSavedConnection')}</p>}
                    </div>
                )}

                <fieldset disabled={!formReady || pendingAction === 'save'} className="min-w-0 space-y-3">
                    <legend className="sr-only">{translate('redis.title')}</legend>
                    <div className="space-y-1.5">
                        <label htmlFor={`${formId}-type`} className="text-xs text-muted-foreground">{translate('redis.type.label')}</label>
                        <select
                            id={`${formId}-type`}
                            value={draft.cacheType}
                            onChange={(event) => editDraft('cacheType', event.target.value as CacheDraft['cacheType'])}
                            className="h-10 w-full rounded-xl border border-input bg-background px-3 text-sm focus-visible:outline-2 focus-visible:outline-ring"
                        >
                            <option value="">{translate('redis.type.memory')}</option>
                            <option value="redis">{translate('redis.type.redis')}</option>
                        </select>
                    </div>

                    {draft.cacheType === 'redis' ? (
                        <>
                            <div className="space-y-1.5">
                                <label htmlFor={`${formId}-address`} className="text-xs text-muted-foreground">{translate('redis.fields.addr.label')}</label>
                                <Input
                                    id={`${formId}-address`}
                                    className="rounded-xl"
                                    type="password"
                                    autoComplete="off"
                                    spellCheck={false}
                                    required={!cacheConfig?.has_saved_connection}
                                    value={draft.address}
                                    onChange={(event) => editDraft('address', event.target.value)}
                                    placeholder={translate('redis.fields.addr.placeholder')}
                                    aria-invalid={Boolean(fieldErrors.address)}
                                    aria-describedby={`${formId}-address-hint${fieldErrors.address ? ` ${formId}-address-error` : ''}`}
                                />
                                <p id={`${formId}-address-hint`} className="text-xs leading-5 text-muted-foreground">{translate('redis.replacementHint')}</p>
                                {fieldErrors.address && <p id={`${formId}-address-error`} className="text-xs text-destructive" role="alert">{translate(`redis.validation.${fieldErrors.address}`)}</p>}
                            </div>

                            <details ref={advancedDetailsRef} className="rounded-lg border border-border/30 p-3">
                                <summary className="cursor-pointer text-xs font-medium focus-visible:outline-2 focus-visible:outline-ring">{translate('redis.advanced')}</summary>
                                <div className="mt-3 space-y-3">
                                    <p id={`${formId}-identity-hint`} className="text-xs leading-5 text-muted-foreground">{translate(urlInput ? 'redis.urlIdentityHint' : 'redis.manualIdentityHint')}</p>
                                    <fieldset disabled={manualIdentityDisabled} aria-describedby={`${formId}-identity-hint`} className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2">
                                        <legend className="sr-only">{translate('redis.manualConnection')}</legend>
                                        <div className="space-y-1.5">
                                            <label htmlFor={`${formId}-username`} className="text-xs text-muted-foreground">{translate('redis.fields.username.label')}</label>
                                            <Input id={`${formId}-username`} value={draft.username} disabled={manualIdentityDisabled} autoComplete="off" onChange={(event) => editDraft('username', event.target.value)} placeholder="default" />
                                        </div>
                                        <div className="space-y-1.5">
                                            <label htmlFor={`${formId}-password`} className="text-xs text-muted-foreground">{translate('redis.fields.password.label')}</label>
                                            <Input
                                                id={`${formId}-password`} type="password" value={draft.password} autoComplete="new-password" disabled={manualIdentityDisabled}
                                                onChange={(event) => editDraft('password', event.target.value)} placeholder={translate('redis.fields.password.placeholder')}
                                                aria-invalid={Boolean(fieldErrors.password)} aria-describedby={fieldErrors.password ? `${formId}-password-error` : `${formId}-identity-hint`}
                                            />
                                            {fieldErrors.password && <p id={`${formId}-password-error`} className="text-xs text-destructive" role="alert">{translate(`redis.validation.${fieldErrors.password}`)}</p>}
                                        </div>
                                        <div className="space-y-1.5">
                                            <label htmlFor={`${formId}-database`} className="text-xs text-muted-foreground">{translate('redis.fields.db.label')}</label>
                                            <Input
                                                id={`${formId}-database`} type="number" min={0} step={1} value={draft.database} disabled={manualIdentityDisabled}
                                                onChange={(event) => editDraft('database', event.target.value)} aria-invalid={Boolean(fieldErrors.database)}
                                                aria-describedby={fieldErrors.database ? `${formId}-database-error` : `${formId}-identity-hint`}
                                            />
                                            {fieldErrors.database && <p id={`${formId}-database-error`} className="text-xs text-destructive" role="alert">{translate(`redis.validation.${fieldErrors.database}`)}</p>}
                                        </div>
                                        <label htmlFor={`${formId}-tls`} className="flex items-center gap-2 text-xs">
                                            <input id={`${formId}-tls`} type="checkbox" checked={draft.tls} disabled={manualIdentityDisabled} onChange={(event) => editDraft('tls', event.target.checked)} />
                                            {translate('redis.tlsLabel')}
                                        </label>
                                    </fieldset>
                                    <div className="space-y-1.5">
                                        <label htmlFor={`${formId}-ca-file`} className="text-xs text-muted-foreground">{translate('redis.caFileLabel')}</label>
                                        <Input id={`${formId}-ca-file`} value={draft.caFile} disabled={!allowsCacheCertificate(draft)} onChange={(event) => editDraft('caFile', event.target.value)} placeholder="/etc/secrets/ca.pem" aria-describedby={`${formId}-ca-hint`} />
                                        <p id={`${formId}-ca-hint`} className="text-xs leading-5 text-muted-foreground">{translate('redis.tlsHint')}</p>
                                    </div>

                                    <p id={`${formId}-tuning-hint`} className="text-xs leading-5 text-muted-foreground">{translate('redis.tuningHint')}</p>
                                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                                        {(['poolSize', 'dialTimeout', 'readTimeout'] as const).map((field) => (
                                            <div key={field} className="space-y-1.5">
                                                <label htmlFor={`${formId}-${field}`} className="text-xs text-muted-foreground">{translate(`redis.fields.${field}.label`)}</label>
                                                <Input
                                                    id={`${formId}-${field}`} type={field === 'poolSize' ? 'number' : 'text'}
                                                    min={field === 'poolSize' ? 0 : undefined} step={field === 'poolSize' ? 1 : undefined}
                                                    value={draft[field]} onChange={(event) => editDraft(field, event.target.value)}
                                                    placeholder={translate(`redis.fields.${field}.placeholder`)}
                                                    aria-invalid={field === 'poolSize' && Boolean(fieldErrors.poolSize)}
                                                    aria-describedby={field === 'poolSize' && fieldErrors.poolSize ? `${formId}-poolSize-error` : `${formId}-tuning-hint`}
                                                />
                                                {field === 'poolSize' && fieldErrors.poolSize && <p id={`${formId}-poolSize-error`} className="text-xs text-destructive" role="alert">{translate(`redis.validation.${fieldErrors.poolSize}`)}</p>}
                                            </div>
                                        ))}
                                    </div>
                                    <p className="text-xs leading-5 text-muted-foreground">{translate('redis.providerHint')}</p>
                                </div>
                            </details>
                        </>
                    ) : <p className="text-xs leading-5 text-muted-foreground">{translate('redis.memoryHint')}</p>}
                </fieldset>

                <p id={`${formId}-save-hint`} className="text-xs leading-5 text-muted-foreground" role={isError ? 'alert' : 'status'}>
                    {isLoading ? translate('redis.loading') : !formReady || !knownSource ? translate('redis.loadError')
                        : editableSource ? translate('redis.saveHint') : translate('redis.readOnlyHint')}
                </p>

                <div className="flex flex-wrap gap-2">
                    <Button id={`${formId}-preview`} type="button" variant="outline" className="flex-1 rounded-xl" onClick={() => runAction('preview')} disabled={!formReady || busy || draft.cacheType !== 'redis'}>
                        {pendingAction === 'preview' && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
                        {translate('redis.previewButton')}
                    </Button>
                    <Button id={`${formId}-test`} type="button" variant="outline" className="flex-1 rounded-xl" onClick={() => runAction('test')} disabled={!formReady || busy || draft.cacheType !== 'redis'}>
                        {pendingAction === 'test' && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
                        {translate('redis.testButton')}
                    </Button>
                    <Button id={`${formId}-save`} type="button" className="flex-1 rounded-xl" onClick={() => runAction('save')} disabled={!formReady || !editableSource || busy} aria-describedby={`${formId}-save-hint`}>
                        {pendingAction === 'save' && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
                        {translate(pendingAction === 'save' ? 'redis.saving' : 'redis.saveButton')}
                    </Button>
                </div>

                {preview && (
                    <div className="space-y-1 rounded-lg border border-border/30 p-3 text-xs leading-5" role="status">
                        <h3 className="font-medium">{translate('redis.previewSummary')}</h3>
                        <p className="break-all">{renderSummary(preview)}</p>
                    </div>
                )}
                {feedback && (
                    <div className={`space-y-1 rounded-lg border p-3 text-xs leading-5 ${feedback.error ? 'border-destructive/30 text-destructive' : 'border-border/30'}`} role={feedback.error ? 'alert' : 'status'}>
                        <p>{feedback.message}</p>
                        {feedback.restartNeeded && <p>{translate('redis.restartNotice')}</p>}
                    </div>
                )}
            </div>

            <div className="space-y-1 text-xs leading-5 text-muted-foreground">
                <p>{translate('redis.persistenceHint')}</p>
                <p>{translate('redis.sharedKeysHint')}</p>
            </div>
        </div>
    );
}
