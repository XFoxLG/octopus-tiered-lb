'use client'

import { useState } from 'react'
import { useTranslations } from 'next-intl'
import { Plus, Pencil, Trash2, RefreshCw, Tags } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
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
  usePriceCategoryList,
  useCreatePriceCategory,
  useUpdatePriceCategory,
  useDeletePriceCategory,
  type ModelPriceCategory,
} from '@/api/endpoints/model'
import { useNavStore } from '@/components/modules/navbar'
import { useModelViewStore } from './view-store'
import { ConfirmDeleteDialog, EnabledBadge, PriceRuleFields } from './PriceRuleFields'
import {
  buildPriceRuleBasePayload,
  formatPriceValue,
  hasErrors,
  isPriceRuleType,
  validatePriceRuleBase,
  type PriceRuleBaseErrors,
  type PriceRuleFormBase,
} from './price-form'
import { PeakScheduleSection } from './PeakScheduleSection'

const EMPTY_FORM: PriceRuleFormBase = {
  name: '',
  rule_type: 'contains',
  rule_value: '',
  input: '',
  output: '',
  cache_read: '',
  cache_write: '',
  sort_order: '0',
  enabled: true,
}

const NO_ERRORS: PriceRuleBaseErrors = {}

export function PriceCategoriesView() {
  const t = useTranslations('model.priceCategory')
  // 校验错误 / 加载失败 / 禁用徽标等新文案走共享命名空间（见 price-form.ts 头注）。
  const tv = useTranslations('model.priceRule')
  // keep-alive：路由级切换不卸载本组件，价格查询需按「模型广场 + 分类页签可见」门控，
  // 避免停留在其他页面时持续轮询。
  const activeItem = useNavStore((s) => s.activeItem)
  const modelView = useModelViewStore((s) => s.modelView)
  const queriesEnabled = activeItem === 'model' && modelView === 'categories'

  const { data: categories, isLoading, isError, refetch } = usePriceCategoryList(queriesEnabled)
  const createMutation = useCreatePriceCategory()
  const updateMutation = useUpdatePriceCategory()
  const deleteMutation = useDeletePriceCategory()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<ModelPriceCategory | null>(null)
  const [form, setForm] = useState<PriceRuleFormBase>(EMPTY_FORM)
  // 提交尝试后才展示校验错误；此后随输入实时重算（错误消失即通过）。
  const [showErrors, setShowErrors] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ModelPriceCategory | null>(null)

  // 门控关闭且尚无缓存数据时按加载中处理（此时面板不可见，仅避免闪现空态）。
  const showLoading = queriesEnabled ? isLoading : !categories
  const showError = isError && !categories
  const errors = showErrors ? validatePriceRuleBase(form) : NO_ERRORS
  const saving = createMutation.isPending || updateMutation.isPending

  const openCreate = () => {
    setEditing(null)
    setForm(EMPTY_FORM)
    setShowErrors(false)
    setDialogOpen(true)
  }

  const openEdit = (cat: ModelPriceCategory) => {
    setEditing(cat)
    setForm({
      name: cat.name,
      rule_type: isPriceRuleType(cat.rule_type) ? cat.rule_type : 'contains',
      rule_value: cat.rule_value,
      input: formatPriceValue(cat.input),
      output: formatPriceValue(cat.output),
      cache_read: formatPriceValue(cat.cache_read),
      cache_write: formatPriceValue(cat.cache_write),
      sort_order: String(cat.sort_order ?? 0),
      enabled: cat.enabled,
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
    if (hasErrors(validatePriceRuleBase(form))) {
      setShowErrors(true)
      return
    }
    const payload = buildPriceRuleBasePayload(form)
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
              <Tags className="size-5" />
              {t('title')}
            </h2>
            <span className="rounded-full bg-muted/60 px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
              {categories?.length ?? 0}
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
          <Tags className="size-8 opacity-40" />
          <p>{tv('loadFailed')}</p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw className="mr-1.5 size-4" />
            {tv('retry')}
          </Button>
        </div>
      ) : !categories || categories.length === 0 ? (
        <div className="flex h-40 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-border text-sm text-muted-foreground">
          <Tags className="size-8 opacity-40" />
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
                  <TableHead className="hidden text-right lg:table-cell">{t('sortOrder')}</TableHead>
                  <TableHead>{t('enabled')}</TableHead>
                  <TableHead className="text-right">{t('actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {categories.map((cat) => (
                  <TableRow key={cat.id}>
                    <TableCell className="max-w-40 truncate font-medium" title={cat.name}>
                      {cat.name}
                    </TableCell>
                    <TableCell>{ruleLabel(cat.rule_type)}</TableCell>
                    <TableCell className="max-w-40 truncate font-mono text-sm" title={cat.rule_value}>
                      {cat.rule_value}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-right font-mono text-sm">{cat.input}</TableCell>
                    <TableCell className="whitespace-nowrap text-right font-mono text-sm">{cat.output}</TableCell>
                    <TableCell className="hidden whitespace-nowrap text-right font-mono text-sm md:table-cell">
                      {cat.cache_read}
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-right font-mono text-sm md:table-cell">
                      {cat.cache_write}
                    </TableCell>
                    <TableCell className="hidden text-right lg:table-cell">{cat.sort_order}</TableCell>
                    <TableCell>
                      <EnabledBadge
                        enabled={cat.enabled}
                        enabledLabel={t('enabled')}
                        disabledLabel={tv('disabled')}
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => openEdit(cat)}
                          aria-label={t('edit')}
                        >
                          <Pencil className="size-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setDeleteTarget(cat)}
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

          <fieldset disabled={saving} className="py-2">
            <PriceRuleFields
              idPrefix="pc"
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

      {/* 峰谷计费（DeepSeek 峰谷自定义入口） */}
      <PeakScheduleSection />
    </div>
  )
}
