/**
 * 价格分类 / 峰谷计费共享的表单纯函数：解析、校验、展示格式化。
 *
 * 校验语义与后端对齐（internal/op/llm/price_category.go、price_schedule.go）：
 *   - name 必填（后端 trim + 转小写，唯一索引）
 *   - rule_type ∈ exact | prefix | contains
 *   - rule_value 必填（匹配时后端再 trim + 小写）
 *   - 四项价格与 off_peak_mul 为有限数且 >= 0（后端无上限约束）
 *   - sort_order 为整数（后端无约束，可为负；越小越先匹配）
 *   - 窗口为自 00:00 起的分钟数（北京时间由后端 BillingWindow 固定判定，与展示
 *     时区无关），约束 0 <= start <= end <= 1440；start == end（含 0,0）表示该
 *     窗口关闭，两窗口均关闭 = 全天空闲
 *   - 两窗口重叠后端允许（并集即高峰时段），前端仅提示不阻断提交
 *
 * 校验函数返回 i18n key（model.priceRule 命名空间）而非翻译文案，保持纯函数可测试；
 * 翻译由组件层用 useTranslations('model.priceRule') 完成。
 */

export type PriceRuleType = 'exact' | 'prefix' | 'contains'

export const PRICE_RULE_TYPES = ['exact', 'prefix', 'contains'] as const

export function isPriceRuleType(value: string): value is PriceRuleType {
    return (PRICE_RULE_TYPES as readonly string[]).includes(value);
}

/**
 * 解析价格输入：空白无效，零价格需显式输入 0；
 * 非有限数（NaN / Infinity / 非法文本）返回 null，由调用方报错而不是静默归零。
 */
export function parsePriceInput(value: string): number | null {
    const trimmed = value.trim();
    if (!/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(trimmed)) return null;
    const n = Number(trimmed);
    return Number.isFinite(n) ? n : null;
}

/** 解析优先级，仅接受安全整数；空白、小数和非法文本返回 null。 */
export function parseIntegerInput(value: string): number | null {
    const trimmed = value.trim();
    if (trimmed === '') return null;
    if (!/^[+-]?\d+$/.test(trimmed)) return null;
    const parsed = Number(trimmed);
    return Number.isSafeInteger(parsed) ? parsed : null;
}

// ---------------------------------------------------------------------------
// 时间窗（自 00:00 起的分钟数；窗口语义固定为北京时间，由后端判定，前端不做时区换算）
// ---------------------------------------------------------------------------

export const MINUTES_PER_DAY = 1440;

/**
 * 分钟 → "HH:MM"。
 * 1440 保留为 24:00，避免编辑全天窗口时丢失最后一分钟。
 */
export function minutesToHHMM(m: number): string {
    if (!Number.isFinite(m)) return '00:00';
    const clamped = Math.min(Math.max(Math.floor(m), 0), MINUTES_PER_DAY);
    const h = Math.floor(clamped / 60);
    const mm = clamped % 60;
    return `${String(h).padStart(2, '0')}:${String(mm).padStart(2, '0')}`;
}

/** "HH:MM" → 分钟；接受日末 24:00，非法时间或空白返回 null。 */
export function hhmmToMinutes(value: string): number | null {
    const trimmed = value.trim();
    if (trimmed === '') return null;
    const m = /^(\d{1,2}):(\d{2})$/.exec(trimmed);
    if (!m) return null;
    const h = Number(m[1]);
    const mm = Number(m[2]);
    if (h > 24 || mm > 59 || h === 24 && mm !== 0) return null;
    return h * 60 + mm;
}

/** 时间窗表单输入：值为 "HH:MM" 或空字符串（未填写）。 */
export interface WindowInput {
    start: string;
    end: string;
}

/** 时间窗校验失败原因：incomplete = 只填一端或非法文本；inverted = start > end。 */
export type WindowErrorReason = 'incomplete' | 'inverted';

export interface ResolvedWindow {
    start: number;
    end: number;
}

export type WindowResolveResult =
    | { ok: true; value: ResolvedWindow }
    | { ok: false; reason: WindowErrorReason };

/**
 * 解析单个时间窗：
 *   - 两端全空 → 关闭窗 {0,0}（后端语义：start==end 的窗口不生效）
 *   - 只填一端或含非法文本 → incomplete
 *   - start > end → inverted（后端拒绝）；start == end → 关闭窗（后端接受）
 */
export function resolveWindow(w: WindowInput): WindowResolveResult {
    if (w.start.trim() === '' && w.end.trim() === '') {
        return { ok: true, value: { start: 0, end: 0 } };
    }
    const start = hhmmToMinutes(w.start);
    const end = hhmmToMinutes(w.end);
    if (start === null || end === null) return { ok: false, reason: 'incomplete' };
    if (start > end) return { ok: false, reason: 'inverted' };
    return { ok: true, value: { start, end } };
}

/** 校验失败原因 → model.priceRule 下的 i18n key。 */
export function windowErrorKey(reason: WindowErrorReason): string {
    return reason === 'inverted' ? 'windowInverted' : 'windowIncomplete';
}

/**
 * 两个 [start, end) 窗口是否重叠。关闭窗（start >= end，含 0,0）不参与重叠。
 * 后端允许重叠（并集即高峰），结果仅用于提示，不阻断提交。
 */
export function windowsOverlap(a: ResolvedWindow, b: ResolvedWindow): boolean {
    if (a.start >= a.end || b.start >= b.end) return false;
    return a.start < b.end && b.start < a.end;
}

