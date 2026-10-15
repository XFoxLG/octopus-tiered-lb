'use client';

import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Sparkles } from 'lucide-react';
import { Hint } from '@/components/ui/hint';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { useSettingList, useSetSetting } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import {
    AUTO_SOFT_SIGNAL_FIELDS,
    AUTO_STRATEGY_FIELDS,
    CONTEXT_LIMIT_LEARN_FIELDS,
    CONTEXT_PREFLIGHT_FIELDS,
    type RuntimeSettingField,
} from './runtime-settings';

// v2.9.0 布尔开关：key + 三语文案 key。缺省值按后端默认（开启）处理。
const SOFT_SIGNALS_TOGGLE = {
    key: 'auto_soft_signals_enabled',
    labelKey: 'autoStrategy.softSignals.toggle.label',
    hintKey: 'autoStrategy.softSignals.toggle.hint',
};
const CONTEXT_PRECHECK_TOGGLE = {
    key: 'relay_context_precheck_enabled',
    labelKey: 'autoStrategy.contextGuard.toggle.label',
    hintKey: 'autoStrategy.contextGuard.toggle.hint',
};
const LIMIT_LEARN_TOGGLE = {
    key: 'channel_model_limit_learn_enabled',
    labelKey: 'autoStrategy.limitLearn.toggle.label',
    hintKey: 'autoStrategy.limitLearn.toggle.hint',
};
const TOGGLE_FIELDS = [SOFT_SIGNALS_TOGGLE, CONTEXT_PRECHECK_TOGGLE, LIMIT_LEARN_TOGGLE];

export function SettingAutoStrategy() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [values, setValues] = useState<Record<string, string>>({});
    const initialValues = useRef<Record<string, string>>({});

    useEffect(() => {
        if (!settings) return;

        const trackedKeys = [
            ...AUTO_STRATEGY_FIELDS.map((field) => field.key),
            ...AUTO_SOFT_SIGNAL_FIELDS.map((field) => field.key),
            ...CONTEXT_PREFLIGHT_FIELDS.map((field) => field.key),
            ...CONTEXT_LIMIT_LEARN_FIELDS.map((field) => field.key),
            ...TOGGLE_FIELDS.map((field) => field.key),
        ];
        const toggleKeys = new Set(TOGGLE_FIELDS.map((field) => field.key));
        const nextValues = trackedKeys.reduce<Record<string, string>>((acc, key) => {
            const stored = settings.find((item) => item.key === key)?.value;
            // 开关缺省视为开启（与后端默认值一致）；数字项缺省留空，由 placeholder 提示。
            acc[key] = stored ?? (toggleKeys.has(key) ? 'true' : '');
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
                    initialValues.current = {
                        ...initialValues.current,
                        [key]: value,
                    };
                }
            }
        );
    };

    const handleToggle = (key: string, checked: boolean) => {
        const value = checked ? 'true' : 'false';
        setValues((prev) => ({ ...prev, [key]: value }));
        setSetting.mutate(
            { key, value },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    initialValues.current = {
                        ...initialValues.current,
                        [key]: value,
                    };
                }
            }
        );
    };

    const renderNumberField = (field: RuntimeSettingField) => (
        <div key={field.key} className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
            <div className="flex flex-col gap-1">
                <span className="text-sm font-medium">
                    {t(field.labelKey)}
                    <Hint text={field.hintKey ? t(field.hintKey) : undefined} />
                </span>
            </div>
            <Input
                type="number"
                min={field.min}
                max={field.max}
                value={values[field.key] ?? ''}
                onChange={(e) => setValues((prev) => ({ ...prev, [field.key]: e.target.value }))}
                onBlur={() => handleSave(field.key)}
                placeholder={t(field.placeholderKey)}
                className="w-32 rounded-xl"
            />
        </div>
    );

    const renderToggleField = (field: { key: string; labelKey: string; hintKey: string }) => (
        <div key={field.key} className="flex flex-col gap-3 rounded-lg border-border/30 bg-card p-4 shadow-sm md:flex-row md:items-center md:justify-between">
            <div className="flex min-w-0 flex-col gap-1">
                <span className="text-sm font-medium">
                    {t(field.labelKey)}
                    <Hint text={t(field.hintKey)} />
                </span>
            </div>
            <Switch
                aria-label={t(field.labelKey)}
                checked={values[field.key] === 'true'}
                onCheckedChange={(checked) => handleToggle(field.key, checked)}
            />
        </div>
    );

    return (
        <div className="rounded-xl border-border/35 bg-card p-6 space-y-5 text-card-foreground shadow-md">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Sparkles className="h-5 w-5" />
                {t('autoStrategy.title')}
                <Hint text={t('autoStrategy.hint')} />
            </h2>

            <div className="space-y-4">
                {AUTO_STRATEGY_FIELDS.map(renderNumberField)}
            </div>

            {/* v2.9.0：软信号罚分——把假流式/空输出/流中断/截断这些「被记成功」的行为
                变成可统计的软信号，参与 Auto 打分。截断默认 0（只记录不罚分）。 */}
            <div className="space-y-4 rounded-lg border-border/30 bg-card p-4 shadow-sm">
                <div className="text-sm font-semibold text-card-foreground">
                    {t('autoStrategy.softSignals.title')}
                    <Hint text={t('autoStrategy.softSignals.hint')} />
                </div>
                <div className="space-y-4">
                    {renderToggleField(SOFT_SIGNALS_TOGGLE)}
                    {AUTO_SOFT_SIGNAL_FIELDS.map(renderNumberField)}
                </div>
            </div>

            {/* v2.9.0：请求前上下文预检——已知上限且估算输入超限时直接排除候选。 */}
            <div className="space-y-4 rounded-lg border-border/30 bg-card p-4 shadow-sm">
                <div className="text-sm font-semibold text-card-foreground">
                    {t('autoStrategy.contextGuard.title')}
                    <Hint text={t('autoStrategy.contextGuard.hint')} />
                </div>
                <div className="space-y-4">
                    {renderToggleField(CONTEXT_PRECHECK_TOGGLE)}
                    {CONTEXT_PREFLIGHT_FIELDS.map(renderNumberField)}
                </div>
            </div>

            {/* v2.9.0：渠道×模型上限自动学习（manual 人工值永不被覆盖）。 */}
            <div className="space-y-4 rounded-lg border-border/30 bg-card p-4 shadow-sm">
                <div className="text-sm font-semibold text-card-foreground">
                    {t('autoStrategy.limitLearn.title')}
                    <Hint text={t('autoStrategy.limitLearn.hint')} />
                </div>
                <div className="space-y-4">
                    {renderToggleField(LIMIT_LEARN_TOGGLE)}
                    {CONTEXT_LIMIT_LEARN_FIELDS.map(renderNumberField)}
                </div>
            </div>
        </div>
    );
}
