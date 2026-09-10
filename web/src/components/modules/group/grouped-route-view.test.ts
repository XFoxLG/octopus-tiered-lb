import assert from 'node:assert/strict';
import test from 'node:test';
import type { Group } from '../../../api/endpoints/group.ts';
import {
    buildGroupedRouteModelCategories,
    UNGROUPED_BUCKET_ID,
    UNCATEGORIZED_CATEGORY_ID,
    type GroupedRouteModelRow,
} from './grouped-view.ts';

interface GroupItemSeed {
    group_id: number;
    channel_id: number;
    model_name: string;
    priority: number;
    weight: number;
}

function group_(
    groupId: number,
    name: string,
    items: GroupItemSeed[],
    category = '',
): Group {
    return {
        id: groupId,
        name,
        category,
        endpoint_type: 'chat',
        endpoint_provider: 'openai',
        outbound_format: '',
        mode: 1,
        match_regex: '',
        first_token_time_out: 0,
        attempt_time_out: 0,
        session_keep_time: 0,
        condition: '',
        items,
    };
}

function channel(channelId: number, modelName: string, channelName = `#${channelId}`, enabled = true): GroupedRouteModelRow {
    return { id: `${channelId}-${modelName}`, name: modelName, channel_id: channelId, channel_name: channelName, enabled };
}

test('groups with the same category aggregate into one category bucket, preserving first-seen order', () => {
    const groups = [
        group_(1, 'G1', [{ group_id: 1, channel_id: 1, model_name: 'a', priority: 1, weight: 1 }], '对话'),
        group_(2, 'G2', [{ group_id: 2, channel_id: 1, model_name: 'b', priority: 1, weight: 1 }], '嵌入'),
        group_(3, 'G3', [{ group_id: 3, channel_id: 1, model_name: 'c', priority: 1, weight: 1 }], '对话'),
    ];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B'), channel(1, 'c', 'C')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels);

    assert.equal(categories.length, 2, 'two distinct categories');
    assert.deepEqual(categories.map((c) => c.name), ['对话', '嵌入']);
    // First category contains G1 + G3 (same category), preserving group input order.
    assert.deepEqual(
        categories[0].buckets.map((b) => b.name),
        ['G1', 'G3'],
        'groups in same category keep input order',
    );
    // totalModelCount aggregates across all group buckets in the category.
    assert.equal(categories[0].totalModelCount, 2);
    assert.equal(categories[1].totalModelCount, 1);
});

test('groups without a category fall into the uncategorized bucket, placed after named categories', () => {
    const groups = [
        group_(1, 'G1', [{ group_id: 1, channel_id: 1, model_name: 'a', priority: 1, weight: 1 }], '对话'),
        group_(2, 'G2', [{ group_id: 2, channel_id: 1, model_name: 'b', priority: 1, weight: 1 }]),
        group_(3, 'G3', [{ group_id: 3, channel_id: 1, model_name: 'c', priority: 1, weight: 1 }], '对话'),
        group_(4, 'G4', [{ group_id: 4, channel_id: 1, model_name: 'd', priority: 1, weight: 1 }]),
    ];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B'), channel(1, 'c', 'C'), channel(1, 'd', 'D')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels);

    assert.deepEqual(
        categories.map((c) => c.kind),
        ['category', 'uncategorized'],
        'uncategorized comes after named categories',
    );
    const uncategorized = categories.find((c) => c.kind === 'uncategorized');
    assert.ok(uncategorized, 'uncategorized bucket exists');
    assert.equal(uncategorized.id, UNCATEGORIZED_CATEGORY_ID);
    assert.deepEqual(
        uncategorized.buckets.map((b) => b.name),
        ['G2', 'G4'],
        'uncategorized contains all no-category groups in input order',
    );
});

test('ungrouped models (not referenced by any group) form the final ungrouped category bucket', () => {
    const groups = [group_(1, 'G', [{ group_id: 1, channel_id: 1, model_name: 'b', priority: 1, weight: 1 }], '对话')];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B'), channel(2, 'c', 'C')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels);
    const ungrouped = categories.find((c) => c.id === UNGROUPED_BUCKET_ID);

    assert.ok(ungrouped, 'ungrouped category bucket exists');
    assert.equal(ungrouped.kind, 'ungrouped');
    assert.deepEqual(ungrouped.models!.map((m) => m.name), ['a', 'c']);
    assert.equal(ungrouped.totalModelCount, 2);
    // Placed last.
    assert.equal(categories[categories.length - 1].id, UNGROUPED_BUCKET_ID);
});

test('members are sorted by item priority within each group bucket', () => {
    const groups = [
        group_(1, 'G', [
            { group_id: 1, channel_id: 1, model_name: 'c', priority: 5, weight: 1 },
            { group_id: 1, channel_id: 1, model_name: 'a', priority: 1, weight: 1 },
            { group_id: 1, channel_id: 1, model_name: 'b', priority: 2, weight: 1 },
        ], '对话'),
    ];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B'), channel(1, 'c', 'C')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels);

    assert.deepEqual(
        categories[0].buckets[0].models.map((m) => m.name),
        ['a', 'b', 'c'],
        'models sorted by priority ascending',
    );
});

test('searching by category name keeps the whole category with all its groups and models', () => {
    const groups = [
        group_(1, 'Alpha', [{ group_id: 1, channel_id: 1, model_name: 'a', priority: 1, weight: 1 }], '对话'),
        group_(2, 'Beta', [{ group_id: 2, channel_id: 1, model_name: 'b', priority: 1, weight: 1 }], '嵌入'),
        group_(3, 'Gamma', [{ group_id: 3, channel_id: 1, model_name: 'c', priority: 1, weight: 1 }], '对话'),
    ];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B'), channel(1, 'c', 'C')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels, '对话');

    assert.equal(categories.length, 1, 'only the matched category remains');
    assert.equal(categories[0].name, '对话');
    assert.deepEqual(
        categories[0].buckets.map((b) => b.name),
        ['Alpha', 'Gamma'],
        'both groups under the matched category are kept',
    );
});

test('searching by group name keeps that group (and its category) with full members', () => {
    const groups = [
        group_(1, 'Alpha', [{ group_id: 1, channel_id: 1, model_name: 'a', priority: 1, weight: 1 }], '对话'),
        group_(2, 'Beta', [{ group_id: 2, channel_id: 1, model_name: 'b', priority: 1, weight: 1 }], '对话'),
    ];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels, 'alpha');

    assert.equal(categories.length, 1);
    assert.equal(categories[0].buckets.length, 1);
    assert.equal(categories[0].buckets[0].name, 'Alpha');
    assert.deepEqual(categories[0].buckets[0].models.map((m) => m.name), ['a']);
});

test('searching by model name filters member rows but keeps the group and category', () => {
    const groups = [
        group_(1, 'G', [
            { group_id: 1, channel_id: 1, model_name: 'a', priority: 1, weight: 1 },
            { group_id: 1, channel_id: 1, model_name: 'b', priority: 2, weight: 1 },
        ], '对话'),
    ];
    const modelChannels = [channel(1, 'a', 'A'), channel(1, 'b', 'B')];

    const categories = buildGroupedRouteModelCategories(groups, modelChannels, 'b');

    assert.equal(categories.length, 1);
    assert.equal(categories[0].buckets.length, 1);
    assert.deepEqual(categories[0].buckets[0].models.map((m) => m.name), ['b']);
});
