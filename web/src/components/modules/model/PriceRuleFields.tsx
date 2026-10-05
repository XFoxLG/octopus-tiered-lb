'use client';

import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { Hint } from '@/components/ui/hint';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
  PRICE_RULE_TYPES,
  type PriceRuleBaseErrors,
  type PriceRuleFormBase,
} from './price-form';

/**
 * 价格分类（PriceCategoriesView）与峰谷计费（PeakScheduleSection）共用的
 * 表单字段块 / 删除确认 / 状态徽标。
 *
 * 校验错误文案取自 `model.priceRule` 命名空间（错误值为该命名空间的 i18n key，
 * 见 price-form.ts）；字段标签等既有文案由调用方从各自命名空间传入
 * （两个命名空间下这些 key 的取值一致）。
 */

/** PriceRuleFields 需要的既有文案（由调用方从自身命名空间取值传入）。 */
export interface PriceRuleFieldLabels {
  name: string;
  namePlaceholder: string;
  ruleType: string;
  ruleValue: string;
  ruleValuePlaceholder: string;
  exact: string;
  prefix: string;
  contains: string;
  sortOrder: string;
  sortOrderHint: string;
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  priceHint: string;
  enabled: string;
}

const RULE_TYPE_LABEL_KEYS = {
  exact: 'exact',
  prefix: 'prefix',
  contains: 'contains',
} as const;

/** 共用表单字段块：名称、优先级、规则类型、规则值、四项价格、启用开关。 */
export function PriceRuleFields({
  idPrefix,
  base,
  errors,
  showErrors,
  onChange,
  labels,
}: {
  idPrefix: string;
  base: PriceRuleFormBase;
  errors: PriceRuleBaseErrors;
  showErrors: boolean;
  onChange: (patch: Partial<PriceRuleFormBase>) => void;
  labels: PriceRuleFieldLabels;
}) {
  const tv = useTranslations('model.priceRule');

  const errorText = (key: string | undefined) =>
    showErrors && key ? <p className="mt-1 text-xs text-destructive">{tv(key)}</p> : null;

  const priceFields: {
    key: 'input' | 'output' | 'cache_read' | 'cache_write';
    id: string;
    label: string;
    hint?: string;
  }[] = [
    { key: 'input', id: 'input', label: labels.input, hint: labels.priceHint },
    { key: 'output', id: 'output', label: labels.output },
    { key: 'cache_read', id: 'cache-read', label: labels.cacheRead },
    { key: 'cache_write', id: 'cache-write', label: labels.cacheWrite },
  ];

  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="grid gap-2">
          <Label htmlFor={`${idPrefix}-name`}>{labels.name} *</Label>
          <Input
            id={`${idPrefix}-name`}
            value={base.name}
            onChange={(e) => onChange({ name: e.target.value })}
            placeholder={labels.namePlaceholder}
            aria-invalid={showErrors && errors.name ? true : undefined}
          />
          {errorText(errors.name)}
        </div>
        <div className="grid gap-2">
          <Label htmlFor={`${idPrefix}-sort`}>
            {labels.sortOrder}
            <Hint text={labels.sortOrderHint} />
          </Label>
          <Input
            id={`${idPrefix}-sort`}
            type="number"
            step="1"
            value={base.sort_order}
            onChange={(e) => onChange({ sort_order: e.target.value })}
            aria-invalid={showErrors && errors.sort_order ? true : undefined}
          />
          {errorText(errors.sort_order)}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="grid gap-2">
          <Label htmlFor={`${idPrefix}-rule-type`}>{labels.ruleType} *</Label>
          <Select
            value={base.rule_type}
            onValueChange={(v) => onChange({ rule_type: v as PriceRuleFormBase['rule_type'] })}
          >
            <SelectTrigger id={`${idPrefix}-rule-type`} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PRICE_RULE_TYPES.map((type) => (
                <SelectItem key={type} value={type}>
                  {labels[RULE_TYPE_LABEL_KEYS[type]]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="grid gap-2">
          <Label htmlFor={`${idPrefix}-rule-value`}>{labels.ruleValue} *</Label>
          <Input
            id={`${idPrefix}-rule-value`}
            value={base.rule_value}
            onChange={(e) => onChange({ rule_value: e.target.value })}
            placeholder={labels.ruleValuePlaceholder}
            className="font-mono"
            aria-invalid={showErrors && errors.rule_value ? true : undefined}
          />
          {errorText(errors.rule_value)}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {priceFields.map((field) => (
          <div key={field.key} className="grid gap-2">
            <Label htmlFor={`${idPrefix}-${field.id}`}>
              {field.label}
              {field.hint ? <Hint text={field.hint} /> : null}
            </Label>
            <Input
              id={`${idPrefix}-${field.id}`}
              type="number"
              step="any"
              min="0"
              value={base[field.key]}
              onChange={(e) => onChange({ [field.key]: e.target.value } as Partial<PriceRuleFormBase>)}
              aria-invalid={showErrors && errors[field.key] ? true : undefined}
            />
            {errorText(errors[field.key])}
          </div>
        ))}
      </div>

      <div className="flex items-center gap-2">
        <Switch
          id={`${idPrefix}-enabled`}
          checked={base.enabled}
          onCheckedChange={(checked) => onChange({ enabled: checked })}
        />
        <Label htmlFor={`${idPrefix}-enabled`}>{labels.enabled}</Label>
      </div>
    </div>
  );
}

/** 统一的启用/禁用徽标：禁用是显式状态，而不是"缺失"。 */
export function EnabledBadge({
  enabled,
  enabledLabel,
  disabledLabel,
}: {
  enabled: boolean;
  enabledLabel: string;
  disabledLabel: string;
}) {
  return enabled ? (
    <Badge variant="default">{enabledLabel}</Badge>
  ) : (
    <Badge variant="outline" className="text-muted-foreground">
      {disabledLabel}
    </Badge>
  );
}

/**
 * 删除确认 AlertDialog（替代 window.confirm）。pending 时锁定全部按钮，
 * 确认后由调用方在 onSuccess 里关闭（onOpenChange(false)）。
 */
export function ConfirmDeleteDialog({
  open,
  onOpenChange,
  title,
  description,
  pending,
  onConfirm,
  labels,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  pending: boolean;
  onConfirm: () => void;
  labels: { cancel: string; confirm: string; deleting: string };
}) {
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!pending) onOpenChange(next);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>{labels.cancel}</AlertDialogCancel>
          <Button variant="destructive" disabled={pending} onClick={onConfirm}>
            {pending ? labels.deleting : labels.confirm}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
