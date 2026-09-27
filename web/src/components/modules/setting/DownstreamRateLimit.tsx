'use client';

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Gauge } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Hint } from '@/components/ui/hint';
import { SettingKey, useSettingList, useSetSetting } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';

// 全站下游限速:请求频率 + 总并发。只作用于 4 条转发路由,管理 API/登录/日志
// 不受影响。两个设置默认 0 = 关闭,升级后行为不变。
const FIELDS = [
    {
        key: SettingKey.GlobalRateLimitRPM,
        labelKey: 'downstreamRateLimit.rpm.label',
        hintKey: 'downstreamRateLimit.rpm.hint',
        placeholderKey: 'downstreamRateLimit.rpm.placeholder',
        min: 0,
    },
    {
        key: SettingKey.GlobalMaxConcurrency,
        labelKey: 'downstreamRateLimit.concurrency.label',
        hintKey: 'downstreamRateLimit.concurrency.hint',
        placeholderKey: 'downstreamRateLimit.concurrency.placeholder',
        min: 0,
    },
] as const;

export function SettingDownstreamRateLimit() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [values, setValues] = useState<Record<string, string>>({});
    const initialValues = useRef<Record<string, string>>({});

    useEffect(() => {
        if (!settings) return;
        const nextValues = FIELDS.reduce<Record<string, string>>((acc, field) => {
            acc[field.key] = settings.find((item) => item.key === field.key)?.value ?? '0';
            return acc;
        }, {});
        queueMicrotask(() => setValues(nextValues));
        initialValues.current = nextValues;
    }, [settings]);

    const handleSave = (key: string) => {
        const value = values[key] ?? '';
        if (value === initialValues.current[key]) return;
        setSetting.mutate(
            { key, value },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    initialValues.current = { ...initialValues.current, [key]: value };
                },
            },
        );
    };

    return (
        <div className="space-y-5 rounded-xl border-border/35 bg-card p-6 text-card-foreground shadow-md">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <Gauge className="h-5 w-5" />
                {t('downstreamRateLimit.title')}
            </h2>
            <p className="text-sm text-muted-foreground">{t('downstreamRateLimit.description')}</p>

            <div className="space-y-4">
                {FIELDS.map((field) => (
                    <div
                        key={field.key}
                        className="flex min-w-0 flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between"
                    >
                        <div className="min-w-0 flex flex-col gap-1">
                            <span className="text-sm font-medium">
                                {t(field.labelKey)}
                                <Hint text={t(field.hintKey)} />
                            </span>
                        </div>
                        <Input
                            type="number"
                            min={field.min}
                            value={values[field.key] ?? ''}
                            onChange={(e) => setValues((prev) => ({ ...prev, [field.key]: e.target.value }))}
                            onBlur={() => handleSave(field.key)}
                            placeholder={t(field.placeholderKey)}
                            className="w-full rounded-xl md:w-48"
                        />
                    </div>
                ))}
            </div>
        </div>
    );
}
