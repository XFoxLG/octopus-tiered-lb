'use client';

import { Check, Loader, Trash2 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { cn } from '@/lib/utils';
import { PRICE_FIELDS, PRICE_FIELD_LABEL_KEYS, type PriceDraft, type PriceField } from './pricing';

type ModelEditDialogProps = {
    modelName: string;
    brandColor: string;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    editValues: PriceDraft;
    /** 校验失败的字段列表；非空时输入框标 aria-invalid 并内联提示。 */
    invalidFields: readonly PriceField[];
    isPending: boolean;
    onChange: (next: PriceDraft) => void;
    onSave: () => void;
    // peakBilling 为 true 时在价格输入上方显示 DeepSeek 峰谷计费说明
    // （目录价为高峰价，空闲减半）。可选，默认不显示。
    peakBilling?: boolean;
};

export function ModelEditDialog({
    modelName,
    brandColor,
    open,
    onOpenChange,
    editValues,
    invalidFields,
    isPending,
    onChange,
    onSave,
    peakBilling = false,
}: ModelEditDialogProps) {
    const t = useTranslations('model.overlay');
    const hasInvalidFields = invalidFields.length > 0;

    // 提交进行中禁止 Escape / 点遮罩关闭，避免请求返回后状态弹窗错位。
    const handleOpenChange = (next: boolean) => {
        if (!next && isPending) return;
        onOpenChange(next);
    };

    return (
        <Dialog open={open} onOpenChange={handleOpenChange}>
            <DialogContent className="max-h-[calc(100dvh-2rem)] max-w-md overflow-y-auto">
                <DialogHeader>
                    <DialogTitle className="break-all text-left text-base leading-tight sm:text-lg">{modelName}</DialogTitle>
                    <DialogDescription className="text-left">{t('priceHint')}</DialogDescription>
                </DialogHeader>
                <form
                    onSubmit={(event) => {
                        event.preventDefault();
                        if (isPending) return;
                        onSave();
                    }}
                    className="grid gap-3"
                >
                    {peakBilling && (
                        <p className="text-[0.68rem] leading-relaxed text-amber-600 dark:text-amber-400 sm:text-xs">
                            {t('peakBillingHint')}
                        </p>
                    )}
                    <div className="grid grid-cols-2 gap-2 sm:gap-3">
                        {PRICE_FIELDS.map((field) => {
                            const invalid = invalidFields.includes(field);
                            return (
                                <label key={field} className="grid gap-1 text-[0.68rem] text-muted-foreground sm:gap-1.5 sm:text-xs">
                                    {t(PRICE_FIELD_LABEL_KEYS[field])}
                                    <Input
                                        type="number"
                                        step="any"
                                        min="0"
                                        value={editValues[field]}
                                        onChange={(event) => onChange({ ...editValues, [field]: event.target.value })}
                                        aria-invalid={invalid || undefined}
                                        disabled={isPending}
                                        className="h-9 rounded-lg border-border/25 bg-card text-xs sm:h-10 sm:text-sm"
                                    />
                                </label>
                            );
                        })}
                    </div>

                    {hasInvalidFields && (
                        <p className="text-xs text-destructive" role="alert">
                            {t('priceInvalid')}
                        </p>
                    )}

                    <DialogFooter className="mt-1 gap-2">
                        <Button
                            type="button"
                            variant="outline"
                            onClick={() => onOpenChange(false)}
                            disabled={isPending}
                            className="flex-1 sm:flex-1"
                        >
                            {t('cancel')}
                        </Button>
                        <Button
                            type="submit"
                            disabled={isPending}
                            className="flex-1 text-white sm:flex-1"
                            style={brandColor ? { backgroundColor: brandColor } : undefined}
                        >
                            {isPending ? <Loader className="size-4 animate-spin" /> : <Check className="size-4" />}
                            {t('save')}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

type ModelDeleteDialogProps = {
    modelName: string;
    open: boolean;
    isPending: boolean;
    onOpenChange: (open: boolean) => void;
    onConfirm: () => void;
};

export function ModelDeleteDialog({
    modelName,
    open,
    isPending,
    onOpenChange,
    onConfirm,
}: ModelDeleteDialogProps) {
    const t = useTranslations('model.overlay');

    return (
        <AlertDialog
            open={open}
            onOpenChange={(next) => {
                if (!next && isPending) return;
                onOpenChange(next);
            }}
        >
            <AlertDialogContent size="sm">
                <AlertDialogHeader>
                    <AlertDialogTitle>{t('deleteTitle')}</AlertDialogTitle>
                    <AlertDialogDescription>{t('deleteDescription', { name: modelName })}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel disabled={isPending}>{t('cancel')}</AlertDialogCancel>
                    <AlertDialogAction
                        variant="destructive"
                        disabled={isPending}
                        className={cn('gap-1.5', isPending && 'cursor-not-allowed opacity-50')}
                        // 阻止 AlertDialogAction 的默认关闭：成功与否由 hook 在回调里收口，
                        // 失败时弹窗保持打开，用户可直接重试。
                        onClick={(event) => {
                            event.preventDefault();
                            if (isPending) return;
                            onConfirm();
                        }}
                    >
                        {isPending ? <Loader className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                        {isPending ? t('deleting') : t('confirmDelete')}
                    </AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    );
}
