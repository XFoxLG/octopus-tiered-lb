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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
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

type RuleType = 'exact' | 'prefix' | 'contains'

// 分钟 → "HH:MM"（如 540 → "09:00"）
function minutesToHHMM(m: number): string {
  const h = Math.floor(m / 60)
  const mm = m % 60
  return `${String(h).padStart(2, '0')}:${String(mm).padStart(2, '0')}`
}

// "HH:MM" → 分钟（空/非法 → 0）
function hhmmToMinutes(v: string): number {
  const m = /^(\d{1,2}):(\d{2})$/.exec(v.trim())
  if (!m) return 0
  const h = Number(m[1])
  const mm = Number(m[2])
  if (h > 23 || mm > 59) return 0
  return h * 60 + mm
}

interface FormState {
  name: string
  rule_type: RuleType
  rule_value: string
  input: string
  output: string
  cache_read: string
  cache_write: string
  off_peak_mul: string
  weekend_off_peak: boolean
  w1_start: string
  w1_end: string
  w2_start: string
  w2_end: string
  sort_order: string
  enabled: boolean
}

const EMPTY_FORM: FormState = {
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

function formatPrice(v: number): string {
  return String(v ?? 0)
}

function parsePrice(v: string): number {
  const n = Number(v)
  return Number.isFinite(n) ? n : 0
}

function windowLabel(s: ModelPriceSchedule, t: (key: string) => string): string {
  const w1 = s.window1_start < s.window1_end ? `${minutesToHHMM(s.window1_start)}-${minutesToHHMM(s.window1_end)}` : null
  const w2 = s.window2_start < s.window2_end ? `${minutesToHHMM(s.window2_start)}-${minutesToHHMM(s.window2_end)}` : null
  if (!w1 && !w2) return t('noWindow')
  return [w1, w2].filter(Boolean).join(' / ')
}

export function PeakScheduleSection() {
  const t = useTranslations('model.peakSchedule')
  const { data: schedules, isLoading } = usePriceScheduleList()
  const createMutation = useCreatePriceSchedule()
  const updateMutation = useUpdatePriceSchedule()
  const deleteMutation = useDeletePriceSchedule()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<ModelPriceSchedule | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)

  const openCreate = () => {
    setEditing(null)
    setForm(EMPTY_FORM)
    setDialogOpen(true)
  }

  const openEdit = (s: ModelPriceSchedule) => {
    setEditing(s)
    setForm({
      name: s.name,
      rule_type: s.rule_type as RuleType,
      rule_value: s.rule_value,
      input: formatPrice(s.input),
      output: formatPrice(s.output),
      cache_read: formatPrice(s.cache_read),
      cache_write: formatPrice(s.cache_write),
      off_peak_mul: String(s.off_peak_mul ?? 0.5),
      weekend_off_peak: s.weekend_off_peak,
      w1_start: minutesToHHMM(s.window1_start),
      w1_end: minutesToHHMM(s.window1_end),
      w2_start: minutesToHHMM(s.window2_start),
      w2_end: minutesToHHMM(s.window2_end),
      sort_order: String(s.sort_order ?? 0),
      enabled: s.enabled,
    })
    setDialogOpen(true)
  }

  const handleDelete = (id: number) => {
    if (window.confirm(t('confirmDelete'))) {
      deleteMutation.mutate(id, {
        onSuccess: () => toast.success(t('toastDeleted')),
        onError: (e: Error) => toast.error(e.message || t('toastError')),
      })
    }
  }

  const handleSubmit = () => {
    const payload = {
      name: form.name.trim(),
      rule_type: form.rule_type,
      rule_value: form.rule_value.trim(),
      input: parsePrice(form.input),
      output: parsePrice(form.output),
      cache_read: parsePrice(form.cache_read),
      cache_write: parsePrice(form.cache_write),
      off_peak_mul: parsePrice(form.off_peak_mul),
      weekend_off_peak: form.weekend_off_peak,
      window1_start: hhmmToMinutes(form.w1_start),
      window1_end: hhmmToMinutes(form.w1_end),
      window2_start: hhmmToMinutes(form.w2_start),
      window2_end: hhmmToMinutes(form.w2_end),
      sort_order: Number.parseInt(form.sort_order || '0', 10),
      enabled: form.enabled,
    }
    if (editing) {
      updateMutation.mutate(
        { ...payload, id: editing.id },
        {
          onSuccess: () => {
            toast.success(t('toastSaved'))
            setDialogOpen(false)
          },
          onError: (e: Error) => toast.error(e.message || t('toastError')),
        },
      )
    } else {
      createMutation.mutate(payload, {
        onSuccess: () => {
          toast.success(t('toastSaved'))
          setDialogOpen(false)
        },
        onError: (e: Error) => toast.error(e.message || t('toastError')),
      })
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

      {isLoading ? (
        <div className="flex h-32 items-center justify-center rounded-2xl border border-border bg-card">
          <RefreshCw className="size-5 animate-spin text-muted-foreground" />
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
                  <TableHead className="text-right">{t('cacheRead')}</TableHead>
                  <TableHead className="text-right">{t('cacheWrite')}</TableHead>
                  <TableHead className="text-right">{t('offPeakMul')}</TableHead>
                  <TableHead>{t('window')}</TableHead>
                  <TableHead className="text-right">{t('sortOrder')}</TableHead>
                  <TableHead>{t('enabled')}</TableHead>
                  <TableHead className="text-right">{t('actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {schedules.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="font-medium">{s.name}</TableCell>
                    <TableCell>{ruleLabel(s.rule_type)}</TableCell>
                    <TableCell className="font-mono text-sm">{s.rule_value}</TableCell>
                    <TableCell className="text-right font-mono text-sm">{s.input}</TableCell>
                    <TableCell className="text-right font-mono text-sm">{s.output}</TableCell>
                    <TableCell className="text-right font-mono text-sm">{s.cache_read}</TableCell>
                    <TableCell className="text-right font-mono text-sm">{s.cache_write}</TableCell>
                    <TableCell className="text-right font-mono text-sm">×{s.off_peak_mul}</TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-xs">
                      {windowLabel(s, t)}
                      {s.weekend_off_peak && (
                        <Badge variant="outline" className="ml-1.5 border-sky-400/50 text-sky-500 dark:text-sky-400">
                          {t('weekendOffPeakBadge')}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right">{s.sort_order}</TableCell>
                    <TableCell>
                      {s.enabled ? (
                        <Badge variant="default">{t('enabled')}</Badge>
                      ) : (
                        <Badge variant="secondary">{t('missing')}</Badge>
                      )}
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
                          onClick={() => handleDelete(s.id)}
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

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? t('edit') : t('add')}</DialogTitle>
            <DialogDescription>{t('description')}</DialogDescription>
          </DialogHeader>

          <div className="grid gap-4 py-2">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="ps-name">{t('name')} *</Label>
                <Input
                  id="ps-name"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder={t('namePlaceholder')}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-sort">
                  {t('sortOrder')}
                  <Hint text={t('sortOrderHint')} />
                </Label>
                <Input
                  id="ps-sort"
                  type="number"
                  value={form.sort_order}
                  onChange={(e) => setForm({ ...form, sort_order: e.target.value })}
                />
              </div>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="ps-rule-type">{t('ruleType')} *</Label>
                <Select
                  value={form.rule_type}
                  onValueChange={(v) => setForm({ ...form, rule_type: v as RuleType })}
                >
                  <SelectTrigger id="ps-rule-type" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="exact">{t('exact')}</SelectItem>
                    <SelectItem value="prefix">{t('prefix')}</SelectItem>
                    <SelectItem value="contains">{t('contains')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-rule-value">{t('ruleValue')} *</Label>
                <Input
                  id="ps-rule-value"
                  value={form.rule_value}
                  onChange={(e) => setForm({ ...form, rule_value: e.target.value })}
                  placeholder={t('ruleValuePlaceholder')}
                  className="font-mono"
                />
              </div>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <div className="grid gap-2">
                <Label htmlFor="ps-input">
                  {t('input')}
                  <Hint text={t('priceHint')} />
                </Label>
                <Input
                  id="ps-input"
                  type="number"
                  step="any"
                  min="0"
                  value={form.input}
                  onChange={(e) => setForm({ ...form, input: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-output">{t('output')}</Label>
                <Input
                  id="ps-output"
                  type="number"
                  step="any"
                  min="0"
                  value={form.output}
                  onChange={(e) => setForm({ ...form, output: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-cache-read">{t('cacheRead')}</Label>
                <Input
                  id="ps-cache-read"
                  type="number"
                  step="any"
                  min="0"
                  value={form.cache_read}
                  onChange={(e) => setForm({ ...form, cache_read: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-cache-write">{t('cacheWrite')}</Label>
                <Input
                  id="ps-cache-write"
                  type="number"
                  step="any"
                  min="0"
                  value={form.cache_write}
                  onChange={(e) => setForm({ ...form, cache_write: e.target.value })}
                />
              </div>
            </div>

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
                />
              </div>
              <div className="grid gap-2">
                <Label>
                  {t('window')}
                  <Hint text={t('windowHint')} />
                </Label>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <div className="grid gap-2">
                <Label htmlFor="ps-w1s">{t('window1Start')}</Label>
                <Input
                  id="ps-w1s"
                  type="time"
                  step="60"
                  value={form.w1_start}
                  onChange={(e) => setForm({ ...form, w1_start: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-w1e">{t('window1End')}</Label>
                <Input
                  id="ps-w1e"
                  type="time"
                  step="60"
                  value={form.w1_end}
                  onChange={(e) => setForm({ ...form, w1_end: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-w2s">{t('window2Start')}</Label>
                <Input
                  id="ps-w2s"
                  type="time"
                  step="60"
                  value={form.w2_start}
                  onChange={(e) => setForm({ ...form, w2_start: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="ps-w2e">{t('window2End')}</Label>
                <Input
                  id="ps-w2e"
                  type="time"
                  step="60"
                  value={form.w2_end}
                  onChange={(e) => setForm({ ...form, w2_end: e.target.value })}
                />
              </div>
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
              <div className="flex items-center gap-2">
                <Switch
                  id="ps-enabled"
                  checked={form.enabled}
                  onCheckedChange={(checked) => setForm({ ...form, enabled: checked })}
                />
                <Label htmlFor="ps-enabled">{t('enabled')}</Label>
              </div>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {t('cancel')}
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={createMutation.isPending || updateMutation.isPending}
            >
              {createMutation.isPending || updateMutation.isPending ? t('saving') : t('save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
