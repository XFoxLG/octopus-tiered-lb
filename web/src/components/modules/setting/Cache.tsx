'use client';

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Database, Loader2, Check, AlertTriangle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { toast } from '@/components/common/Toast';
import { useGetCacheConfig, useTestCacheConnection, useSaveCacheConfig } from '@/api/endpoints/setting';

// CacheType 缓存后端类型：空字符串=内存（默认，向后兼容），"redis"=启用 Redis 后端。
type CacheType = '' | 'redis';

export function SettingCache() {
    const t = useTranslations('setting');

    const { data: cacheConfig, isLoading } = useGetCacheConfig();
    const testCache = useTestCacheConnection();
    const saveCache = useSaveCacheConfig();

    const [cacheType, setCacheType] = useState<CacheType>('');
    const [addr, setAddr] = useState('');
    const [password, setPassword] = useState('');
    const [username, setUsername] = useState('');
    const [db, setDb] = useState('0');
    const [poolSize, setPoolSize] = useState('');
    const [dialTimeout, setDialTimeout] = useState('');
    const [readTimeout, setReadTimeout] = useState('');
    const [tls, setTls] = useState(false);
    const [caFile, setCaFile] = useState('');
    const deploymentManaged = cacheConfig?.config_source !== 'file';

    // 挂载/配置到达时回填表单（从 config.json 的 cache 字段读取当前值）。
    useEffect(() => {
        if (!cacheConfig) return;
        setCacheType((cacheConfig.type || '') as CacheType);
        const r = cacheConfig.redis;
        setAddr(r?.addr || '');
        setPassword(r?.password || '');
        setUsername(r?.username || '');
        setDb(r?.db != null ? String(r.db) : '0');
        setPoolSize(r?.pool_size ? String(r.pool_size) : '');
        setDialTimeout(r?.dial_timeout || '');
        setReadTimeout(r?.read_timeout || '');
        setTls(r?.tls || false);
        setCaFile(r?.ca_file || '');
    }, [cacheConfig]);

    const buildRequest = () => ({
        type: cacheType,
        redis: {
            addr: addr.trim(),
            password,
            username,
            db: db.trim() === '' ? 0 : Number(db),
            pool_size: poolSize.trim() === '' ? 0 : Number(poolSize),
            dial_timeout: dialTimeout.trim(),
            read_timeout: readTimeout.trim(),
            tls,
            ca_file: caFile.trim(),
        },
    });

    const onTest = () => {
        if (cacheType !== 'redis') {
            toast.error(t('redis.testFailed', { error: t('redis.typeRedisRequired') }));
            return;
        }
        if (!addr.trim()) {
            toast.error(t('redis.testFailed', { error: t('redis.fields.addr.label') }));
            return;
        }
        testCache.mutate(buildRequest(), {
            onSuccess: () => toast.success(t('redis.testSuccess')),
            onError: (err) => toast.error(t('redis.testFailed', { error: (err as Error).message })),
        });
    };

    const onSave = () => {
        if (cacheType === 'redis' && !addr.trim()) {
            toast.error(t('redis.testFailed', { error: t('redis.fields.addr.label') }));
            return;
        }
        saveCache.mutate(buildRequest(), {
            onSuccess: () => toast.success(t('redis.saved')),
            onError: (err) => toast.error(t('redis.testFailed', { error: (err as Error).message })),
        });
    };

    return (
        <div className="space-y-5 p-4 sm:p-6">
            <div className="space-y-1">
                <div className="flex items-center gap-2">
                    <Database className="size-5 text-muted-foreground" />
                    <h2 className="text-lg font-semibold text-card-foreground">{t('redis.title')}</h2>
                </div>
                <p className="text-xs leading-5 text-muted-foreground">{t('redis.description')}</p>
            </div>

            {cacheConfig && (
                <div className="space-y-2 rounded-lg border border-border/30 p-3 text-xs leading-5" role="status">
                    <p>{t('redis.runtimeSummary', {
                        backend: cacheConfig.runtime_backend === 'redis' ? 'Redis / Valkey' : t('redis.type.memory'),
                        health: cacheConfig.runtime_healthy ? t('redis.healthy') : t('redis.unhealthy'),
                        tls: cacheConfig.runtime_tls ? 'TLS' : t('redis.noTls'),
                    })}</p>
                    <p>{t(`redis.source.${cacheConfig.config_source}`)}</p>
                    {cacheConfig.reconnecting && <p>{t('redis.reconnecting')}</p>}
                    {cacheConfig.restart_needed && <p>{t('redis.restartNotice')}</p>}
                </div>
            )}

            {deploymentManaged && (
                <div className="space-y-2 rounded-lg border border-amber-500/30 p-3 text-xs leading-5">
                    <p>{t('redis.deploymentInstructions')}</p>
                    <p className="break-all font-mono">OCTOPUS_CACHE_TYPE=redis<br />OCTOPUS_CACHE_REDIS_ADDR=host:port<br />OCTOPUS_CACHE_REDIS_TLS=true<br />OCTOPUS_CACHE_REDIS_USERNAME<br />OCTOPUS_CACHE_REDIS_PASSWORD</p>
                    <p>{t('redis.deploymentTestOnly')}</p>
                </div>
            )}

            <div className="space-y-3 rounded-lg border border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="space-y-1.5">
                    <label className="text-xs text-muted-foreground">{t('redis.type.label')}</label>
                    <select
                        value={cacheType}
                        aria-label={t('redis.type.label')}
                        onChange={(e) => setCacheType(e.target.value as CacheType)}
                        className="h-10 rounded-xl border border-input bg-background px-3 text-sm w-full"
                    >
                        <option value="">{t('redis.type.memory')}</option>
                        <option value="redis">{t('redis.type.redis')}</option>
                    </select>
                </div>

                {cacheType === 'redis' && (
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                        <div className="space-y-1.5 sm:col-span-2">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.addr.label')}</label>
                            <Input
                                className="rounded-xl"
                                aria-label={t('redis.fields.addr.label')}
                                type="password"
                                autoComplete="off"
                                value={addr}
                                onChange={(e) => setAddr(e.target.value)}
                                placeholder="host:port / rediss://host:port"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.password.label')}</label>
                            <Input
                                type="password"
                                aria-label={t('redis.fields.password.label')}
                                className="rounded-xl"
                                value={password}
                                onChange={(e) => setPassword(e.target.value)}
                                placeholder="••••••"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.username.label')}</label>
                            <Input
                                className="rounded-xl"
                                value={username}
                                aria-label={t('redis.fields.username.label')}
                                onChange={(e) => setUsername(e.target.value)}
                                placeholder="default"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.db.label')}</label>
                            <Input
                                type="number"
                                className="rounded-xl"
                                value={db}
                                aria-label={t('redis.fields.db.label')}
                                min={0}
                                step={1}
                                onChange={(e) => setDb(e.target.value)}
                                placeholder="0"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.poolSize.label')}</label>
                            <Input
                                type="number"
                                className="rounded-xl"
                                value={poolSize}
                                aria-label={t('redis.fields.poolSize.label')}
                                min={1}
                                step={1}
                                onChange={(e) => setPoolSize(e.target.value)}
                                placeholder="5"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.dialTimeout.label')}</label>
                            <Input
                                className="rounded-xl"
                                value={dialTimeout}
                                aria-label={t('redis.fields.dialTimeout.label')}
                                onChange={(e) => setDialTimeout(e.target.value)}
                                placeholder={t('redis.fields.dialTimeout.placeholder')}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <label className="text-xs text-muted-foreground">{t('redis.fields.readTimeout.label')}</label>
                            <Input
                                className="rounded-xl"
                                value={readTimeout}
                                aria-label={t('redis.fields.readTimeout.label')}
                                onChange={(e) => setReadTimeout(e.target.value)}
                                placeholder={t('redis.fields.readTimeout.placeholder')}
                            />
                        </div>
                        <label className="flex items-center gap-2 text-xs sm:col-span-2">
                            <input type="checkbox" checked={tls || addr.trim().startsWith('rediss://')} disabled={addr.trim().startsWith('rediss://')} onChange={(event) => setTls(event.target.checked)} />
                            {t('redis.tlsLabel')}
                        </label>
                        <div className="space-y-1.5 sm:col-span-2">
                            <label htmlFor="redis-ca-file" className="text-xs text-muted-foreground">{t('redis.caFileLabel')}</label>
                            <Input id="redis-ca-file" value={caFile} onChange={(event) => setCaFile(event.target.value)} placeholder="/etc/secrets/ca.pem" />
                            <p className="text-xs leading-5 text-muted-foreground">{t('redis.tlsHint')}</p>
                        </div>
                    </div>
                )}

                <div className="flex flex-col gap-2 sm:flex-row">
                    <Button
                        type="button"
                        variant="outline"
                        className="w-full sm:flex-1 rounded-xl"
                        onClick={onTest}
                        disabled={testCache.isPending || saveCache.isPending || cacheType !== 'redis'}
                    >
                        {testCache.isPending ? <Loader2 className="size-4 animate-spin" /> : <Check className="size-4" />}
                        {t('redis.testButton')}
                    </Button>
                    <Button
                        type="button"
                        className="w-full sm:flex-1 rounded-xl"
                        onClick={onSave}
                        disabled={saveCache.isPending || testCache.isPending || isLoading || deploymentManaged}
                    >
                        {saveCache.isPending ? <Loader2 className="size-4 animate-spin" /> : <Database className="size-4" />}
                        {saveCache.isPending ? t('redis.saving') : t('redis.saveButton')}
                    </Button>
                </div>

                {saveCache.data ? (
                    <div className="space-y-1 rounded-lg border border-emerald-500/20 bg-emerald-500/8 p-3 text-xs text-emerald-700 dark:text-emerald-300 break-words">
                        <div className="font-semibold text-amber-600 dark:text-amber-400">{t('redis.restartNotice')}</div>
                    </div>
                ) : null}
            </div>

            <div className="flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-xs text-amber-700 dark:text-amber-300">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                <div className="leading-5">{t('redis.restartNotice')}</div>
            </div>
        </div>
    );
}
