'use client';

import { useState } from 'react';
import { useCreateModel } from '@/api/endpoints/model';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Field, FieldLabel, FieldGroup } from '@/components/ui/field';
import { Hint } from '@/components/ui/hint';
import {
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useTranslations } from 'next-intl';
import { toast } from '@/components/common/Toast';
import { parsePriceDraft, PRICE_FIELDS, PRICE_FIELD_LABEL_KEYS, type PriceDraft, type PriceField } from './pricing';

const EMPTY_FORM: PriceDraft & { name: string } = {
    name: '',
    input: '',
    output: '',
    cache_read: '',
    cache_write: '',
};

const PRICE_INPUT_CLASS = 'rounded-xl aria-invalid:border-destructive';

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('model.create');
    const tToast = useTranslations('model.toast');
    const createModel = useCreateModel();

    const [formData, setFormData] = useState(EMPTY_FORM);
    // 首次提交后才展示校验错误，输入过程中不打扰；修正后错误即时消失（随渲染重算）。
    const [showValidation, setShowValidation] = useState(false);

    const trimmedName = formData.name.trim();
    // 严格解析：留空即报错（免费需显式输入 0），负数 / 非法输入标记对应字段，
    // 不再被 parseFloat || 0 静默清零。
    // 纯函数、开销极小，随渲染重算即可（修正后错误即时消失）。
    const { invalidFields: invalidPriceFields } = parsePriceDraft(formData);
    const nameInvalid = showValidation && trimmedName === '';
    const priceInvalid = showValidation && invalidPriceFields.length > 0;

    const updateField = (field: keyof typeof EMPTY_FORM, value: string) => {
        setFormData((prev) => ({ ...prev, [field]: value }));
    };

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setShowValidation(true);
        if (!trimmedName || invalidPriceFields.length > 0) return;

        const { prices } = parsePriceDraft(formData);
        createModel.mutate({
            name: trimmedName,
            ...prices,
        }, {
            onSuccess: () => {
                setFormData(EMPTY_FORM);
                setShowValidation(false);
                setIsOpen(false);
                toast.success(tToast('created'));
            }
            // 失败提示由全局 MutationCache onError 统一弹出，这里不重复 toast。
        });
    };

    const renderPriceField = (field: PriceField) => {
        const id = `model-${field.replace(/_/g, '-')}`;
        const invalid = showValidation && invalidPriceFields.includes(field);
        return (
            <Field key={field}>
                <FieldLabel htmlFor={id}>
                    {t(PRICE_FIELD_LABEL_KEYS[field])}
                    {field === 'input' && <Hint text={t('priceHint')} />}
                </FieldLabel>
                <Input
                    id={id}
                    type="number"
                    step="any"
                    min="0"
                    value={formData[field]}
                    onChange={(e) => updateField(field, e.target.value)}
                    aria-invalid={invalid || undefined}
                    className={PRICE_INPUT_CLASS}
                />
            </Field>
        );
    };

    return (
        <div className="w-full max-w-full md:max-w-xl">
            <MorphingDialogTitle>
                <header className="mb-5 flex items-center justify-between">
                    <h2 className="text-2xl font-bold text-card-foreground">{t('title')}</h2>
                    <MorphingDialogClose
                        className="relative right-0 top-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 },
                        }}
                    />
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit} noValidate>
                    <FieldGroup className="gap-4">
                        <Field>
                            <FieldLabel htmlFor="model-name">{t('name')}</FieldLabel>
                            <Input
                                id="model-name"
                                value={formData.name}
                                onChange={(e) => updateField('name', e.target.value)}
                                autoComplete="off"
                                aria-invalid={nameInvalid || undefined}
                                aria-describedby={nameInvalid ? 'model-name-error' : undefined}
                                className="rounded-xl aria-invalid:border-destructive"
                            />
                            {nameInvalid && (
                                <p id="model-name-error" role="alert" className="text-xs text-destructive">
                                    {t('nameRequired')}
                                </p>
                            )}
                        </Field>
                        <div className="grid grid-cols-2 gap-4">
                            {PRICE_FIELDS.map(renderPriceField)}
                        </div>

                        {priceInvalid && (
                            <p role="alert" className="text-xs text-destructive">
                                {t('priceInvalid')}
                            </p>
                        )}

                        <Button
                            type="submit"
                            disabled={createModel.isPending}
                            className="w-full rounded-xl h-11"
                        >
                            {createModel.isPending ? t('submitting') : t('submit')}
                        </Button>
                    </FieldGroup>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}
