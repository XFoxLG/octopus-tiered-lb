import type { KeyModelResult } from '@/api/endpoints/channel';

/**
 * 「按 key 抓取模型」结果回填所需的表单 key 最小结构。
 * 与 ChannelKeyFormItem 兼容（结构子集），方便单测独立构造数据。
 */
export type PerKeyFillKey = {
    id?: number;
    channel_key: string;
    remark?: string;
    supported_models?: string;
};

export type PerKeyFillMatchedBy = 'id' | 'masked' | 'remark';

/** 单条抓取结果与表单 key 的匹配结论 */
export type PerKeyFillMatch = {
    /** 抓取结果在 results 数组中的下标 */
    resultIndex: number;
    result: KeyModelResult;
    /** 命中的表单 key 下标；null 表示没匹配上 */
    keyIndex: number | null;
    matchedBy: PerKeyFillMatchedBy | null;
};

/** 回填计划：只描述"要改哪些表单状态"，不做任何网络请求 */
export type PerKeyFillPlan = {
    /** 需要写入 supported_models 的表单 key（按 keyIndex 去重） */
    fills: Array<{ keyIndex: number; supportedModels: string; matchedBy: PerKeyFillMatchedBy | null }>;
    /** 因 passed=false 被跳过的结果 */
    skippedFailed: PerKeyFillMatch[];
    /** 抓取成功但上游返回空模型列表的结果：不写空串，避免静默解除已有限制 */
    skippedEmpty: PerKeyFillMatch[];
    /** 因匹配不到表单 key 被跳过的结果 */
    unmatched: PerKeyFillMatch[];
};

/**
 * 与后端 helper.maskSecret 保持一致的脱敏规则：
 * 前 4 位 + "..." + 后 4 位；长度 <= 8 时原样返回；空串返回空串。
 */
export function maskChannelKeySecret(secret: string): string {
    const trimmed = (secret ?? '').trim();
    if (!trimmed) return '';
    if (trimmed.length <= 8) return trimmed;
    return trimmed.slice(0, 4) + '...' + trimmed.slice(-4);
}

/**
 * 为单条抓取结果在表单 keys 中找目标下标。
 *
 * 匹配优先级：
 * 1. key_id（后端新增字段）与表单 key.id 精确相等；
 * 2. 脱敏后的 channel_key 与 result.key_masked 相等（未保存的新 key 没有 id，靠这个兜底）；
 *    多个候选时优先备注也相等的那个，避免同前缀/后缀的 key 串行；
 * 3. result.key_remark 与表单 key.remark 相等（脱敏值缺失时的最后兜底）。
 *
 * usedIndexes 保证一次批量回填里同一个表单 key 不会被写两次。
 */
export function matchPerKeyResultToFormKey(
    result: KeyModelResult,
    keys: PerKeyFillKey[],
    usedIndexes: ReadonlySet<number> = new Set(),
): { keyIndex: number | null; matchedBy: PerKeyFillMatchedBy | null } {
    const list = keys ?? [];
    const keyId = typeof result.key_id === 'number' ? result.key_id : 0;

    if (keyId > 0) {
        const byId = list.findIndex((key, index) => !usedIndexes.has(index) && key.id === keyId);
        if (byId >= 0) return { keyIndex: byId, matchedBy: 'id' };
    }

    const masked = (result.key_masked ?? '').trim();
    if (masked) {
        const remark = (result.key_remark ?? '').trim();
        const candidates = list
            .map((key, index) => ({ key, index }))
            .filter(({ key, index }) => !usedIndexes.has(index) && maskChannelKeySecret(key.channel_key) === masked);

        if (candidates.length > 0) {
            const preferred = remark
                ? candidates.find(({ key }) => (key.remark ?? '').trim() === remark)
                : undefined;
            const hit = preferred ?? candidates[0];
            return { keyIndex: hit.index, matchedBy: 'masked' };
        }
    }

    const fallbackRemark = (result.key_remark ?? '').trim();
    if (fallbackRemark) {
        const byRemark = list.findIndex(
            (key, index) => !usedIndexes.has(index) && (key.remark ?? '').trim() === fallbackRemark,
        );
        if (byRemark >= 0) return { keyIndex: byRemark, matchedBy: 'remark' };
    }

    return { keyIndex: null, matchedBy: null };
}

/**
 * 计算「一键填充」计划：把每个 passed 的抓取结果的 models 以逗号连接，
 * 写入匹配到的表单 key 的 supported_models。
 *
 * - passed=false 的结果默认跳过（不覆盖用户已有值），进入 skippedFailed；
 * - passed=true 但 models 为空的结果同样跳过（写空串会静默解除已有限制），进入 skippedEmpty；
 * - 匹配不到表单 key 的结果进入 unmatched。
 */
export function planPerKeyModelFill(
    results: KeyModelResult[],
    keys: PerKeyFillKey[],
    options?: { includeFailed?: boolean },
): PerKeyFillPlan {
    const list = results ?? [];
    const includeFailed = options?.includeFailed === true;
    const usedIndexes = new Set<number>();
    const fills: PerKeyFillPlan['fills'] = [];
    const skippedFailed: PerKeyFillMatch[] = [];
    const skippedEmpty: PerKeyFillMatch[] = [];
    const unmatched: PerKeyFillMatch[] = [];

    for (let resultIndex = 0; resultIndex < list.length; resultIndex += 1) {
        const result = list[resultIndex];
        const passed = result.passed === true;

        if (!passed && !includeFailed) {
            skippedFailed.push({ resultIndex, result, keyIndex: null, matchedBy: null });
            continue;
        }

        const models = (result.models ?? []).map((model) => model.trim()).filter(Boolean);
        if (models.length === 0) {
            skippedEmpty.push({ resultIndex, result, keyIndex: null, matchedBy: null });
            continue;
        }

        const { keyIndex, matchedBy } = matchPerKeyResultToFormKey(result, keys, usedIndexes);
        if (keyIndex === null) {
            unmatched.push({ resultIndex, result, keyIndex: null, matchedBy: null });
            continue;
        }

        usedIndexes.add(keyIndex);
        fills.push({
            keyIndex,
            matchedBy,
            supportedModels: models.join(','),
        });
    }

    return { fills, skippedFailed, skippedEmpty, unmatched };
}

/**
 * 按计划生成新的 keys 数组（不修改入参），供 setState 使用。
 */
export function applyPerKeyModelFill<T extends PerKeyFillKey>(
    keys: T[],
    fills: Array<Pick<PerKeyFillPlan['fills'][number], 'keyIndex' | 'supportedModels'>>,
): T[] {
    const list = keys ?? [];
    if (!fills || fills.length === 0) return list;

    const byIndex = new Map<number, string>();
    for (const fill of fills) {
        if (fill.keyIndex >= 0 && fill.keyIndex < list.length) {
            byIndex.set(fill.keyIndex, fill.supportedModels);
        }
    }
    if (byIndex.size === 0) return list;

    return list.map((key, index) =>
        byIndex.has(index) ? { ...key, supported_models: byIndex.get(index) } : key,
    );
}
