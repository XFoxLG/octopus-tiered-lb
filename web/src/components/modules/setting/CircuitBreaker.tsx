'use client';

import { useEffect, useState, useRef } from 'react';
import { useTranslations } from 'next-intl';
import { Zap, Hash, Timer, TimerOff, TimerReset } from 'lucide-react';
import { Hint } from '@/components/ui/hint';
import { Input } from '@/components/ui/input';
import { useSettingList, useSetSetting, SettingKey } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';

export function SettingCircuitBreaker() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [threshold, setThreshold] = useState('');
    const [cooldown, setCooldown] = useState('');
    const [maxCooldown, setMaxCooldown] = useState('');
    const [probeTimeout, setProbeTimeout] = useState('');

    const initialThreshold = useRef('');
    const initialCooldown = useRef('');
    const initialMaxCooldown = useRef('');
    const initialProbeTimeout = useRef('');

    useEffect(() => {
        if (settings) {
            const th = settings.find(s => s.key === SettingKey.CircuitBreakerThreshold);
            const cd = settings.find(s => s.key === SettingKey.CircuitBreakerCooldown);
            const mcd = settings.find(s => s.key === SettingKey.CircuitBreakerMaxCooldown);
            const pt = settings.find(s => s.key === SettingKey.CircuitBreakerHalfOpenProbeTimeout);
            if (th) {
                queueMicrotask(() => setThreshold(th.value));
                initialThreshold.current = th.value;
            }
            if (cd) {
                queueMicrotask(() => setCooldown(cd.value));
                initialCooldown.current = cd.value;
            }
            if (mcd) {
                queueMicrotask(() => setMaxCooldown(mcd.value));
                initialMaxCooldown.current = mcd.value;
            }
            if (pt) {
                queueMicrotask(() => setProbeTimeout(pt.value));
                initialProbeTimeout.current = pt.value;
            }
        }
    }, [settings]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                if (key === SettingKey.CircuitBreakerThreshold) {
                    initialThreshold.current = value;
                } else if (key === SettingKey.CircuitBreakerCooldown) {
                    initialCooldown.current = value;
                } else if (key === SettingKey.CircuitBreakerMaxCooldown) {
                    initialMaxCooldown.current = value;
                } else if (key === SettingKey.CircuitBreakerHalfOpenProbeTimeout) {
                    initialProbeTimeout.current = value;
                }
            }
        });
    };

    return (
        <div className="rounded-xl border-border/35 bg-card p-6 space-y-5 text-card-foreground shadow-md ">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Zap className="h-5 w-5" />
                {t('circuitBreaker.title')}
                <Hint text={t('circuitBreaker.hint')} />
            </h2>

            {/* 熔断触发阈值 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Hash className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.threshold.label')}</span>
                </div>
                <Input
                    type="number"
                    value={threshold}
                    onChange={(e) => setThreshold(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerThreshold, threshold, initialThreshold.current)}
                    placeholder={t('circuitBreaker.threshold.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 基础冷却时间 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <Timer className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.cooldown.label')}</span>
                </div>
                <Input
                    type="number"
                    value={cooldown}
                    onChange={(e) => setCooldown(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerCooldown, cooldown, initialCooldown.current)}
                    placeholder={t('circuitBreaker.cooldown.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 最大冷却时间 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <TimerOff className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.maxCooldown.label')}</span>
                </div>
                <Input
                    type="number"
                    value={maxCooldown}
                    onChange={(e) => setMaxCooldown(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerMaxCooldown, maxCooldown, initialMaxCooldown.current)}
                    placeholder={t('circuitBreaker.maxCooldown.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* HalfOpen 探测超时 */}
            <div className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
                <div className="flex items-center gap-3">
                    <TimerReset className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.probeTimeout.label')}</span>
                </div>
                <Input
                    type="number"
                    value={probeTimeout}
                    onChange={(e) => setProbeTimeout(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerHalfOpenProbeTimeout, probeTimeout, initialProbeTimeout.current)}
                    placeholder={t('circuitBreaker.probeTimeout.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>
        </div>
    );
}
