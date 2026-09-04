'use client';

import { useState, useEffect } from 'react';
import { useTranslations } from 'next-intl';
import { Layers, Plus, Trash2, ChevronLeft, Pencil, FlaskConical, RefreshCw, KeyRound, Upload, Download, RotateCcw as RecoverIcon, Clock as TempUnschedIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { toast } from '@/components/common/Toast';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogFooter,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Hint } from '@/components/ui/hint';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import {
    usePoolList,
    usePoolAccounts,
    useCreatePool,
    useDeletePool,
    useDeletePoolAccount,
    useTestPoolAccount,
    useFetchPoolQuota,
    useRefreshPoolToken,
    useImportPoolAccounts,
    useRecoverPoolAccount,
    useTempUnschedPoolAccount,
    useBatchPoolAccounts,
    useExportPoolAccounts,
    type AccountPool,
    type PoolAccount,
} from '@/api/endpoints/pool';
import { AccountFormDialog } from './AccountFormDialog';
import { POOL_PLATFORM_OPTIONS } from './type-options';

export function Pool() {
    const [selectedPool, setSelectedPool] = useState<AccountPool | null>(null);

    if (selectedPool) {
        return <PoolDetail pool={selectedPool} onBack={() => setSelectedPool(null)} />;
    }
    return <PoolList onSelect={setSelectedPool} />;
}

function PoolList({ onSelect }: { onSelect: (pool: AccountPool) => void }) {
    const t = useTranslations('pool');
    const { data: pools, isLoading, error } = usePoolList();
    const createPool = useCreatePool();
    const deletePool = useDeletePool();
    const [dialogOpen, setDialogOpen] = useState(false);
    const [form, setForm] = useState({ name: '', description: '', strategy: 'ewma', default_concurrency: 1, cooldown_base_sec: 300 });

    // OAuth 授权回跳：URL 带 pool_id 时自动选中对应池子（后端 302 到 /pool?oauth=...&pool_id=N）。
    // 等待池子列表加载完成后处理一次并清理参数。
    useEffect(() => {
        if (typeof window === 'undefined') return;
        const params = new URLSearchParams(window.location.search);
        const poolIdStr = params.get('pool_id');
        if (!poolIdStr) return;
        if (!pools) return; // 列表未加载，等数据到达后再处理
        const url = new URL(window.location.href);
        url.searchParams.delete('pool_id');
        window.history.replaceState({}, '', url.toString());
        const target = pools.find((p) => p.id === Number(poolIdStr));
        if (target) onSelect(target);
    }, [pools, onSelect]);

    if (isLoading) return <LoadingState />;
    if (error) return <ErrorState message={String(error)} />;

    const handleCreate = () => {
        createPool.mutate(form, {
            onSuccess: () => { setDialogOpen(false); setForm({ name: '', description: '', strategy: 'ewma', default_concurrency: 1, cooldown_base_sec: 300 }); toast.success(t('created')); },
            onError: (e) => toast.error(String(e)),
        });
    };

    return (
        <div className="space-y-4 p-4">
            <div className="flex items-center justify-between">
                <h2 className="text-xl font-semibold flex items-center gap-2">
                    <Layers className="h-5 w-5" />
                    {t('title')}
                </h2>
                <Button onClick={() => setDialogOpen(true)} size="sm">
                    <Plus className="h-4 w-4 mr-1" />
                    {t('create')}
                </Button>
            </div>

            <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
                {(pools ?? []).map((pool) => (
                    <div
                        key={pool.id}
                        className="rounded-lg border p-4 cursor-pointer hover:border-primary/50 transition-colors"
                        onClick={() => onSelect(pool)}
                    >
                        <div className="flex items-center justify-between">
                            <span className="font-medium">{pool.name}</span>
                            <div className="flex items-center gap-1">
                                <Badge variant={pool.enabled ? 'default' : 'secondary'}>
                                    {pool.enabled ? t('enabled') : t('disabled')}
                                </Badge>
                                <Button
                                    variant="ghost" size="icon" className="h-7 w-7"
                                    onClick={(e) => { e.stopPropagation(); deletePool.mutate(pool.id, { onSuccess: () => toast.success(t('deleted')), onError: (e) => toast.error(String(e)) }); }}
                                >
                                    <Trash2 className="h-3.5 w-3.5" />
                                </Button>
                            </div>
                        </div>
                        {pool.description && <p className="text-sm text-muted-foreground mt-1">{pool.description}</p>}
                        <div className="flex items-center gap-3 mt-2 text-xs text-muted-foreground">
                            <span>{t('strategy')}: {pool.strategy}</span>
                            <span>{t('concurrency')}: {pool.default_concurrency}</span>
                        </div>
                    </div>
                ))}
            </div>

            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t('create')}</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4">
                        <div>
                            <Label>{t('name')}</Label>
                            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
                        </div>
                        <div>
                            <Label>{t('description')}</Label>
                            <Input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
                        </div>
                        <div>
                            <Label>{t('strategy')}</Label>
                            <Select value={form.strategy} onValueChange={(v) => setForm({ ...form, strategy: v })}>
                                <SelectTrigger><SelectValue /></SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="ewma">EWMA</SelectItem>
                                    <SelectItem value="round_robin">Round Robin</SelectItem>
                                    <SelectItem value="random">Random</SelectItem>
                                    <SelectItem value="least_loaded">Least Loaded</SelectItem>
                                    <SelectItem value="tiered_adaptive">Tiered Adaptive</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="grid grid-cols-2 gap-4">
                            <div>
                                <Label>{t('concurrency')}</Label>
                                <Input type="number" value={form.default_concurrency} onChange={(e) => setForm({ ...form, default_concurrency: Number(e.target.value) })} />
                            </div>
                            <div>
                                <Label>{t('cooldown')}</Label>
                                <Input type="number" value={form.cooldown_base_sec} onChange={(e) => setForm({ ...form, cooldown_base_sec: Number(e.target.value) })} />
                            </div>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button onClick={handleCreate} disabled={!form.name || createPool.isPending}>{t('create')}</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}

