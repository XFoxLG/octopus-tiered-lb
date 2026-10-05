'use client'

import { useState } from 'react'
import { useTranslations } from 'next-intl'
import { Plus, Pencil, Trash2, RefreshCw, Zap } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { Hint } from '@/components/ui/hint'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  usePriceScheduleList,
  useCreatePriceSchedule,
  useUpdatePriceSchedule,
  useDeletePriceSchedule,
  type ModelPriceSchedule,
} from '@/api/endpoints/model'
import { useNavStore } from '@/components/modules/navbar'
import { useModelViewStore } from './view-store'
import { ConfirmDeleteDialog, EnabledBadge, PriceRuleFields } from './PriceRuleFields'
import {
  buildPriceRuleBasePayload,
  formatPriceValue,
  formatWindowsLabel,
  hasErrors,
  isPriceRuleType,
  parsePriceInput,
  resolveScheduleWindows,
  validatePriceRuleBase,
  windowErrorKey,
  windowToForm,
  type PriceRuleFormBase,
  type PriceScheduleErrors,
  type WindowInput,
} from './price-form'

// 峰谷规则表单 = 共用基础字段 + 倍率 / 周末开关 / 两段北京时间窗口。
// 窗口留空（两端全空）= 关闭该时段（后端 0,0 语义），两窗全关 = 全天空闲。
interface ScheduleFormState extends PriceRuleFormBase {
  off_peak_mul: string
  weekend_off_peak: boolean
  w1_start: string
  w1_end: string
  w2_start: string
  w2_end: string
}

const EMPTY_FORM: ScheduleFormState = {
  name: '',
  rule_type: 'contains',
  rule_value: '',
  input: '',
  output: '',
  cache_read: '',
  cache_write: '',
  off_peak_mul: '0.5',
  weekend_off_peak: true,
  w1_start: '09:00',
  w1_end: '12:00',
  w2_start: '14:00',
  w2_end: '18:00',
  sort_order: '0',
  enabled: true,
}

const NO_ERRORS: PriceScheduleErrors = {}

function formWindows(form: ScheduleFormState): { w1: WindowInput; w2: WindowInput } {
  return {
    w1: { start: form.w1_start, end: form.w1_end },
    w2: { start: form.w2_start, end: form.w2_end },
  }
}

