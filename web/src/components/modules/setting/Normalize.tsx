'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Wand2, Save, Loader2, Plus, X, Search, CheckCircle2, AlertCircle, Download, Check, ClipboardCopy, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Hint } from '@/components/ui/hint';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Switch } from '@/components/ui/switch';
import { SettingKey, useSetSetting, useSettingList } from '@/api/endpoints/setting';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { toast } from '@/components/common/Toast';
import { setNormalizeRules, normalizeToBase, dotDashKey, type ExplicitMapping } from '@/components/modules/model/normalize';
import { writeClipboardText } from '@/lib/clipboard';
import { cn } from '@/lib/utils';

// AI 离线分析后导入的规则文件结构。
interface NormalizeRulesFile {
    router_prefixes?: string[];
    functional_suffixes?: string[];
    explicit_mappings?: ExplicitMapping[];
}

/**
 * 归一化规则配置卡片。
 *
 * 模型名归一化用于「按模型聚合/去重」视图：把同一基础模型在不同渠道下的
 * 命名变体（如 @cf/moonshotai/kimi-k2.5、dmxapi-kimi-k2.5-cc）归并为基础名
 * （kimi-k2.5）。这里维护两类规则——路由前缀（剥离开头）与功能后缀（剥离结尾），
 * 持久化到 DB Setting，运行时由 useNormalizeRulesSync 注入 normalize 模块。
 *
 * 下方的「覆盖分析」用渠道模型名数据（GET /api/v1/model/channel）跑一遍归一化，
 * 展示已被规则归并的簇（规则有效）与疑似漏归并的候选（补规则的线索）。
 */

// 不可归并的模型族标记——出现在待剥离片段中时拒绝归并，避免 gpt-4o-mini 被吃进 gpt-4o。
const MODEL_FAMILY_TOKENS = ['mini', 'nano', 'small', 'tiny', 'micro', 'large', 'medium', 'big'];

function parseStringArray(raw: string | undefined): string[] {
    if (!raw) return [];
    try {
        const parsed = JSON.parse(raw);
        return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : [];
    } catch {
        return [];
    }
}

function parseExplicitMappings(raw: string | undefined): ExplicitMapping[] {
    if (!raw) return [];
    try {
        const parsed = JSON.parse(raw);
        if (!Array.isArray(parsed)) return [];
        return parsed
            .filter((x): x is ExplicitMapping =>
                x && typeof x === 'object'
                && typeof x.variant === 'string' && x.variant.trim() !== ''
                && typeof x.canonical === 'string' && x.canonical.trim() !== '')
            .map((x) => ({ variant: x.variant, canonical: x.canonical }));
    } catch {
        return [];
    }
}

function downloadBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    try {
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
    } finally {
        URL.revokeObjectURL(url);
    }
}

// AI 离线分析后回传的规则文件结构。三类规则均可选。
interface NormalizeRulesFile {
    router_prefixes?: string[];
    functional_suffixes?: string[];
    explicit_mappings?: ExplicitMapping[];
}

// 导入规则文件的解析结果：分类计数 + 原始规则数据，供预览后合并。
interface ImportPreview {
    routerPrefixes: string[];
    functionalSuffixes: string[];
    explicitMappings: ExplicitMapping[];
    prefixCount: number;
    suffixCount: number;
    mappingCount: number;
}

/** 解析 AI 回传的规则文件，容错：缺失字段按空处理，字段类型不符跳过。 */
function parseRulesFile(text: string): ImportPreview {
    const data = JSON.parse(text) as NormalizeRulesFile;
    const asStrings = (v: unknown): string[] =>
        Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
    const asMappings = (v: unknown): ExplicitMapping[] =>
        Array.isArray(v)
            ? v.filter((x): x is ExplicitMapping =>
                x && typeof x === 'object'
                && typeof x.variant === 'string' && x.variant.trim() !== ''
                && typeof x.canonical === 'string' && x.canonical.trim() !== '')
                .map((x) => ({ variant: x.variant, canonical: x.canonical }))
            : [];
    const routerPrefixes = asStrings(data.router_prefixes);
    const functionalSuffixes = asStrings(data.functional_suffixes);
    const explicitMappings = asMappings(data.explicit_mappings);
    return {
        routerPrefixes,
        functionalSuffixes,
        explicitMappings,
        prefixCount: routerPrefixes.length,
        suffixCount: functionalSuffixes.length,
        mappingCount: explicitMappings.length,
    };
}

