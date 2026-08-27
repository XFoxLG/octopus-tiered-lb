import assert from 'node:assert/strict';
import test from 'node:test';

import type { LLMChannel } from '../../../api/endpoints/model';
import { filterEnabledModelChannels } from './utils.ts';

function channel(overrides: Partial<LLMChannel>): LLMChannel {
    return {
        name: 'gpt-4o',
        enabled: true,
        channel_id: 1,
        channel_name: 'channel-a',
        ...overrides,
    };
}

test('filterEnabledModelChannels drops models from disabled channels', () => {
    const input = [
        channel({ channel_id: 1, channel_name: 'enabled-a' }),
        channel({ channel_id: 2, channel_name: 'disabled-b', enabled: false }),
        channel({ channel_id: 3, channel_name: 'enabled-c' }),
    ];

    const result = filterEnabledModelChannels(input);

    assert.deepEqual(
        result.map((mc) => mc.channel_name),
        ['enabled-a', 'enabled-c'],
    );
});

test('filterEnabledModelChannels treats a missing enabled field as enabled', () => {
    const input = [channel({ channel_id: 1 }), channel({ channel_id: 2, enabled: false })];

    const result = filterEnabledModelChannels(input);

    assert.equal(result.length, 1);
    assert.equal(result[0].channel_id, 1);
});
