'use client';

import { useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Database, Download, Upload, AlertTriangle, Loader2, Check, X, ChevronDown, ChevronRight } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Hint } from '@/components/ui/hint';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { toast } from '@/components/common/Toast';
import { useExportDB, useImportDB, useMigrateDatabase, useTestDatabaseConnection } from '@/api/endpoints/setting';
import { Progress } from '@/components/ui/progress';
import { cn } from '@/lib/utils';

type ImportMode = 'incremental' | 'full';

export function SettingBackup() {
    const t = useTranslations('setting');

    const exportDB = useExportDB();
    const importDB = useImportDB();
    const testDatabase = useTestDatabaseConnection();
    const migrateDatabase = useMigrateDatabase();

    const [includeLogs, setIncludeLogs] = useState(false);
    const [includeStats, setIncludeStats] = useState(false);
    const [importMode, setImportMode] = useState<ImportMode>('incremental');
    const [targetType, setTargetType] = useState<'sqlite' | 'mysql' | 'postgres'>('sqlite');
    const [targetPath, setTargetPath] = useState('data/data-next.db');
    const [migrateLogs, setMigrateLogs] = useState(false);
    const [migrateStats, setMigrateStats] = useState(false);

    // 结构化数据库连接字段（MySQL / PostgreSQL），自动拼 DSN。
    // SQLite 保持单路径输入框，不使用这些字段。
    const [dbHost, setDbHost] = useState('127.0.0.1');
    const [dbPort, setDbPort] = useState('3306');
    const [dbUser, setDbUser] = useState('root');
    const [dbPassword, setDbPassword] = useState('');
    const [dbName, setDbName] = useState('octopus');
    const [advancedDSN, setAdvancedDSN] = useState(false); // 高级模式：直接编辑 DSN

    const [file, setFile] = useState<File | null>(null);
    const fileInputRef = useRef<HTMLInputElement | null>(null);

    const rowsAffected = importDB.data?.rows_affected ?? null;
    const importProgress = importDB.data?.progress ?? [];
    const totalSteps = importProgress.length;
    const completedSteps = importProgress.filter(s => s.ok).length;
    const progressValue = totalSteps > 0 ? Math.round((completedSteps / totalSteps) * (importDB.isPending ? 50 : 100)) : 0;

    const rowsAffectedList = useMemo(() => {
        if (!rowsAffected) return [];
        return Object.entries(rowsAffected)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([k, v]) => ({ table: k, count: v }));
    }, [rowsAffected]);

    // 根据数据库类型和输入模式计算最终 DSN/path。
    // SQLite 或高级模式：直接用 targetPath。
    // MySQL 结构化模式：user:pass@tcp(host:port)/dbname
    // PostgreSQL 结构化模式：host=H user=U password=P dbname=D port=PORT sslmode=disable
    const resolvedTargetPath = useMemo(() => {
        if (targetType === 'sqlite' || advancedDSN) {
            return targetPath;
        }
        if (targetType === 'mysql') {
            return `${dbUser}:${dbPassword}@tcp(${dbHost}:${dbPort})/${dbName}`;
        }
        // postgres
        return `host=${dbHost} user=${dbUser} password=${dbPassword} dbname=${dbName} port=${dbPort} sslmode=disable`;
    }, [targetType, advancedDSN, targetPath, dbHost, dbPort, dbUser, dbPassword, dbName]);

    // 切换数据库类型时重置默认端口。
    const handleTargetTypeChange = (type: 'sqlite' | 'mysql' | 'postgres') => {
        setTargetType(type);
        setAdvancedDSN(false);
        if (type === 'mysql' && dbPort === '5432') setDbPort('3306');
        else if (type === 'postgres' && dbPort === '3306') setDbPort('5432');
        if (type === 'sqlite' && !targetPath) setTargetPath('data/data-next.db');
    };

    const onPickFile = (f: File | null) => {
        setFile(f);
    };

    const onImport = async () => {
        if (!file) {
            toast.error(t('backup.import.noFile'));
            return;
        }
        try {
            await importDB.mutateAsync({ file, mode: importMode });
            toast.success(t('backup.import.success'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.import.failed'));
        }
    };

    const onExport = async () => {
        try {
            await exportDB.mutateAsync({ include_logs: includeLogs, include_stats: includeStats });
            toast.success(t('backup.export.success'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.export.failed'));
        }
    };

    const migrationPayload = {
        type: targetType,
        path: resolvedTargetPath,
        include_logs: migrateLogs,
        include_stats: migrateStats,
    };

    const onTestDatabase = async () => {
        if (!resolvedTargetPath.trim()) {
            toast.error(t('backup.migration.targetRequired')); 
            return;
        }
        try {
            await testDatabase.mutateAsync(migrationPayload);
            toast.success(t('backup.migration.testSuccess')); 
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.migration.testFailed')); 
        }
    };

    const onMigrateDatabase = async () => {
        if (!resolvedTargetPath.trim()) {
            toast.error(t('backup.migration.targetRequired'));
            return;
        }
        if (!window.confirm(t('backup.migration.confirm'))) {
            return;
        }
        try {
            await migrateDatabase.mutateAsync(migrationPayload);
            toast.success(t('backup.migration.success')); 
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.migration.failed')); 
        }
    };

    return (
        <div className="rounded-xl border-border/35 bg-card p-4 sm:p-6 space-y-4 sm:space-y-5 text-card-foreground shadow-md ">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Database className="h-5 w-5" />
                {t('backup.title')}
            </h2>

            {/* 导出 */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="text-sm font-semibold text-card-foreground">{t('backup.export.title')}</div>

                <div className="flex items-center justify-between gap-4">
                    <div className="min-w-0 flex-1">
                        <div className="text-sm text-muted-foreground">
                            {t('backup.export.includeLogs')}
                            <Hint text={includeLogs ? t('backup.export.includeLogsHint') : undefined} />
                        </div>
                    </div>
                    <Switch checked={includeLogs} onCheckedChange={setIncludeLogs} />
                </div>

                <div className="flex items-center justify-between gap-4">
                    <div className="text-sm text-muted-foreground">{t('backup.export.includeStats')}</div>
                    <Switch checked={includeStats} onCheckedChange={setIncludeStats} />
                </div>

                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={onExport}
                    disabled={exportDB.isPending}
                >
                    <Download className="size-4" />
                    {exportDB.isPending ? t('backup.export.exporting') : t('backup.export.button')}
                </Button>
            </div>

            <div className="h-px bg-border/50" />

            <div className="space-y-3 rounded-lg border border-amber-500/20 bg-card p-3 sm:p-4 shadow-sm">
                <div className="flex items-start gap-2">
                    <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600" />
                    <div className="min-w-0 flex-1">
                        <div className="text-sm font-semibold text-card-foreground">{t('backup.migration.title')}</div>
                        <div className="mt-1 text-xs leading-5 text-muted-foreground">
                            {t('backup.migration.description')}
                        </div>
                    </div>
                </div>

                <div className="grid grid-cols-1 gap-3">
                    <select
                        value={targetType}
                        onChange={(e) => handleTargetTypeChange(e.target.value as 'sqlite' | 'mysql' | 'postgres')}
                        className="h-10 rounded-xl border border-input bg-background px-3 text-sm w-full"
                    >
                        <option value="sqlite">SQLite</option>
                        <option value="mysql">MySQL</option>
                        <option value="postgres">PostgreSQL</option>
                    </select>

                    {targetType === 'sqlite' || advancedDSN ? (
                        <Input
                            className="rounded-xl w-full"
                            value={targetPath}
                            onChange={(e) => setTargetPath(e.target.value)}
                            placeholder={targetType === 'sqlite' ? 'data/data-next.db' : 'user:pass@tcp(host:3306)/octopus'}
                        />
                    ) : (
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                            <div className="space-y-1.5">
                                <label className="text-xs text-muted-foreground">{t('backup.migration.host')}</label>
                                <Input
                                    className="rounded-xl"
                                    value={dbHost}
                                    onChange={(e) => setDbHost(e.target.value)}
                                    placeholder="127.0.0.1"
                                />
                            </div>
                            <div className="space-y-1.5">
                                <label className="text-xs text-muted-foreground">{t('backup.migration.port')}</label>
                                <Input
                                    className="rounded-xl"
                                    value={dbPort}
                                    onChange={(e) => setDbPort(e.target.value)}
                                    placeholder={targetType === 'mysql' ? '3306' : '5432'}
                                />
                            </div>
                            <div className="space-y-1.5">
                                <label className="text-xs text-muted-foreground">{t('backup.migration.user')}</label>
                                <Input
                                    className="rounded-xl"
                                    value={dbUser}
                                    onChange={(e) => setDbUser(e.target.value)}
                                    placeholder="root"
                                />
                            </div>
                            <div className="space-y-1.5">
                                <label className="text-xs text-muted-foreground">{t('backup.migration.password')}</label>
                                <Input
                                    type="password"
                                    className="rounded-xl"
                                    value={dbPassword}
                                    onChange={(e) => setDbPassword(e.target.value)}
                                    placeholder="••••••"
                                />
                            </div>
                            <div className="space-y-1.5 sm:col-span-2">
                                <label className="text-xs text-muted-foreground">{t('backup.migration.dbName')}</label>
                                <Input
                                    className="rounded-xl"
                                    value={dbName}
                                    onChange={(e) => setDbName(e.target.value)}
                                    placeholder="octopus"
                                />
                            </div>
                        </div>
                    )}

                    {/* 高级模式切换：直接编辑 DSN */}
                    {targetType !== 'sqlite' && (
                        <button
                            type="button"
                            onClick={() => {
                                setAdvancedDSN(!advancedDSN);
                                if (!advancedDSN) setTargetPath(resolvedTargetPath);
                            }}
                            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
                        >
                            {advancedDSN ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
                            {t('backup.migration.advancedDSN')}
                        </button>
                    )}
                </div>

                <div className="grid grid-cols-1 gap-3">
                    <div className="flex items-center justify-between gap-4 rounded-lg border border-border/30 p-3">
                        <div className="min-w-0 flex-1">
                            <div className="text-sm text-muted-foreground">
                                {t('backup.migration.migrateLogs')}
                                <Hint text={t('backup.migration.migrateLogsHint')} />
                            </div>
                        </div>
                        <Switch checked={migrateLogs} onCheckedChange={setMigrateLogs} />
                    </div>
                    <div className="flex items-center justify-between gap-4 rounded-lg border border-border/30 p-3">
                        <div className="text-sm text-muted-foreground">{t('backup.migration.migrateStats')}</div>
                        <Switch checked={migrateStats} onCheckedChange={setMigrateStats} />
                    </div>
                </div>

                <div className="flex flex-col gap-2 sm:flex-row">
                    <Button type="button" variant="outline" className="w-full sm:flex-1 rounded-xl" onClick={onTestDatabase} disabled={testDatabase.isPending || migrateDatabase.isPending}>
                        {testDatabase.isPending ? <Loader2 className="size-4 animate-spin" /> : <Check className="size-4" />}
                        {t('backup.migration.testButton')}
                    </Button>
                    <Button type="button" variant="destructive" className="w-full sm:flex-1 rounded-xl" onClick={onMigrateDatabase} disabled={migrateDatabase.isPending || testDatabase.isPending}>
                        {migrateDatabase.isPending ? <Loader2 className="size-4 animate-spin" /> : <Database className="size-4" />}
                        {migrateDatabase.isPending ? t('backup.migration.migrating') : t('backup.migration.button')}
                    </Button>
                </div>

                {migrateDatabase.data ? (
                    <div className="space-y-2 rounded-lg border border-emerald-500/20 bg-emerald-500/8 p-3 text-xs text-emerald-700 dark:text-emerald-300 break-words">
                        <div>{t('backup.migration.successDetail', { type: migrateDatabase.data.type, path: migrateDatabase.data.path })}</div>
                        {migrateDatabase.data.cleaned_files && migrateDatabase.data.cleaned_files.length > 0 ? (
                            <div className="text-amber-600 dark:text-amber-400">
                                {t('backup.migration.cleanedFiles', { count: migrateDatabase.data.cleaned_files.length })}
                                <ul className="mt-1 list-inside list-disc space-y-0.5 break-all">
                                    {migrateDatabase.data.cleaned_files.map((f) => (
                                        <li key={f}>{f}</li>
                                    ))}
                                </ul>
                            </div>
                        ) : null}
                        <div className="font-semibold text-amber-600 dark:text-amber-400">{t('backup.migration.restartNotice')}</div>
                    </div>
                ) : null}
            </div>

            <div className="h-px bg-border/50" />

            {/* 导入 */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="text-sm font-semibold text-card-foreground">
                    {t('backup.import.title')}
                    <Hint text={t('backup.import.sizeHint')} />
                </div>

                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div className="text-sm text-muted-foreground">{t('backup.import.mode.label')}</div>
                    <div className="flex gap-1 rounded-lg border border-border/30 bg-muted/30 p-0.5">
                        <button
                            type="button"
                            onClick={() => setImportMode('incremental')}
                            className={cn(
                                'flex-1 sm:flex-initial rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
                                importMode === 'incremental' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
                            )}
                        >
                            {t('backup.import.mode.incremental')}
                        </button>
                        <button
                            type="button"
                            onClick={() => setImportMode('full')}
                            className={cn(
                                'flex-1 sm:flex-initial rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
                                importMode === 'full' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
                            )}
                        >
                            {t('backup.import.mode.full')}
                        </button>
                    </div>
                </div>

                {importMode === 'full' && (
                    <div className="flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/8 p-3 text-xs text-amber-700 dark:text-amber-300">
                        <AlertTriangle className="size-4 shrink-0 mt-0.5" />
                        <span className="break-words">{t('backup.import.mode.fullWarning')}</span>
                    </div>
                )}

                <Input
                    ref={fileInputRef}
                    type="file"
                    accept="application/json,.json"
                    onChange={(e) => onPickFile(e.target.files?.[0] ?? null)}
                    className="rounded-xl w-full"
                />

                <Button
                    type="button"
                    variant="destructive"
                    className="w-full rounded-xl"
                    onClick={onImport}
                    disabled={importDB.isPending}
                >
                    {importDB.isPending ? <Loader2 className="size-4 animate-spin" /> : <Upload className="size-4" />}
                    {importDB.isPending ? t('backup.import.importing') : t('backup.import.button')}
                </Button>

                {(importDB.isPending || importProgress.length > 0) && (
                    <div className="space-y-2 pt-2">
                        <Progress value={progressValue} className="h-1.5" />
                        {importProgress.length > 0 && (
                            <div className="space-y-1 max-h-48 overflow-y-auto">
                                {importProgress.map((step, i) => (
                                    <div key={i} className={cn(
                                        'flex items-center gap-2 text-xs rounded-md px-2 py-1',
                                        step.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive bg-destructive/5'
                                    )}>
                                        {step.ok ? <Check className="size-3.5 shrink-0" /> : <X className="size-3.5 shrink-0" />}
                                        <span className="tabular-nums w-10 shrink-0 text-muted-foreground">{step.mode}</span>
                                        <span className="truncate flex-1 break-all">{step.table}</span>
                                        <span className="tabular-nums shrink-0 text-muted-foreground">{step.rows_affected}</span>
                                    </div>
                                ))}
                            </div>
                        )}
                    </div>
                )}

                {rowsAffectedList.length > 0 && (
                    <div className="mt-2 space-y-1">
                        <div className="text-xs font-semibold text-card-foreground">{t('backup.import.result')}</div>
                        <div className="grid grid-cols-2 gap-1 text-xs text-muted-foreground">
                            {rowsAffectedList.map((it) => (
                                <div key={it.table} className="flex justify-between gap-2">
                                    <span className="truncate break-all">{it.table}</span>
                                    <span className="tabular-nums shrink-0">{it.count}</span>
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </div>

        </div>
    );
}