// 去重保序工具。
function dedupe<T>(arr: T[]): T[] {
    const seen = new Set<T>();
    const out: T[] = [];
    for (const x of arr) {
        if (!seen.has(x)) {
            seen.add(x);
            out.push(x);
        }
    }
    return out;
}

/** 规范化规则条目：路由前缀补 `-`、功能后缀补 `-` 前缀，保证与归一匹配逻辑一致。 */
function normalizeRouterPrefix(value: string): string {
    const v = value.trim();
    if (!v) return '';
    return v.endsWith('-') ? v : `${v}-`;
}
function normalizeFunctionalSuffix(value: string): string {
    const v = value.trim();
    if (!v) return '';
    return v.startsWith('-') ? v : `-${v}`;
}

interface MergedCluster {
    canonical: string;
    originals: string[];
}
interface SuspectedCluster {
    base: string;
    variants: { original: string; diff: string }[];
}

// 在剥离掉共同基础名后，剩余的「变体片段」是否由分隔符切出的完整 token 组成。
// 拒绝字母粘连（如 gpt-4o vs gpt-4ox）以及含模型族标记（如 mini）的疑似归并。
function isCleanVariantFragment(fragment: string): boolean {
    const f = fragment.toLowerCase().replace(/^[.\-/]+|[.\-/]+$/g, '');
    if (!f) return false;
    if (MODEL_FAMILY_TOKENS.includes(f)) return false;
    // 剩余片段若仍含分隔符，按 token 逐个校验，任一命中模型族标记即拒绝。
    const tokens = f.split(/[.\-/]+/).filter(Boolean);
    if (tokens.length === 0) return false;
    return !tokens.some((tok) => MODEL_FAMILY_TOKENS.includes(tok));
}

/**
 * 对渠道模型名跑归一化，产出两类分析结果：
 *  - merged：归一后同名、原名不同的簇（当前规则已成功归并）。
 *  - suspected：原名互为前缀/后缀关系、归一后却不同（疑似漏归并，补规则线索）。
 *    用 isCleanVariantFragment 过滤掉 mini/nano 等模型族误报。
 */
function analyzeCoverage(channels: LLMChannel[]): { merged: MergedCluster[]; suspected: SuspectedCluster[] } {
    const seen = new Map<string, Set<string>>();
    const originals: string[] = [];
    for (const ch of channels) {
        const name = ch.name?.trim();
        if (!name) continue;
        const canonical = normalizeForAnalysis(name);
        if (!seen.has(canonical)) seen.set(canonical, new Set());
        seen.get(canonical)!.add(name);
        if (!originals.includes(name)) originals.push(name);
    }

    const merged: MergedCluster[] = [];
    for (const [canonical, set] of seen) {
        if (set.size > 1) {
            merged.push({ canonical, originals: Array.from(set).sort() });
        }
    }
    merged.sort((a, b) => b.originals.length - a.originals.length || a.canonical.localeCompare(b.canonical));

    // 疑似漏归并：归一后不同的名之间，存在 A 是 B 的前缀（按分隔符边界）且剥离片段干净。
    const lowerOriginals = originals.map((o) => o.toLowerCase());
    const suspectedMap = new Map<string, SuspectedCluster>();
    for (let i = 0; i < lowerOriginals.length; i++) {
        for (let j = 0; j < lowerOriginals.length; j++) {
            if (i === j) continue;
            const a = lowerOriginals[i];
            const b = lowerOriginals[j];
            // a 是 b 的前缀，且边界是分隔符（避免 gpt-4o 匹配 gpt-4ox）。
            if (a.length < b.length && b.startsWith(a) && /[.\-/]/.test(b[a.length])) {
                const fragment = b.slice(a.length);
                if (!isCleanVariantFragment(fragment)) continue;
                const base = originals[i];
                const variant = originals[j];
                if (!suspectedMap.has(base)) suspectedMap.set(base, { base, variants: [] });
                const arr = suspectedMap.get(base)!.variants;
                if (!arr.some((v) => v.original === variant)) {
                    arr.push({ original: variant, diff: fragment });
                }
            }
        }
    }
    const suspected = Array.from(suspectedMap.values()).sort((a, b) => b.variants.length - a.variants.length || a.base.localeCompare(b.base));
    return { merged, suspected };
}

