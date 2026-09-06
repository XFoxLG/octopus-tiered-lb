import type { Group } from '../../../api/endpoints/group';
import type { LLMChannel } from '../../../api/endpoints/model';

export const UNGROUPED_BUCKET_ID = '__ungrouped__' as const;
export const UNCATEGORIZED_CATEGORY_ID = '__uncategorized__' as const;

export interface GroupedRouteModelRow {
    id: string;
    name: string;
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    item_id?: number;
    priority?: number;
    weight?: number;
    group_id?: number;
}

export interface GroupedRouteModelBucket {
    id: number | string;
    key: string;
    kind: 'group' | 'ungrouped';
    name: string;
    category?: string;
    endpoint_type?: string;
    group?: Group;
    models: GroupedRouteModelRow[];
    matchedByGroupName: boolean;
}

/**
 * 分类桶：按 Group.category 字段归大类，相同 category 的多个 Group 聚合。
 * 无 category 的 Group 归入「未分类」(UNCATEGORIZED_CATEGORY_ID)。
 */
export interface GroupedRouteModelCategoryBucket {
    id: string;
    name: string;
    kind: 'category' | 'uncategorized' | 'ungrouped';
    /** 该分类下的分组桶（category/uncategorized 有，ungrouped 为空） */
    buckets: GroupedRouteModelBucket[];
    /** 未归属任何分组的路由模型（仅 ungrouped 分类有） */
    models?: GroupedRouteModelRow[];
    /** 分类下模型总数（跨所有分组桶，用于计数显示） */
    totalModelCount: number;
}

export interface BuildGroupedRouteModelBucketsOptions {
    assignedGroups?: Group[];
}

function rowMatches(row: GroupedRouteModelRow, term: string) {
    return row.name.toLowerCase().includes(term) || row.channel_name.toLowerCase().includes(term);
}

function groupMatches(group: Group, term: string) {
    if (!term) return true;
    return [group.name, group.category, group.endpoint_type]
        .filter(Boolean)
        .some((value) => value!.toLowerCase().includes(term));
}


function buildChannelLookup(modelChannels: LLMChannel[]) {
    const lookup = new Map<string, LLMChannel>();
    for (const channelModel of modelChannels) {
        lookup.set(`${channelModel.channel_id}-${channelModel.name}`, channelModel);
    }
    return lookup;
}

function buildAssignedKeySet(groups: Group[]) {
    const assignedKeys = new Set<string>();
    for (const group of groups) {
        for (const item of group.items ?? []) {
            assignedKeys.add(`${item.channel_id}-${item.model_name}`);
        }
    }
    return assignedKeys;
}

/**
 * 按分类（Group.category）归大类，构建三层结构：分类 -> 分组 -> 模型。
 * - 有 category 的 Group 按 category 字段聚合到同一分类桶。
 * - 无 category 的 Group 归入「未分类」分类桶（排在有分类的之后）。
 * - 未归属任何分组的路由模型归入「未分组」分类桶（排在最后）。
 * 搜索语义：category 名匹配 -> 整个分类保留；group 名匹配 -> 该分组所在分类保留；
 * model 名匹配 -> 仅匹配模型保留，其分组与分类保留。
 */