export function PeakScheduleSection() {
  const t = useTranslations('model.peakSchedule')
  // 校验错误 / 加载失败 / 禁用徽标等新文案走共享命名空间（见 price-form.ts 头注）。
  const tv = useTranslations('model.priceRule')
  // keep-alive：路由级切换不卸载本组件，峰谷查询需按「模型广场 + 分类页签可见」门控，
  // 避免停留在其他页面时持续轮询。
  const activeItem = useNavStore((s) => s.activeItem)
  const modelView = useModelViewStore((s) => s.modelView)
  const queriesEnabled = activeItem === 'model' && modelView === 'categories'

  const { data: schedules, isLoading, isError, refetch } = usePriceScheduleList(queriesEnabled)
  const createMutation = useCreatePriceSchedule()
  const updateMutation = useUpdatePriceSchedule()
  const deleteMutation = useDeletePriceSchedule()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<ModelPriceSchedule | null>(null)
  const [form, setForm] = useState<ScheduleFormState>(EMPTY_FORM)
  // 提交尝试后才展示校验错误；此后随输入实时重算（错误消失即通过）。
  const [showErrors, setShowErrors] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ModelPriceSchedule | null>(null)

  // 门控关闭且尚无缓存数据时按加载中处理（此时面板不可见，仅避免闪现空态）。
  const showLoading = queriesEnabled ? isLoading : !schedules
  const showError = isError && !schedules
  const saving = createMutation.isPending || updateMutation.isPending

  // 实时校验结果：错误展示与两窗重叠提示（重叠后端允许，仅提示不阻断）。
  const baseErrors = validatePriceRuleBase(form)
  const mul = parsePriceInput(form.off_peak_mul)
  const { w1, w2 } = formWindows(form)
  const windowResult = resolveScheduleWindows(w1, w2)
  const errors: PriceScheduleErrors = showErrors
    ? {
        ...baseErrors,
        off_peak_mul: mul === null ? 'invalidNumber' : mul < 0 ? 'nonNegativeRequired' : undefined,
        ...(windowResult.ok ? {} : windowResult.errors),
      }
    : NO_ERRORS
  const overlapWarn = windowResult.ok && windowResult.overlap

  const openCreate = () => {
    setEditing(null)
    setForm(EMPTY_FORM)
    setShowErrors(false)
    setDialogOpen(true)
  }

  const openEdit = (s: ModelPriceSchedule) => {
    setEditing(s)
    const w1 = windowToForm({ start: s.window1_start, end: s.window1_end })
    const w2 = windowToForm({ start: s.window2_start, end: s.window2_end })
    setForm({
      name: s.name,
      rule_type: isPriceRuleType(s.rule_type) ? s.rule_type : 'contains',
      rule_value: s.rule_value,
      input: formatPriceValue(s.input),
      output: formatPriceValue(s.output),
      cache_read: formatPriceValue(s.cache_read),
      cache_write: formatPriceValue(s.cache_write),
      off_peak_mul: formatPriceValue(s.off_peak_mul ?? 0.5),
      weekend_off_peak: s.weekend_off_peak,
      w1_start: w1.start,
      w1_end: w1.end,
      w2_start: w2.start,
      w2_end: w2.end,
      sort_order: String(s.sort_order ?? 0),
      enabled: s.enabled,
    })
    setShowErrors(false)
    setDialogOpen(true)
  }

  const handleDelete = () => {
    if (!deleteTarget || deleteMutation.isPending) return
    deleteMutation.mutate(deleteTarget.id, {
      onSuccess: () => {
        setDeleteTarget(null)
        toast.success(t('toastDeleted'))
      },
    })
  }

  const handleSubmit = () => {
    if (saving) return
    if (hasErrors(baseErrors) || mul === null || mul < 0 || !windowResult.ok) {
      setShowErrors(true)
      return
    }
    const payload = {
      ...buildPriceRuleBasePayload(form),
      off_peak_mul: mul,
      weekend_off_peak: form.weekend_off_peak,
      window1_start: windowResult.w1.start,
      window1_end: windowResult.w1.end,
      window2_start: windowResult.w2.start,
      window2_end: windowResult.w2.end,
    }
    const options = {
      onSuccess: () => {
        toast.success(t('toastSaved'))
        setDialogOpen(false)
      },
    }
    if (editing) {
      updateMutation.mutate({ ...payload, id: editing.id }, options)
    } else {
      createMutation.mutate(payload, options)
    }
  }

  const ruleLabel = (type: string) => {
    switch (type) {
      case 'exact':
        return <Badge variant="default">{t('exact')}</Badge>
      case 'prefix':
        return <Badge variant="secondary">{t('prefix')}</Badge>
      default:
        return <Badge variant="outline">{t('contains')}</Badge>
    }
  }

  return (
    <div className="space-y-3">
      <section className="rounded-2xl border border-border bg-card p-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-2">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
              <Zap className="size-5 text-amber-500" />
              {t('title')}
            </h2>
            <span className="rounded-full bg-muted/60 px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
              {schedules?.length ?? 0}
            </span>
          </div>
          <Button onClick={openCreate}>
            <Plus className="mr-1.5 size-4" />
            {t('add')}
          </Button>
        </div>
        <p className="mt-3 text-xs text-muted-foreground">{t('description')}</p>
      </section>

      {showLoading ? (
        <div className="flex h-32 items-center justify-center rounded-2xl border border-border bg-card">
          <RefreshCw className="size-5 animate-spin text-muted-foreground" />
        </div>
      ) : showError ? (
        <div className="flex h-40 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-destructive/40 text-sm text-muted-foreground">
          <Zap className="size-8 opacity-40" />
          <p>{tv('loadFailed')}</p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw className="mr-1.5 size-4" />
            {tv('retry')}
          </Button>
        </div>
      ) : !schedules || schedules.length === 0 ? (
        <div className="flex h-40 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-border text-sm text-muted-foreground">
          <Zap className="size-8 opacity-40" />
          {t('empty')}
        </div>
      ) : (
        <section className="overflow-hidden rounded-2xl border border-border bg-card">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('name')}</TableHead>
                  <TableHead>{t('ruleType')}</TableHead>
                  <TableHead>{t('ruleValue')}</TableHead>
                  <TableHead className="text-right">{t('input')}</TableHead>
                  <TableHead className="text-right">{t('output')}</TableHead>
                  <TableHead className="hidden text-right md:table-cell">{t('cacheRead')}</TableHead>
                  <TableHead className="hidden text-right md:table-cell">{t('cacheWrite')}</TableHead>
                  <TableHead className="text-right">{t('offPeakMul')}</TableHead>
                  <TableHead>
                    {t('window')}
                    <Hint text={t('windowHint')} />
                  </TableHead>
                  <TableHead className="hidden text-right lg:table-cell">{t('sortOrder')}</TableHead>
                  <TableHead>{t('enabled')}</TableHead>
                  <TableHead className="text-right">{t('actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {schedules.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="max-w-40 truncate font-medium" title={s.name}>
                      {s.name}
                    </TableCell>
                    <TableCell>{ruleLabel(s.rule_type)}</TableCell>
                    <TableCell className="max-w-40 truncate font-mono text-sm" title={s.rule_value}>
                      {s.rule_value}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-right font-mono text-sm">{s.input}</TableCell>
                    <TableCell className="whitespace-nowrap text-right font-mono text-sm">{s.output}</TableCell>
                    <TableCell className="hidden whitespace-nowrap text-right font-mono text-sm md:table-cell">
                      {s.cache_read}
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-right font-mono text-sm md:table-cell">
                      {s.cache_write}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-right font-mono text-sm">×{s.off_peak_mul}</TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-xs">
                      {formatWindowsLabel(
                        { start: s.window1_start, end: s.window1_end },
                        { start: s.window2_start, end: s.window2_end },
                      ) ?? t('noWindow')}
                      {s.weekend_off_peak && (
                        <Badge variant="outline" className="ml-1.5 border-sky-400/50 text-sky-500 dark:text-sky-400">
                          {t('weekendOffPeakBadge')}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="hidden text-right lg:table-cell">{s.sort_order}</TableCell>
                    <TableCell>
                      <EnabledBadge
                        enabled={s.enabled}
                        enabledLabel={t('enabled')}
                        disabledLabel={tv('disabled')}
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => openEdit(s)}
                          aria-label={t('edit')}
                        >
                          <Pencil className="size-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setDeleteTarget(s)}
                          aria-label={t('delete')}
                        >
                          <Trash2 className="size-4 text-destructive" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </section>
      )}

      <Dialog open={dialogOpen} onOpenChange={(open) => { if (!saving) setDialogOpen(open) }}>
        <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? t('edit') : t('add')}</DialogTitle>
            <DialogDescription>{t('description')}</DialogDescription>
          </DialogHeader>

          <fieldset disabled={saving} className="grid gap-4 py-2">
            <PriceRuleFields
              idPrefix="ps"
              base={form}
              errors={errors}
              showErrors={showErrors}
              onChange={(patch) => setForm({ ...form, ...patch })}
              labels={{
                name: t('name'),
                namePlaceholder: t('namePlaceholder'),
                ruleType: t('ruleType'),
                ruleValue: t('ruleValue'),
                ruleValuePlaceholder: t('ruleValuePlaceholder'),
                exact: t('exact'),
                prefix: t('prefix'),
                contains: t('contains'),
                sortOrder: t('sortOrder'),
                sortOrderHint: t('sortOrderHint'),
                input: t('input'),
                output: t('output'),
                cacheRead: t('cacheRead'),
                cacheWrite: t('cacheWrite'),
                priceHint: t('priceHint'),
                enabled: t('enabled'),
              }}
            />

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="ps-mul">
                  {t('offPeakMul')}
                  <Hint text={t('offPeakMulHint')} />
                </Label>
                <Input
                  id="ps-mul"
                  type="number"
                  step="0.05"
                  min="0"
                  value={form.off_peak_mul}
                  onChange={(e) => setForm({ ...form, off_peak_mul: e.target.value })}
                  aria-invalid={showErrors && errors.off_peak_mul ? true : undefined}
                />
                {showErrors && errors.off_peak_mul ? (
                  <p className="mt-1 text-xs text-destructive">{tv(errors.off_peak_mul)}</p>
                ) : null}
              </div>
            </div>

            <div className="grid gap-2">
              <Label>
                {t('window')}
                <Hint text={t('windowHint')} />
              </Label>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <div className="grid gap-2">
                  <Label htmlFor="ps-w1s">{t('window1Start')}</Label>
                  <Input
                    id="ps-w1s"
                    type="text"
                    inputMode="text"
                    placeholder="HH:MM"
                    value={form.w1_start}
                    onChange={(e) => setForm({ ...form, w1_start: e.target.value })}
                    aria-invalid={showErrors && errors.w1 ? true : undefined}
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="ps-w1e">{t('window1End')}</Label>
                  <Input
                    id="ps-w1e"
                    type="text"
                    inputMode="text"
                    placeholder="HH:MM"
                    value={form.w1_end}
                    onChange={(e) => setForm({ ...form, w1_end: e.target.value })}
                    aria-invalid={showErrors && errors.w1 ? true : undefined}
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="ps-w2s">{t('window2Start')}</Label>
                  <Input
                    id="ps-w2s"
                    type="text"
                    inputMode="text"
                    placeholder="HH:MM"
                    value={form.w2_start}
                    onChange={(e) => setForm({ ...form, w2_start: e.target.value })}
                    aria-invalid={showErrors && errors.w2 ? true : undefined}
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="ps-w2e">{t('window2End')}</Label>
                  <Input
                    id="ps-w2e"
                    type="text"
                    inputMode="text"
                    placeholder="HH:MM"
                    value={form.w2_end}
                    onChange={(e) => setForm({ ...form, w2_end: e.target.value })}
                    aria-invalid={showErrors && errors.w2 ? true : undefined}
                  />
                </div>
              </div>
              {showErrors && (errors.w1 || errors.w2) ? (
                <div className="space-y-1" role="alert">
                  {errors.w1 ? (
                    <p className="text-xs text-destructive">
                      {t('window1Start')}: {tv(windowErrorKey(errors.w1))}
                    </p>
                  ) : null}
                  {errors.w2 ? (
                    <p className="text-xs text-destructive">
                      {t('window2Start')}: {tv(windowErrorKey(errors.w2))}
                    </p>
                  ) : null}
                </div>
              ) : null}
              {overlapWarn ? (
                <p className="text-xs text-amber-600 dark:text-amber-400" role="status">
                  {tv('windowOverlapWarn')}
                </p>
              ) : null}
            </div>

            <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
              <div className="flex items-center gap-2">
                <Switch
                  id="ps-weekend-off-peak"
                  checked={form.weekend_off_peak}
                  onCheckedChange={(checked) => setForm({ ...form, weekend_off_peak: checked })}
                />
                <Label htmlFor="ps-weekend-off-peak">
                  {t('weekendOffPeak')}
                  <Hint text={t('weekendOffPeakHint')} />
                </Label>
              </div>
            </div>
          </fieldset>

          <DialogFooter>
            <Button variant="outline" disabled={saving} onClick={() => setDialogOpen(false)}>
              {t('cancel')}
            </Button>
            <Button onClick={handleSubmit} disabled={saving}>
              {saving ? t('saving') : t('save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t('confirmDeleteTitle')}
        description={deleteTarget ? t('confirmDelete') : ''}
        pending={deleteMutation.isPending}
        onConfirm={handleDelete}
        labels={{ cancel: t('cancel'), confirm: t('delete'), deleting: t('deleting') }}
      />
    </div>
  )
}