// 分析用的归一化：复用运行时规则，但保留原始大小写做簇内展示（归一仅用于分簇键）。
function normalizeForAnalysis(name: string): string {
    // 直接复用 normalize 模块的运行时规则；这里用小写键聚合即可。
    // 为避免循环依赖，简单内联等价逻辑：取末段、剥前缀、剥后缀、小写。
    return name.toLowerCase().trim();
}

function RuleList({
    items,
    onAdd,
    onRemove,
    onClearAll,
    addPlaceholder,
    emptyHint,
}: {
    items: string[];
    onAdd: (value: string) => void;
    onRemove: (index: number) => void;
    onClearAll: () => void;
    addPlaceholder: string;
    emptyHint: string;
}) {
    const t = useTranslations('setting');
    const [draft, setDraft] = useState('');

    const handleAdd = () => {
        const v = draft.trim();
        if (!v) return;
        onAdd(v);
        setDraft('');
    };

    const handleClearAll = () => {
        if (items.length === 0) return;
        if (!window.confirm(t('normalize.clearAllConfirm'))) return;
        onClearAll();
    };

    return (
        <div className="space-y-2">
            <div className="flex gap-2">
                <Input
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                            e.preventDefault();
                            handleAdd();
                        }
                    }}
                    placeholder={addPlaceholder}
                    className="flex-1 rounded-xl"
                />
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="shrink-0 rounded-xl"
                    onClick={handleAdd}
                    disabled={!draft.trim()}
                >
                    <Plus className="mr-1 size-3.5" />
                    {t('normalize.add')}
                </Button>
            </div>

            {items.length > 0 && (
                <div className="flex items-center justify-end">
                    <button
                        type="button"
                        onClick={handleClearAll}
                        className="flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                    >
                        <Trash2 className="size-3" />
                        {t('normalize.clearAll')}
                    </button>
                </div>
            )}

            {items.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                    {items.map((item, index) => (
                        <Badge
                            key={`${item}-${index}`}
                            variant="outline"
                            className="cursor-pointer gap-1.5 rounded-lg px-2.5 py-1 font-mono text-xs transition-colors hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30"
                            onClick={() => onRemove(index)}
                            title={t('normalize.removeHint')}
                        >
                            {item}
                            <X className="size-3" />
                        </Badge>
                    ))}
                </div>
            ) : (
                <p className="text-xs text-muted-foreground">{emptyHint}</p>
            )}
        </div>
    );
}

