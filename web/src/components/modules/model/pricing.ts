// 模型广场卡片的纯展示 / 校验逻辑，供桌面 Item、移动 MobileModelItem 与 Create 共用。
// 保持无 React 依赖，便于用 node:test 直接单测（pricing.test.ts）。

import type { ModelMarketChannel } from '@/api/endpoints/model';

/** DeepSeek 峰谷计费标识（目录价为高峰价，空闲减半），只读展示用。 */
export const PEAK_BILLING_SCHEDULE = 'deepseek_v4';

export function isPeakBillingSchedule(schedule: string | null | undefined): boolean {
    return schedule === PEAK_BILLING_SCHEDULE;
}

export type PriceField = 'input' | 'output' | 'cache_read' | 'cache_write';

export type PriceDraft = Record<PriceField, string>;

export const PRICE_FIELDS: readonly PriceField[] = ['input', 'output', 'cache_read', 'cache_write'];

export const PRICE_FIELD_LABEL_KEYS: Record<PriceField, 'input' | 'output' | 'cacheRead' | 'cacheWrite'> = {
    input: 'input',
    output: 'output',
    cache_read: 'cacheRead',
    cache_write: 'cacheWrite',
};

// 接受非负十进制数字（可带小数点、允许 ".5"）与常规科学计数法（"1e-7"），
// 拒绝负号 / 无穷 / 缺失指数的残缺写法 / 十六进制 / 多个小数点 / 杂文本。
const PRICE_PATTERN = /^\d*\.?\d+(?:[eE][+-]?\d+)?$/;

/**
 * 严格解析价格输入。
 * - 空串（含纯空白）→ null：留空不再被静默清零，免费需显式输入 0
 * - 合法非负数（含 "1e-7" 这类科学计数法）→ 数值，避免 toFixed 展开把极小有效价清成 0
 * - 其余（负数、NaN、Infinity、残缺指数、"1.2.3" 等）→ null，由调用方阻断提交并给出提示
 *
 * 取代原先 `parseFloat(x) || 0` 的静默清零：负数与乱输入不再被吞成 0。
 */
export function parsePriceInput(raw: string): number | null {
    const trimmed = raw.trim();
    if (trimmed === '') return null;
    if (!PRICE_PATTERN.test(trimmed)) return null;
    const value = Number(trimmed);
    return Number.isFinite(value) ? value : null;
}

export interface ParsedPriceDraft {
    prices: Record<PriceField, number>;
    invalidFields: PriceField[];
}

/** 批量解析四个价格字段，返回解析结果与非法字段列表（保持字段顺序）。 */
export function parsePriceDraft(draft: PriceDraft): ParsedPriceDraft {
    const prices = {} as Record<PriceField, number>;
    const invalidFields: PriceField[] = [];
    for (const field of PRICE_FIELDS) {
        const value = parsePriceInput(draft[field]);
        if (value === null) {
            invalidFields.push(field);
        } else {
            prices[field] = value;
        }
    }
    return { prices, invalidFields };
}

/** 数值 → 输入框草稿文本；极小数保留原生科学计数法（"1e-7"），parsePriceInput 可原样解析回来。 */
export function formatPriceDraftValue(value: number): string {
    if (!Number.isFinite(value)) return '0';
    return String(value);
}

export function priceDraftFromModel(model: Pick<Record<PriceField, number>, PriceField>): PriceDraft {
    return {
        input: formatPriceDraftValue(model.input),
        output: formatPriceDraftValue(model.output),
        cache_read: formatPriceDraftValue(model.cache_read),
        cache_write: formatPriceDraftValue(model.cache_write),
    };
}

/** 成功 + 失败请求总数。 */
export function totalRequests(requestSuccess: number, requestFailed: number): number {
    return requestSuccess + requestFailed;
}

/** 成功率统一展示：无请求时为 "—"，保留 1 位小数（桌面 / 移动一致）。 */
export function formatSuccessRate(successRate: number, requestCount: number): string {
    if (!Number.isFinite(successRate) || requestCount <= 0) return '—';
    return `${(successRate * 100).toFixed(1)}%`;
}

/**
 * "输入/缓存读" 或 "输出/缓存写" 成对价格文本。
 * 中国模式按汇率换算为 ¥，否则美元；汇率异常时回退 1 防止 NaN 污染展示。
 */
export function formatPricePair(
    primary: number,
    secondary: number,
    chinaMode: boolean,
    exchangeRate: number,
): string {
    const rate = Number.isFinite(exchangeRate) && exchangeRate > 0 ? exchangeRate : 1;
    const amount = (value: number) => (Number.isFinite(value) ? value : 0) * (chinaMode ? rate : 1);
    const symbol = chinaMode ? '¥' : '$';
    return `${amount(primary).toFixed(2)}/${amount(secondary).toFixed(2)}${symbol}`;
}

export interface ChannelTagSummary {
    visible: ModelMarketChannel[];
    hiddenCount: number;
}

/** 渠道标签截断：最多展示 maxVisible 个，其余折叠成 +N。 */
export function summarizeChannelTags(channels: ModelMarketChannel[], maxVisible: number): ChannelTagSummary {
    const safeMax = Math.max(0, maxVisible);
    return {
        visible: channels.slice(0, safeMax),
        hiddenCount: Math.max(0, channels.length - safeMax),
    };
}