/** 表格里的高峰时段展示文本（如 "09:00-12:00 / 14:00-18:00"）；两窗全关返回 null。 */
export function formatWindowsLabel(w1: ResolvedWindow, w2: ResolvedWindow): string | null {
    const parts: string[] = [];
    if (w1.start < w1.end) parts.push(`${minutesToHHMM(w1.start)}-${minutesToHHMM(w1.end)}`);
    if (w2.start < w2.end) parts.push(`${minutesToHHMM(w2.start)}-${minutesToHHMM(w2.end)}`);
    return parts.length > 0 ? parts.join(' / ') : null;
}

/** 编辑回显：生效窗 → "HH:MM"；关闭窗（start >= end）→ 两端留空（与"留空=关闭"一致）。 */
export function windowToForm(w: ResolvedWindow): WindowInput {
    if (w.start >= w.end) return { start: '', end: '' };
    return { start: minutesToHHMM(w.start), end: minutesToHHMM(w.end) };
}

// ---------------------------------------------------------------------------
// 表单状态与校验
// ---------------------------------------------------------------------------

/** 两个视图共享的基础表单字段（价格分类 = 全部；峰谷规则另加扩展字段）。 */
export interface PriceRuleFormBase {
    name: string;
    rule_type: PriceRuleType;
    rule_value: string;
    input: string;
    output: string;
    cache_read: string;
    cache_write: string;
    sort_order: string;
    enabled: boolean;
}

const PRICE_FIELDS = ['input', 'output', 'cache_read', 'cache_write'] as const;

/** 基础字段校验错误：值为 model.priceRule 命名空间下的 i18n key。 */
export interface PriceRuleBaseErrors {
    name?: string;
    rule_value?: string;
    input?: string;
    output?: string;
    cache_read?: string;
    cache_write?: string;
    sort_order?: string;
}

/** 校验基础字段；错误值为 i18n key，由调用方翻译。 */
export function validatePriceRuleBase(form: PriceRuleFormBase): PriceRuleBaseErrors {
    const errors: PriceRuleBaseErrors = {};
    if (form.name.trim() === '') errors.name = 'nameRequired';
    if (form.rule_value.trim() === '') errors.rule_value = 'ruleValueRequired';
    for (const field of PRICE_FIELDS) {
        const parsed = parsePriceInput(form[field]);
        if (parsed === null) errors[field] = 'invalidNumber';
        else if (parsed < 0) errors[field] = 'nonNegativeRequired';
    }
    if (parseIntegerInput(form.sort_order) === null) errors.sort_order = 'invalidInteger';
    return errors;
}

/** 是否存在任一校验错误。参数用 object 以兼容 interface 类型（无隐式索引签名）。 */
export function hasErrors(errors: object): boolean {
    return Object.values(errors).some((v) => v !== undefined);
}

export interface PriceRuleBasePayload {
    name: string;
    rule_type: PriceRuleType;
    rule_value: string;
    input: number;
    output: number;
    cache_read: number;
    cache_write: number;
    sort_order: number;
    enabled: boolean;
}

/**
 * 生成提交载荷：name / rule_value trim（大小写交由后端归一，与现有行为一致）；
 * 应在 validatePriceRuleBase 通过后调用，非法值此处兜底为 0。
 */
export function buildPriceRuleBasePayload(form: PriceRuleFormBase): PriceRuleBasePayload {
    return {
        name: form.name.trim(),
        rule_type: form.rule_type,
        rule_value: form.rule_value.trim(),
        input: parsePriceInput(form.input) ?? 0,
        output: parsePriceInput(form.output) ?? 0,
        cache_read: parsePriceInput(form.cache_read) ?? 0,
        cache_write: parsePriceInput(form.cache_write) ?? 0,
        sort_order: parseIntegerInput(form.sort_order) ?? 0,
        enabled: form.enabled,
    };
}

/** 价格数值展示（编辑回显用）。 */
export function formatPriceValue(v: number): string {
    return String(v ?? 0);
}

// ---------------------------------------------------------------------------
// 峰谷规则扩展字段
// ---------------------------------------------------------------------------

/** 峰谷规则扩展校验错误（在基础错误之上）。 */
export interface PriceScheduleErrors extends PriceRuleBaseErrors {
    off_peak_mul?: string;
    w1?: WindowErrorReason;
    w2?: WindowErrorReason;
}

export interface ResolvedScheduleWindows {
    w1: ResolvedWindow;
    w2: ResolvedWindow;
    overlap: boolean;
}

/**
 * 校验并解析两段时间窗。任一窗口非法时返回 ok:false 与逐窗错误；
 * 全部合法时返回窗口分钟值与重叠提示（重叠不算错误）。
 */
export function resolveScheduleWindows(
    w1: WindowInput,
    w2: WindowInput,
): ({ ok: true } & ResolvedScheduleWindows) | { ok: false; errors: { w1?: WindowErrorReason; w2?: WindowErrorReason } } {
    const r1 = resolveWindow(w1);
    const r2 = resolveWindow(w2);
    const errors: { w1?: WindowErrorReason; w2?: WindowErrorReason } = {};
    if (!r1.ok) errors.w1 = r1.reason;
    if (!r2.ok) errors.w2 = r2.reason;
    if (!r1.ok || !r2.ok) return { ok: false, errors };
    return { ok: true, w1: r1.value, w2: r2.value, overlap: windowsOverlap(r1.value, r2.value) };
}