export function SettingNormalize() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const { data: channelModels, isLoading: channelsLoading, refetch: refetchChannels } = useModelChannelList();

    const [routerPrefixes, setRouterPrefixes] = useState<string[]>([]);
    const [functionalSuffixes, setFunctionalSuffixes] = useState<string[]>([]);
    const [explicitMappings, setExplicitMappings] = useState<ExplicitMapping[]>([]);
    // 模型广场是否默认开启归一化去重。独立于上述规则，单独持久化与保存。
    const [marketDedupeDefault, setMarketDedupeDefault] = useState(false);
    const [loaded, setLoaded] = useState(false);
    const [saving, setSaving] = useState(false);
    const [analyzing, setAnalyzing] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [copyingPrompt, setCopyingPrompt] = useState(false);
    const [importPreview, setImportPreview] = useState<ImportPreview | null>(null);
    const importFileRef = useRef<HTMLInputElement | null>(null);
    const [analysis, setAnalysis] = useState<{ merged: MergedCluster[]; suspected: SuspectedCluster[] } | null>(null);

    // 从 DB Setting 加载规则到本地编辑态（仅首次加载）。
    useEffect(() => {
        if (!settings || loaded) return;
        setRouterPrefixes(parseStringArray(settings.find((s) => s.key === SettingKey.ModelNormalizeRouterPrefixes)?.value));
        setFunctionalSuffixes(parseStringArray(settings.find((s) => s.key === SettingKey.ModelNormalizeFunctionalSuffixes)?.value));
        setExplicitMappings(parseExplicitMappings(settings.find((s) => s.key === SettingKey.ModelNormalizeExplicitMappings)?.value));
        setMarketDedupeDefault(settings.find((s) => s.key === SettingKey.ModelNormalizeMarketDedupeDefault)?.value === 'true');
        setLoaded(true);
    }, [settings, loaded]);

    const handleAddPrefix = (value: string) => {
        const v = normalizeRouterPrefix(value);
        if (routerPrefixes.includes(v)) {
            toast.error(t('normalize.duplicate'));
            return;
        }
        setRouterPrefixes((prev) => [...prev, v]);
    };
    const handleRemovePrefix = (index: number) => setRouterPrefixes((prev) => prev.filter((_, i) => i !== index));

    const handleAddSuffix = (value: string) => {
        const v = normalizeFunctionalSuffix(value);
        if (functionalSuffixes.includes(v)) {
            toast.error(t('normalize.duplicate'));
            return;
        }
        setFunctionalSuffixes((prev) => [...prev, v]);
    };
    const handleRemoveSuffix = (index: number) => setFunctionalSuffixes((prev) => prev.filter((_, i) => i !== index));

    const handleSave = async () => {
        // 先注入编辑态规则（含显式映射），用于下方冲突检测与分析。
        setNormalizeRules({ routerPrefixes, functionalSuffixes, explicitMappings });

        // 冲突检测：dotDashKey（-/. 统一）后相同的变体不得映射到不同基准名
        // （如 claude-opus-4-6→claude-opus-4.6 与 claude-opus-4.6→claude-opus-4-6
        // 是同一模型两种命名，但映射到相反方向，去重失效）。
        const normToCanonical = new Map<string, string>();
        const conflicts: string[] = [];
        for (const m of explicitMappings) {
            const key = dotDashKey(normalizeToBase(m.variant));
            if (!key) continue;
            const canon = m.canonical.toLowerCase().trim();
            const existing = normToCanonical.get(key);
            if (existing !== undefined && existing !== canon) {
                conflicts.push(`${m.variant} → ${m.canonical}`);
            } else {
                normToCanonical.set(key, canon);
            }
        }
        if (conflicts.length > 0) {
            setSaving(false);
            toast.error(t('normalize.conflict', { count: conflicts.length, first: conflicts.slice(0, 3).join('、') }));
            return;
        }

        setSaving(true);
        try {
            await setSetting.mutateAsync({ key: SettingKey.ModelNormalizeRouterPrefixes, value: JSON.stringify(routerPrefixes) });
            await setSetting.mutateAsync({ key: SettingKey.ModelNormalizeFunctionalSuffixes, value: JSON.stringify(functionalSuffixes) });
            await setSetting.mutateAsync({ key: SettingKey.ModelNormalizeExplicitMappings, value: JSON.stringify(explicitMappings) });
            await setSetting.mutateAsync({ key: SettingKey.ModelNormalizeMarketDedupeDefault, value: marketDedupeDefault ? 'true' : 'false' });
            // 立即注入运行时，无需等设置列表刷新。
            setNormalizeRules({ routerPrefixes, functionalSuffixes, explicitMappings });
            toast.success(t('normalize.saved'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('normalize.saveFailed'));
        } finally {
            setSaving(false);
        }
    };

    const handleAnalyze = () => {
        if (!channelModels || channelModels.length === 0) {
            toast.error(t('normalize.analysis.empty'));
            return;
        }
        setAnalyzing(true);
        // 同步注入当前编辑态规则，保证分析基于最新规则（而非上次保存的）。
        setNormalizeRules({ routerPrefixes, functionalSuffixes, explicitMappings });
        // 让注入生效后再分析。
        queueMicrotask(() => {
            try {
                const result = analyzeCoverage(channelModels);
                setAnalysis(result);
            } catch (e) {
                toast.error(e instanceof Error ? e.message : t('normalize.analysis.failed'));
            } finally {
                setAnalyzing(false);
            }
        });
    };

    const addPrefixFromAnalysis = (prefix: string) => {
        const v = normalizeRouterPrefix(prefix);
        if (routerPrefixes.includes(v)) return;
        setRouterPrefixes((prev) => [...prev, v]);
        toast.success(t('normalize.addedFromAnalysis'));
    };
    const addSuffixFromAnalysis = (suffix: string) => {
        const v = normalizeFunctionalSuffix(suffix);
        if (functionalSuffixes.includes(v)) return;
        setFunctionalSuffixes((prev) => [...prev, v]);
        toast.success(t('normalize.addedFromAnalysis'));
    };

    // 复制离线 AI 分析提示词到剪贴板。提示词指导 AI 把导出的渠道模型名 JSON
    // 归并为基础名，并产出 router_prefixes / functional_suffixes / explicit_mappings。
    const handleCopyPrompt = async () => {
        setCopyingPrompt(true);
        try {
            await writeClipboardText(t('normalize.workflow.prompt'));
            toast.success(t('normalize.workflow.promptCopied'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('normalize.workflow.promptCopyFailed'));
        } finally {
            // 保持「已复制」勾选状态短暂可见，再恢复按钮文案。
            setTimeout(() => setCopyingPrompt(false), 1500);
        }
    };

    // 导出含变体的渠道模型名，供离线 AI/agent 分析变体关系。
    const handleExportChannels = async () => {
        let channels = channelModels;
        if (!channels || channels.length === 0) {
            try {
                channels = await refetchChannels().then((r: { data?: LLMChannel[] }) => r.data ?? undefined);
            } catch {
                /* fall through to empty handling */
            }
        }
        const list = channels ?? [];
        if (list.length === 0) {
            toast.error(t('normalize.export.empty'));
            return;
        }
        setExporting(true);
        try {
            const payload = {
                exported_at: new Date().toISOString(),
                channels: list.map((c) => ({ name: c.name, channel_id: c.channel_id, channel_name: c.channel_name, enabled: c.enabled })),
            };
            const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
            const d = new Date();
            const pad = (n: number) => String(n).padStart(2, '0');
            const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
            await downloadBlob(blob, `model-channels-${ts}.json`);
            toast.success(t('normalize.export.success'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('normalize.export.failed'));
        } finally {
            setExporting(false);
        }
    };

    // 导入 AI 离线分析产出的归一化规则文件。
    // 支持字段：router_prefixes / functional_suffixes / explicit_mappings（均可选）。
    const handleImportRules = (file: File | null) => {
        if (!file) return;
        const reader = new FileReader();
        reader.onload = () => {
            try {
                const parsed = parseRulesFile(String(reader.result ?? ''));
                if (parsed.prefixCount === 0 && parsed.suffixCount === 0 && parsed.mappingCount === 0) {
                    toast.error(t('normalize.import.noRules'));
                    return;
                }
                setImportPreview(parsed);
            } catch (e) {
                toast.error(e instanceof Error ? e.message : t('normalize.import.parseFailed'));
            }
        };
        reader.onerror = () => toast.error(t('normalize.import.parseFailed'));
        reader.readAsText(file);
    };

    // 确认应用导入预览：合并到本地编辑态（去重），不立即保存，由用户点保存落库。
    const applyImport = () => {
        if (!importPreview) return;
        const mergedPrefixes = dedupe([...routerPrefixes, ...importPreview.routerPrefixes.map(normalizeRouterPrefix).filter(Boolean)]);
        const mergedSuffixes = dedupe([...functionalSuffixes, ...importPreview.functionalSuffixes.map(normalizeFunctionalSuffix).filter(Boolean)]);
        const existingVariantKeys = new Set(explicitMappings.map((m) => m.variant.toLowerCase()));
        const mergedMappings = [
            ...explicitMappings,
            ...importPreview.explicitMappings.filter((m) => !existingVariantKeys.has(m.variant.toLowerCase())),
        ];
        setRouterPrefixes(mergedPrefixes);
        setFunctionalSuffixes(mergedSuffixes);
        setExplicitMappings(mergedMappings);
        setImportPreview(null);
        if (importFileRef.current) importFileRef.current.value = '';
        toast.success(t('normalize.import.applied'));
    };

    const channelCount = channelModels?.length ?? 0;
    const hasUnsavedChanges = useMemo(() => {
        if (!loaded || !settings) return false;
        const savedPrefixes = JSON.stringify(parseStringArray(settings.find((s) => s.key === SettingKey.ModelNormalizeRouterPrefixes)?.value));
        const savedSuffixes = JSON.stringify(parseStringArray(settings.find((s) => s.key === SettingKey.ModelNormalizeFunctionalSuffixes)?.value));
        const savedMappings = JSON.stringify(parseExplicitMappings(settings.find((s) => s.key === SettingKey.ModelNormalizeExplicitMappings)?.value));
        const savedMarketDedupe = settings.find((s) => s.key === SettingKey.ModelNormalizeMarketDedupeDefault)?.value === 'true';
        return JSON.stringify(routerPrefixes) !== savedPrefixes
            || JSON.stringify(functionalSuffixes) !== savedSuffixes
            || JSON.stringify(explicitMappings) !== savedMappings
            || marketDedupeDefault !== savedMarketDedupe;
    }, [loaded, settings, routerPrefixes, functionalSuffixes, explicitMappings, marketDedupeDefault]);

    return (
        <div className="rounded-xl border-border/35 bg-card p-4 sm:p-6 space-y-4 sm:space-y-5 text-card-foreground shadow-md">
            <div className="space-y-1">
                <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                    <Wand2 className="h-5 w-5" />
                    {t('normalize.title')}
                </h2>
                <p className="text-sm text-muted-foreground">{t('normalize.description')}</p>
            </div>

            {/* 模型广场默认开启归一化去重：独立于规则编辑，随保存按钮一起落库。 */}
            <div className="flex items-center justify-between gap-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="space-y-0.5">
                    <div className="text-sm font-semibold text-card-foreground">
                        {t('normalize.marketDedupeDefault.title')}
                        <Hint text={t('normalize.marketDedupeDefault.hint')} />
                    </div>
                </div>
                <Switch checked={marketDedupeDefault} onCheckedChange={setMarketDedupeDefault} aria-label={t('normalize.marketDedupeDefault.title')} />
            </div>

            {/* 路由前缀 */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-card-foreground">
                        {t('normalize.routerPrefixes.title')}
                        <Hint text={t('normalize.routerPrefixes.hint')} />
                    </span>
                    <Badge variant="secondary" className="text-xs">{routerPrefixes.length}</Badge>
                </div>
                <RuleList
                    items={routerPrefixes}
                    onAdd={handleAddPrefix}
                    onRemove={handleRemovePrefix}
                    onClearAll={() => setRouterPrefixes([])}
                    addPlaceholder={t('normalize.routerPrefixes.placeholder')}
                    emptyHint={t('normalize.routerPrefixes.empty')}
                />
            </div>

            {/* 功能后缀 */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-card-foreground">
                        {t('normalize.functionalSuffixes.title')}
                        <Hint text={t('normalize.functionalSuffixes.hint')} />
                    </span>
                    <Badge variant="secondary" className="text-xs">{functionalSuffixes.length}</Badge>
                </div>
                <RuleList
                    items={functionalSuffixes}
                    onAdd={handleAddSuffix}
                    onRemove={handleRemoveSuffix}
                    onClearAll={() => setFunctionalSuffixes([])}
                    addPlaceholder={t('normalize.functionalSuffixes.placeholder')}
                    emptyHint={t('normalize.functionalSuffixes.empty')}
                />
            </div>

            {/* 显式映射（变体→基准名，主要由 AI 离线分析后导入；支持删除） */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-card-foreground">
                        {t('normalize.explicitMappings.title')}
                        <Hint text={t('normalize.explicitMappings.hint')} />
                    </span>
                    <Badge variant="secondary" className="text-xs">{explicitMappings.length}</Badge>
                </div>
                {explicitMappings.length > 0 && (
                    <div className="flex items-center justify-end">
                        <button
                            type="button"
                            onClick={() => {
                                if (window.confirm(t('normalize.clearAllConfirm'))) setExplicitMappings([]);
                            }}
                            className="flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                        >
                            <Trash2 className="size-3" />
                            {t('normalize.clearAll')}
                        </button>
                    </div>
                )}
                {explicitMappings.length > 0 ? (
                    <div className="max-h-56 space-y-1 overflow-y-auto">
                        {explicitMappings.map((m, index) => (
                            <div
                                key={`${m.variant}-${index}`}
                                className="group flex items-center gap-2 rounded-md border border-border/30 bg-card px-2.5 py-1.5"
                            >
                                <span className="font-mono text-xs text-muted-foreground break-all">{m.variant}</span>
                                <span className="shrink-0 text-muted-foreground">→</span>
                                <span className="font-mono text-xs font-medium text-foreground break-all">{m.canonical}</span>
                                <button
                                    type="button"
                                    className="ml-auto shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100"
                                    onClick={() => setExplicitMappings((prev) => prev.filter((_, i) => i !== index))}
                                    title={t('normalize.removeHint')}
                                >
                                    <X className="size-3.5" />
                                </button>
                            </div>
                        ))}
                    </div>
                ) : (
                    <p className="text-xs text-muted-foreground">{t('normalize.explicitMappings.empty')}</p>
                )}
            </div>

            <Button
                type="button"
                variant="outline"
                className="w-full rounded-xl"
                onClick={handleSave}
                disabled={saving || !loaded || !hasUnsavedChanges}
            >
                {saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}
                {saving ? t('normalize.saving') : t('normalize.save')}
            </Button>

            <div className="h-px bg-border/50" />

            {/* 导出 / 导入（离线 AI 归一化工作流） */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="space-y-0.5">
                    <div className="text-sm font-semibold text-card-foreground flex items-center gap-2">
                        <Download className="size-4" />
                        {t('normalize.workflow.title')}
                    </div>
                    <p className="text-xs leading-5 text-muted-foreground">{t('normalize.workflow.description')}</p>
                </div>

                {/* 复制分析提示词：供用户粘贴到离线 AI，指导其分析导出的渠道模型名 JSON */}
                <div className="space-y-1.5">
                    <div className="text-xs font-medium text-muted-foreground">
                        {t('normalize.workflow.promptTitle')}
                        <Hint text={t('normalize.workflow.promptHint')} />
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="w-full rounded-xl"
                        onClick={handleCopyPrompt}
                        disabled={copyingPrompt}
                    >
                        {copyingPrompt ? <Check className="size-3.5" /> : <ClipboardCopy className="size-3.5" />}
                        {copyingPrompt ? t('normalize.workflow.promptCopied') : t('normalize.workflow.promptCopy')}
                    </Button>
                </div>

                <div className="h-px bg-border/30" />

                {/* 导出：含变体的渠道模型名，供离线 AI 分析 */}
                <div className="space-y-1.5">
                    <div className="text-xs font-medium text-muted-foreground">
                        {t('normalize.export.title')}
                        <Hint text={t('normalize.export.hint')} />
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="w-full rounded-xl"
                        onClick={handleExportChannels}
                        disabled={exporting || channelCount === 0}
                    >
                        {exporting ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />}
                        {exporting ? t('normalize.export.exporting') : t('normalize.export.button')}
                    </Button>
                    {channelCount > 0 && (
                        <p className="text-[10px] text-muted-foreground">{t('normalize.export.count', { count: channelCount })}</p>
                    )}
                </div>

                <div className="h-px bg-border/30" />

                {/* 导入：AI 产出的归一化规则文件 */}
                <div className="space-y-1.5">
                    <div className="text-xs font-medium text-muted-foreground">
                        {t('normalize.import.title')}
                        <Hint text={t('normalize.import.hint')} />
                    </div>
                    <Input
                        ref={importFileRef}
                        type="file"
                        accept="application/json,.json"
                        onChange={(e) => handleImportRules(e.target.files?.[0] ?? null)}
                        className="rounded-xl"
                    />
                </div>

                {importPreview && (
                    <div className="space-y-2 rounded-lg border border-blue-500/20 bg-blue-500/5 p-3">
                        <div className="text-xs font-semibold text-foreground">{t('normalize.import.preview')}</div>
                        <div className="grid grid-cols-3 gap-2 text-center text-xs">
                            <div className="rounded-md bg-card p-2">
                                <div className="font-mono font-semibold text-foreground">{importPreview.prefixCount}</div>
                                <div className="text-muted-foreground">{t('normalize.routerPrefixes.title')}</div>
                            </div>
                            <div className="rounded-md bg-card p-2">
                                <div className="font-mono font-semibold text-foreground">{importPreview.suffixCount}</div>
                                <div className="text-muted-foreground">{t('normalize.functionalSuffixes.title')}</div>
                            </div>
                            <div className="rounded-md bg-card p-2">
                                <div className="font-mono font-semibold text-foreground">{importPreview.mappingCount}</div>
                                <div className="text-muted-foreground">{t('normalize.explicitMappings.title')}</div>
                            </div>
                        </div>
                        <div className="flex gap-2">
                            <Button
                                type="button"
                                size="sm"
                                className="flex-1 rounded-xl"
                                onClick={applyImport}
                            >
                                <Check className="mr-1 size-3.5" />
                                {t('normalize.import.apply')}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="flex-1 rounded-xl"
                                onClick={() => { setImportPreview(null); if (importFileRef.current) importFileRef.current.value = ''; }}
                            >
                                {t('normalize.import.cancel')}
                            </Button>
                        </div>
                    </div>
                )}
            </div>

            <div className="h-px bg-border/50" />

            {/* 覆盖分析 */}
            <div className="space-y-3 rounded-lg border-border/30 bg-card p-3 sm:p-4 shadow-sm">
                <div className="flex items-center justify-between gap-2">
                    <div className="space-y-0.5">
                        <div className="text-sm font-semibold text-card-foreground flex items-center gap-2">
                            <Search className="size-4" />
                            {t('normalize.analysis.title')}
                        </div>
                        <p className="text-xs leading-5 text-muted-foreground">{t('normalize.analysis.description')}</p>
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="shrink-0 rounded-xl"
                        onClick={handleAnalyze}
                        disabled={analyzing || channelsLoading || channelCount === 0}
                    >
                        {analyzing || channelsLoading ? <Loader2 className="size-3.5 animate-spin" /> : <Search className="size-3.5" />}
                        {analyzing ? t('normalize.analysis.analyzing') : t('normalize.analysis.button')}
                    </Button>
                </div>

                {channelCount > 0 && (
                    <p className="text-xs text-muted-foreground">
                        {t('normalize.analysis.channelCount', { count: channelCount })}
                    </p>
                )}

                {analysis && (
                    <div className="space-y-4 pt-1">
                        {/* 已归并簇 */}
                        <div className="space-y-2">
                            <div className="text-xs font-semibold text-emerald-600 dark:text-emerald-400 flex items-center gap-1.5">
                                <CheckCircle2 className="size-3.5" />
                                {t('normalize.analysis.merged', { count: analysis.merged.length })}
                            </div>
                            {analysis.merged.length === 0 ? (
                                <p className="text-xs text-muted-foreground pl-5">{t('normalize.analysis.mergedEmpty')}</p>
                            ) : (
                                <div className="max-h-48 space-y-1.5 overflow-y-auto pl-5">
                                    {analysis.merged.slice(0, 50).map((cluster) => (
                                        <div key={cluster.canonical} className="rounded-md border border-border/30 bg-card px-2.5 py-1.5">
                                            <div className="text-xs font-mono font-medium text-foreground break-all">{cluster.canonical}</div>
                                            <div className="mt-0.5 flex flex-wrap gap-1">
                                                {cluster.originals.map((o) => (
                                                    <span key={o} className="text-[10px] font-mono text-muted-foreground break-all">{o}</span>
                                                ))}
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            )}
                        </div>

                        {/* 疑似漏归并 */}
                        <div className="space-y-2">
                            <div className="text-xs font-semibold text-amber-600 dark:text-amber-400 flex items-center gap-1.5">
                                <AlertCircle className="size-3.5" />
                                {t('normalize.analysis.suspected', { count: analysis.suspected.length })}
                            </div>
                            {analysis.suspected.length === 0 ? (
                                <p className="text-xs text-muted-foreground pl-5">{t('normalize.analysis.suspectedEmpty')}</p>
                            ) : (
                                <div className="max-h-64 space-y-1.5 overflow-y-auto pl-5">
                                    {analysis.suspected.slice(0, 50).map((cluster) => (
                                        <div key={cluster.base} className="rounded-md border border-amber-500/20 bg-amber-500/5 px-2.5 py-1.5">
                                            <div className="text-xs font-mono font-medium text-foreground break-all">
                                                {cluster.base}
                                                <span className="ml-1.5 text-muted-foreground">→</span>
                                            </div>
                                            <div className="mt-0.5 space-y-0.5">
                                                {cluster.variants.map((v) => (
                                                    <div key={v.original} className="flex items-center gap-1.5 text-[10px]">
                                                        <span className="font-mono text-muted-foreground break-all">{v.original}</span>
                                                        <span className="shrink-0 text-amber-600 dark:text-amber-400 font-mono">[{v.diff}]</span>
                                                        <button
                                                            type="button"
                                                            className={cn(
                                                                'shrink-0 rounded px-1 py-0.5 text-[9px] font-medium transition-colors',
                                                                v.diff.startsWith('-')
                                                                    ? 'text-amber-600 hover:bg-amber-500/15 dark:text-amber-400'
                                                                    : 'text-blue-600 hover:bg-blue-500/15 dark:text-blue-400'
                                                            )}
                                                            onClick={() => v.diff.startsWith('-') ? addSuffixFromAnalysis(v.diff) : addPrefixFromAnalysis(v.diff)}
                                                        >
                                                            +{v.diff.startsWith('-') ? t('normalize.analysis.addSuffix') : t('normalize.analysis.addPrefix')}
                                                        </button>
                                                    </div>
                                                ))}
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            )}
                            <p className="text-[10px] text-muted-foreground/70 pl-5">{t('normalize.analysis.disclaimer')}</p>
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
}
