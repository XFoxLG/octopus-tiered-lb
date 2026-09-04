'use client';

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Monitor, Globe, Clock, Shield, Network } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Hint } from '@/components/ui/hint';
import { useSettingList, useSetSetting, SettingKey } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';

export function SettingSystem() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [proxyUrl, setProxyUrl] = useState('');
    const [publicApiBaseUrl, setPublicApiBaseUrl] = useState('');
    const [statsSaveInterval, setStatsSaveInterval] = useState('');
    const [corsAllowOrigins, setCorsAllowOrigins] = useState('');
    const [trustedProxies, setTrustedProxies] = useState('');

    const initialProxyUrl = useRef('');
    const initialPublicApiBaseUrl = useRef('');
    const initialStatsSaveInterval = useRef('');
    const initialCorsAllowOrigins = useRef('');
    const initialTrustedProxies = useRef('');

    useEffect(() => {
        if (settings) {
            const proxy = settings.find(s => s.key === SettingKey.ProxyURL);
            const publicApi = settings.find(s => s.key === SettingKey.PublicAPIBaseURL);
            const interval = settings.find(s => s.key === SettingKey.StatsSaveInterval);
            const cors = settings.find(s => s.key === SettingKey.CORSAllowOrigins);
            const tp = settings.find(s => s.key === SettingKey.TrustedProxies);
            if (tp) {
                queueMicrotask(() => setTrustedProxies(tp.value));
                initialTrustedProxies.current = tp.value;
            }
            if (proxy) {
                queueMicrotask(() => setProxyUrl(proxy.value));
                initialProxyUrl.current = proxy.value;
            }
            if (publicApi) {
                queueMicrotask(() => setPublicApiBaseUrl(publicApi.value));
                initialPublicApiBaseUrl.current = publicApi.value;
            }
            if (interval) {
                queueMicrotask(() => setStatsSaveInterval(interval.value));
                initialStatsSaveInterval.current = interval.value;
            }
            if (cors) {
                queueMicrotask(() => setCorsAllowOrigins(cors.value));
                initialCorsAllowOrigins.current = cors.value;
            }
        }
    }, [settings]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                if (key === SettingKey.ProxyURL) {
                    initialProxyUrl.current = value;
                } else if (key === SettingKey.PublicAPIBaseURL) {
                    initialPublicApiBaseUrl.current = value;
                } else if (key === SettingKey.StatsSaveInterval) {
                    initialStatsSaveInterval.current = value;
                } else if (key === SettingKey.CORSAllowOrigins) {
                    initialCorsAllowOrigins.current = value;
                } else if (key === SettingKey.TrustedProxies) {
                    initialTrustedProxies.current = value;
                }
            }
        });
    };

    // CORS uses the same onBlur save pattern as other settings.
    // The value is a comma-separated list of origins (or "*" for all).

    return (
        <div className="rounded-xl border-border/35 bg-card p-4 sm:p-6 space-y-4 sm:space-y-5 text-card-foreground shadow-md ">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Monitor className="h-5 w-5" />
                {t('system')}
            </h2>

            {/* 代理地址 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Globe className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium">{t('proxyUrl.label')}</span>
                </div>
                <Input
                    value={proxyUrl}
                    onChange={(e) => setProxyUrl(e.target.value)}
                    onBlur={() => handleSave('proxy_url', proxyUrl, initialProxyUrl.current)}
                    placeholder={t('proxyUrl.placeholder')}
                    className="w-full min-w-0 rounded-xl md:w-48"
                />
            </div>

            {/* 公开 API 基础地址 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Globe className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium">
                        {t('publicApiBaseUrl.label')}
                        <Hint text={t('publicApiBaseUrl.hint')} />
                    </span>
                </div>
                <Input
                    value={publicApiBaseUrl}
                    onChange={(e) => setPublicApiBaseUrl(e.target.value)}
                    onBlur={() => handleSave(SettingKey.PublicAPIBaseURL, publicApiBaseUrl, initialPublicApiBaseUrl.current)}
                    placeholder={t('publicApiBaseUrl.placeholder')}
                    className="w-full min-w-0 rounded-xl md:w-72"
                />
            </div>

            {/* 统计保存周期 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Clock className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium">{t('statsSaveInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={statsSaveInterval}
                    onChange={(e) => setStatsSaveInterval(e.target.value)}
                    onBlur={() => handleSave('stats_save_interval', statsSaveInterval, initialStatsSaveInterval.current)}
                    placeholder={t('statsSaveInterval.placeholder')}
                    className="w-full min-w-0 rounded-xl md:w-48"
                />
            </div>

            {/* CORS 跨域白名单 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Shield className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium">
                        {t('corsAllowOrigins.label')}
                        <Hint
                            text={`${t('corsAllowOrigins.hint')}\n${t('corsAllowOrigins.example')}`}
                        />
                    </span>
                </div>
                <Input
                    value={corsAllowOrigins}
                    onChange={(e) => setCorsAllowOrigins(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CORSAllowOrigins, corsAllowOrigins, initialCorsAllowOrigins.current)}
                    placeholder={t('corsAllowOrigins.example')}
                    className="w-full min-w-0 rounded-xl md:w-48"
                />
            </div>

            {/* 可信反向代理 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Network className="h-5 w-5 text-muted-foreground shrink-0" />
                    <span className="text-sm font-medium">
                        {t('trustedProxies.label')}
                        <Hint text={t('trustedProxies.hint')} />
                    </span>
                </div>
                <div className="flex w-full flex-col gap-1.5 md:w-72">
                    <Input
                        value={trustedProxies}
                        onChange={(e) => setTrustedProxies(e.target.value)}
                        onBlur={() => handleSave(SettingKey.TrustedProxies, trustedProxies, initialTrustedProxies.current)}
                        placeholder={t('trustedProxies.placeholder')}
                        className="w-full min-w-0 rounded-xl"
                    />
                    <p className="text-xs text-amber-600 dark:text-amber-500">{t('trustedProxies.restartNotice')}</p>
                </div>
            </div>
        </div>
    );
}