export function buildGroupedRouteModelCategories(
    groups: Group[] | undefined,
    modelChannels: LLMChannel[] | undefined,
    searchTerm = '',
    options: BuildGroupedRouteModelBucketsOptions = {},
): GroupedRouteModelCategoryBucket[] {
    const sourceGroups = groups ?? [];
    const sourceModelChannels = modelChannels ?? [];
    const term = searchTerm.trim().toLowerCase();
    const channelLookup = buildChannelLookup(sourceModelChannels);
    const assignedKeys = buildAssignedKeySet(options.assignedGroups ?? sourceGroups);

    // 1. 按 category 分组，保持首次出现顺序。
    const categoryOrder: string[] = [];
    const bucketsByCategory = new Map<string, GroupedRouteModelBucket[]>();

    for (const group of sourceGroups) {
        const matchedByGroupName = groupMatches(group, term);
        const models = [...(group.items ?? [])]
            .sort((left, right) => left.priority - right.priority)
            .map((item): GroupedRouteModelRow => {
                const key = `${item.channel_id}-${item.model_name}`;
                const channelModel = channelLookup.get(key);
                return {
                    id: key,
                    name: item.model_name,
                    channel_id: item.channel_id,
                    channel_name: channelModel?.channel_name ?? `#${item.channel_id}`,
                    enabled: channelModel?.enabled ?? true,
                    item_id: item.id,
                    priority: item.priority,
                    weight: item.weight,
                    group_id: group.id,
                };
            });
        const visibleModels = term && !matchedByGroupName ? models.filter((model) => rowMatches(model, term)) : models;

        if (term && !matchedByGroupName && visibleModels.length === 0) continue;

        const categoryName = (group.category ?? '').trim();
        const bucketCategoryKey = categoryName || UNCATEGORIZED_CATEGORY_ID;
        if (!bucketsByCategory.has(bucketCategoryKey)) {
            categoryOrder.push(bucketCategoryKey);
            bucketsByCategory.set(bucketCategoryKey, []);
        }
        bucketsByCategory.get(bucketCategoryKey)!.push({
            id: group.id ?? group.name,
            key: `group-${group.id ?? group.name}`,
            kind: 'group',
            name: group.name,
            category: group.category,
            endpoint_type: group.endpoint_type,
            group,
            models: visibleModels,
            matchedByGroupName,
        });
    }

    // 2. 未归属任何分组的模型。
    const ungroupedModels = sourceModelChannels
        .filter((channelModel) => !assignedKeys.has(`${channelModel.channel_id}-${channelModel.name}`))
        .map((channelModel): GroupedRouteModelRow => ({
            id: `${channelModel.channel_id}-${channelModel.name}`,
            name: channelModel.name,
            channel_id: channelModel.channel_id,
            channel_name: channelModel.channel_name,
            enabled: channelModel.enabled,
        }))
        .filter((model) => !term || rowMatches(model, term));

    // 3. 组装分类桶，应用 category 名搜索过滤。
    const categories: GroupedRouteModelCategoryBucket[] = [];

    for (const categoryKey of categoryOrder) {
        const buckets = bucketsByCategory.get(categoryKey)!;
        const isUncategorized = categoryKey === UNCATEGORIZED_CATEGORY_ID;
        const categoryName = isUncategorized ? UNCATEGORIZED_CATEGORY_ID : categoryKey;

        // 搜索词命中分类名 -> 整个分类保留（无需过滤子项）。
        const matchedByCategory = !isUncategorized && (!term || categoryName.toLowerCase().includes(term));

        if (!matchedByCategory && term) {
            // 搜索词未命中分类名，检查是否命中分组名或模型（已在 bucket 构建时过滤）。
            // buckets 中保留的都是通过 group 名或 model 名匹配的；若无则跳过。
            // 但当 term 命中 group 名时，bucket.matchedByGroupName=true 且 models 全保留。
            const hasVisible = buckets.some((b) => b.models.length > 0 || b.matchedByGroupName);
            if (!hasVisible) continue;
        }

        const totalModelCount = buckets.reduce((sum, b) => sum + b.models.length, 0);

        categories.push({
            id: categoryKey,
            name: categoryName,
            kind: isUncategorized ? 'uncategorized' : 'category',
            buckets,
            totalModelCount,
        });
    }

    if (ungroupedModels.length > 0) {
        categories.push({
            id: UNGROUPED_BUCKET_ID,
            name: UNGROUPED_BUCKET_ID,
            kind: 'ungrouped',
            models: ungroupedModels,
            buckets: [],
            totalModelCount: ungroupedModels.length,
        });
    }

    return categories;
}