// 平台标签展示。
function PlatformBadge({ platform }: { platform: string }) {
    const opt = POOL_PLATFORM_OPTIONS.find((o) => o.value === platform);
    return <Badge variant="outline" className="text-xs">{opt?.label || platform}</Badge>;
}

// 解析 quota JSON 字符串展示用量。
function QuotaCell({ quota }: { quota: string }) {
    if (!quota) return <span className="text-muted-foreground">-</span>;
    let q: { used?: number; total?: number } | null = null;
    try {
        q = JSON.parse(quota);
    } catch {
        return <span className="text-xs text-muted-foreground">-</span>;
    }
    const used = Number(q?.used) || 0;
    const total = Number(q?.total) || 0;
    if (total <= 0) return <span className="text-xs text-muted-foreground">{used}</span>;
    const pct = Math.min(100, (used / total) * 100);
    const color = pct > 90 ? 'text-destructive' : pct > 70 ? 'text-amber-600' : 'text-muted-foreground';
    return (
        <div className="text-xs">
            <span className={color}>{used} / {total}</span>
            <div className="mt-0.5 h-1 w-16 overflow-hidden rounded bg-muted">
                <div className="h-full bg-primary" style={{ width: `${pct}%` }} />
            </div>
        </div>
    );
}

function PoolDetail({ pool, onBack }: { pool: AccountPool; onBack: () => void }) {
    const t = useTranslations('pool');
    const { data: accounts, isLoading, error } = usePoolAccounts(pool.id);
    const deleteAccount = useDeletePoolAccount(pool.id);
    const testAccount = useTestPoolAccount(pool.id);
    const fetchQuota = useFetchPoolQuota(pool.id);
    const refreshToken = useRefreshPoolToken(pool.id);
    const importAccounts = useImportPoolAccounts();
    const recoverAccount = useRecoverPoolAccount(pool.id);
    const tempUnsched = useTempUnschedPoolAccount(pool.id);
    const batch = useBatchPoolAccounts(pool.id);
    const exportAccounts = useExportPoolAccounts(pool.id);
    const [dialogOpen, setDialogOpen] = useState(false);
    const [editingAccount, setEditingAccount] = useState<PoolAccount | null>(null);
    const [importOpen, setImportOpen] = useState(false);
    const [importText, setImportText] = useState('');
    const [now] = useState(() => Math.floor(Date.now() / 1000));
    const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
    const [tempUnschedOpen, setTempUnschedOpen] = useState(false);
    const [tempUnschedTarget, setTempUnschedTarget] = useState<PoolAccount | null>(null);
    const [tempUnschedMinutes, setTempUnschedMinutes] = useState('10');
    const [tempUnschedReason, setTempUnschedReason] = useState('');

    if (isLoading) return <LoadingState />;
    if (error) return <ErrorState message={String(error)} />;

    const openCreate = () => { setEditingAccount(null); setDialogOpen(true); };
    const openEdit = (acct: PoolAccount) => { setEditingAccount(acct); setDialogOpen(true); };

    const handleImport = () => {
        importAccounts.mutate(
            { poolId: pool.id, accounts: importText },
            {
                onSuccess: (res) => { setImportOpen(false); setImportText(''); toast.success(t('importSuccess', { count: res.imported })); },
                onError: (e) => toast.error(String(e)),
            },
        );
    };

    const handleExport = () => {
        exportAccounts.mutate(undefined, {
            onSuccess: (data) => {
                const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = `pool-${pool.name}-accounts-${Date.now()}.json`;
                document.body.appendChild(a);
                a.click();
                document.body.removeChild(a);
                URL.revokeObjectURL(url);
                toast.success(t('exportSuccess', { count: data.length }));
            },
            onError: (e) => toast.error(String(e)),
        });
    };

    const handleOpenTempUnsched = (acct: PoolAccount) => {
        setTempUnschedTarget(acct);
        setTempUnschedMinutes('10');
        setTempUnschedReason('');
        setTempUnschedOpen(true);
    };
    const handleConfirmTempUnsched = () => {
        if (!tempUnschedTarget) return;
        tempUnsched.mutate(
            { accountId: tempUnschedTarget.id, minutes: Number(tempUnschedMinutes) || 0, reason: tempUnschedReason },
            {
                onSuccess: () => { setTempUnschedOpen(false); toast.success(t('tempUnschedApplied')); },
                onError: (e) => toast.error(String(e)),
            },
        );
    };

    const accounts_ = accounts ?? [];
    const allSelected = accounts_.length > 0 && selectedIds.size === accounts_.length;
    const toggleSelectAll = () => {
        if (allSelected) setSelectedIds(new Set());
        else setSelectedIds(new Set(accounts_.map((a) => a.id)));
    };
    const toggleSelect = (id: number) => {
        const next = new Set(selectedIds);
        if (next.has(id)) next.delete(id); else next.add(id);
        setSelectedIds(next);
    };

    const selected = Array.from(selectedIds);

    return (
        <div className="space-y-4 p-4">
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                    <Button variant="ghost" size="icon" onClick={onBack}><ChevronLeft className="h-4 w-4" /></Button>
                    <h2 className="text-xl font-semibold">{pool.name}</h2>
                    <Badge variant={pool.enabled ? 'default' : 'secondary'}>{pool.enabled ? t('enabled') : t('disabled')}</Badge>
                </div>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm" onClick={() => setImportOpen(true)}>
                        <Upload className="h-4 w-4 mr-1" />
                        {t('importAccounts')}
                    </Button>
                    <Button variant="outline" size="sm" onClick={handleExport} disabled={exportAccounts.isPending}>
                        <Download className="h-4 w-4 mr-1" />
                        {t('exportAccounts')}
                    </Button>
                    <Button onClick={openCreate} size="sm">
                        <Plus className="h-4 w-4 mr-1" />
                        {t('addAccount')}
                    </Button>
                </div>
            </div>

            {selectedIds.size > 0 && (
                <div className="flex items-center gap-2 rounded-lg border bg-muted/40 p-2">
                    <span className="text-sm">{t('selectedCount', { count: selectedIds.size })}</span>
                    <Button
                        variant="outline" size="sm"
                        disabled={batch.refresh.isPending}
                        onClick={() => batch.refresh.mutate(selected, {
                            onSuccess: (res) => toast.success(t('batchRefreshResult', { ok: res.ok, failed: res.failed.length })),
                            onError: (e) => toast.error(String(e)),
                        })}
                    >
                        {t('batchRefresh')}
                    </Button>
                    <Button
                        variant="outline" size="sm"
                        disabled={batch.clearError.isPending}
                        onClick={() => batch.clearError.mutate(selected, {
                            onSuccess: (res) => toast.success(t('batchClearResult', { ok: res.ok, failed: res.failed.length })),
                            onError: (e) => toast.error(String(e)),
                        })}
                    >
                        {t('batchClearError')}
                    </Button>
                    <Button
                        variant="outline" size="sm"
                        disabled={batch.test.isPending}
                        onClick={() => batch.test.mutate({ accountIds: selected, model: '' }, {
                            onSuccess: (res) => {
                                const ok = res.filter((r) => r.success).length;
                                toast.success(t('batchTestResult', { ok, total: res.length }));
                            },
                            onError: (e) => toast.error(String(e)),
                        })}
                    >
                        {t('batchTest')}
                    </Button>
                </div>
            )}

            <div className="overflow-x-auto rounded-lg border">
                <table className="w-full text-sm">
                    <thead className="bg-muted/50">
                        <tr>
                            <th className="text-left p-3 w-8">
                                <input type="checkbox" className="h-4 w-4" checked={allSelected} onChange={toggleSelectAll} />
                            </th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('accountName')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('platform')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('type')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('status')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('models')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">
    {t('weightCol')}
    <Hint text={t('weightHint')} />
</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">
    {t('loadFactorCol')}
    <Hint text={t('loadFactorHint')} />
</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('concurrency')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('requests')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('errors')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('quota')}</th>
                            <th className="text-left p-3 font-medium whitespace-nowrap">{t('cooldownStatus')}</th>
                            <th className="text-right p-3 font-medium whitespace-nowrap">{t('actions')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {(accounts ?? []).map((acct: PoolAccount) => {
                            const inCooldown = acct.rate_limit_reset_at > now || acct.overload_until > now;
                            const tokenExpired = acct.type === 'oauth' && acct.token_expires_at > 0 && acct.token_expires_at < now + 60;
                            const tempUnschedUntil = acct.temp_unsched_until ?? 0;
                            const inTempUnsched = tempUnschedUntil > now;
                            const expired = (acct.auto_pause_on_expired ?? false) && (acct.expires_at ?? 0) > 0 && (acct.expires_at ?? 0) <= now;
                            const minsLeft = inTempUnsched ? Math.max(1, Math.ceil((tempUnschedUntil - now) / 60)) : 0;
                            return (
                                <tr key={acct.id} className="border-t">
                                    <td className="p-3">
                                        <input
                                            type="checkbox"
                                            className="h-4 w-4"
                                            checked={selectedIds.has(acct.id)}
                                            onChange={() => toggleSelect(acct.id)}
                                        />
                                    </td>
                                    <td className="p-3 whitespace-nowrap">{acct.name || `#${acct.id}`}</td>
                                    <td className="p-3"><PlatformBadge platform={acct.platform} /></td>
                                    <td className="p-3"><Badge variant="secondary" className="text-xs">{t(`typeLabels.${acct.type}`)}</Badge></td>
                                    <td className="p-3">
                                        {tokenExpired ? (
                                            <Badge variant="destructive" className="text-xs">{t('tokenExpired')}</Badge>
                                        ) : expired ? (
                                            <Badge variant="outline" className="text-xs text-muted-foreground">{t('expiredBadge')}</Badge>
                                        ) : inTempUnsched ? (
                                            <Badge variant="outline" className="text-xs text-amber-600" title={acct.temp_unsched_reason ?? ''}>
                                                {t('tempUnschedBadge', { minutes: minsLeft })}
                                            </Badge>
                                        ) : inCooldown ? (
                                            <Badge variant="outline" className="text-xs text-amber-600">{t('cooling')}</Badge>
                                        ) : acct.status === 'active' && acct.schedulable ? (
                                            <Badge variant="default" className="text-xs">{t('active')}</Badge>
                                        ) : (
                                            <Badge variant="destructive" className="text-xs" title={acct.error_message ?? ''}>{acct.status}</Badge>
                                        )}
                                    </td>
                                    <td className="p-3 max-w-[160px]">
                                        {acct.models ? (
                                            <div className="flex flex-wrap gap-1">
                                                {acct.models.split(',').slice(0, 3).map((m, i) => (
                                                    <Badge key={i} variant="outline" className="text-xs">{m.trim()}</Badge>
                                                ))}
                                                {acct.models.split(',').length > 3 && <span className="text-xs text-muted-foreground">+{acct.models.split(',').length - 3}</span>}
                                            </div>
                                        ) : <span className="text-xs text-muted-foreground">{t('allModels')}</span>}
                                    </td>
                                    <td className="p-3">{acct.weight ?? 0}</td>
                                    <td className="p-3">{acct.load_factor && acct.load_factor > 0 ? acct.load_factor : (acct.concurrency || pool.default_concurrency)}</td>
                                    <td className="p-3">{acct.concurrency || pool.default_concurrency}</td>
                                    <td className="p-3">{acct.total_requests}</td>
                                    <td className="p-3">{acct.total_errors}</td>
                                    <td className="p-3"><QuotaCell quota={acct.quota} /></td>
                                    <td className="p-3">{inCooldown ? <Badge variant="outline" className="text-xs text-amber-600">{t('cooling')}</Badge> : <span className="text-muted-foreground">-</span>}</td>
                                    <td className="p-3 text-right whitespace-nowrap">
                                        <div className="flex justify-end gap-1">
                                            <Button variant="ghost" size="icon" className="h-7 w-7" title={t('editAccount')} onClick={() => openEdit(acct)}>
                                                <Pencil className="h-3.5 w-3.5" />
                                            </Button>
                                            <Button
                                                variant="ghost" size="icon" className="h-7 w-7" title={t('testAccount')}
                                                disabled={testAccount.isPending}
                                                onClick={() => {
                                                    const model = acct.models?.split(',')[0]?.trim() || 'gpt-4o-mini';
                                                    testAccount.mutate({ accountId: acct.id, model }, {
                                                        onSuccess: (res) => res.success ? toast.success(t('testSuccess', { latency: res.latency_ms })) : toast.error(t('testFailed', { error: res.error || '' })),
                                                        onError: (e) => toast.error(String(e)),
                                                    });
                                                }}
                                            >
                                                <FlaskConical className="h-3.5 w-3.5" />
                                            </Button>
                                            <Button variant="ghost" size="icon" className="h-7 w-7" title={t('refreshQuota')}
                                                disabled={fetchQuota.isPending}
                                                onClick={() => fetchQuota.mutate(acct.id, { onSuccess: () => toast.success(t('quotaRefreshed')), onError: (e) => toast.error(String(e)) })}
                                            >
                                                <RefreshCw className="h-3.5 w-3.5" />
                                            </Button>
                                            {acct.type === 'oauth' && (
                                                <Button variant="ghost" size="icon" className="h-7 w-7" title={t('refreshToken')}
                                                    disabled={refreshToken.isPending}
                                                    onClick={() => refreshToken.mutate(acct.id, { onSuccess: () => toast.success(t('tokenRefreshed')), onError: (e) => toast.error(String(e)) })}
                                                >
                                                    <KeyRound className="h-3.5 w-3.5" />
                                                </Button>
                                            )}
                                            <Button
                                                variant="ghost" size="icon" className="h-7 w-7" title={t('recoverAccount')}
                                                disabled={recoverAccount.isPending}
                                                onClick={() => recoverAccount.mutate(acct.id, { onSuccess: () => toast.success(t('recoverSuccess')), onError: (e) => toast.error(String(e)) })}
                                            >
                                                <RecoverIcon className="h-3.5 w-3.5" />
                                            </Button>
                                            <Button
                                                variant="ghost" size="icon" className="h-7 w-7" title={t('tempUnschedAction')}
                                                onClick={() => handleOpenTempUnsched(acct)}
                                            >
                                                <TempUnschedIcon className="h-3.5 w-3.5" />
                                            </Button>
                                            <Button variant="ghost" size="icon" className="h-7 w-7" title={t('accountDeleted')}
                                                onClick={() => deleteAccount.mutate(acct.id, { onSuccess: () => toast.success(t('accountDeleted')), onError: (e) => toast.error(String(e)) })}
                                            >
                                                <Trash2 className="h-3.5 w-3.5" />
                                            </Button>
                                        </div>
                                    </td>
                                </tr>
                            );
                        })}
                        {(!accounts || accounts.length === 0) && (
                            <tr><td colSpan={14} className="p-6 text-center text-muted-foreground">{t('noAccounts')}</td></tr>
                        )}
                    </tbody>
                </table>
            </div>

            <AccountFormDialog
                poolId={pool.id}
                account={editingAccount}
                open={dialogOpen}
                onOpenChange={setDialogOpen}
            />

            <Dialog open={tempUnschedOpen} onOpenChange={setTempUnschedOpen}>
                <DialogContent>
                    <DialogHeader><DialogTitle>{t('tempUnschedDialog.title')}</DialogTitle></DialogHeader>
                    <div className="space-y-3">
                        <div>
                            <Label>
    {t('tempUnschedDialog.minutes')}
    <Hint text={t('tempUnschedDialog.minutesHint')} />
</Label>
                            <Input type="number" value={tempUnschedMinutes} onChange={(e) => setTempUnschedMinutes(e.target.value)} />
                        </div>
                        <div>
                            <Label>{t('tempUnschedDialog.reason')}</Label>
                            <Input value={tempUnschedReason} onChange={(e) => setTempUnschedReason(e.target.value)} />
                        </div>
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setTempUnschedOpen(false)}>{t('cancel')}</Button>
                        <Button onClick={handleConfirmTempUnsched}>{t('tempUnschedDialog.confirm')}</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <Dialog open={importOpen} onOpenChange={setImportOpen}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>
    {t('importAccounts')}
    <Hint text={t('importHint')} />
</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-2">
                        <textarea
                            rows={10}
                            className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs"
                            value={importText}
                            onChange={(e) => setImportText(e.target.value)}
                            placeholder={t('importPlaceholder')}
                        />
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setImportOpen(false)}>{t('cancel')}</Button>
                        <Button onClick={handleImport} disabled={!importText || importAccounts.isPending}>{t('import')}</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
