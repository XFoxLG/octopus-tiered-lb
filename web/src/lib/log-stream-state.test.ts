import assert from 'node:assert/strict';
import test from 'node:test';
import { mergeLogPages, mergeLiveRequest } from './log-stream-state.ts';
import { withExactId } from './exact-id.ts';

test('SSE replaces pending metadata without collapsing adjacent snowflake IDs', () => {
    const first = withExactId(JSON.parse('{"id":117392002743437824,"id_str":"117392002743437824","state":"pending"}'));
    const second = withExactId(JSON.parse('{"id":117392002743437825,"id_str":"117392002743437825","state":"pending"}'));
    const pages = mergeLogPages([[first], [second]], {...second, state: 'ready'});
    assert.equal(pages[0][0].state, 'pending');
    assert.equal(pages[1][0].state, 'ready');
    assert.equal(pages.flat().length, 2);
});

test('live request becomes one final record; memory remains bounded', () => {
    const received = {trace_id:'trace',state:'received'};
    const streaming = mergeLiveRequest([received], {...received,state:'streaming'});
    assert.deepEqual(streaming,[{trace_id:'trace',state:'streaming'}]);
    assert.deepEqual(mergeLiveRequest(streaming,{...received,state:'recorded'}),[]);
    const many = Array.from({length:256}, (_,index) => ({trace_id:String(index),state:'waiting'}));
    assert.equal(mergeLiveRequest(many,received).length,256);
});


test('head refresh keeps history and removes entries outside the newest filtered window', async () => {
    const { mergeLogHead } = await import('./log-stream-state.ts');
    const pages = [[{ id: 'pending', time: 110 }, { id: 'old', time: 80 }], [{ id: 'history', time: 50 }]];
    const incoming = [{ id: 'new', time: 120 }, { id: 'edge', time: 100 }];
    assert.deepEqual(mergeLogHead(pages, incoming), [
        [incoming[0], incoming[1], { id: 'old', time: 80 }],
        [{ id: 'history', time: 50 }],
    ]);
    assert.deepEqual(mergeLogHead(pages, []), [[], []]);
    const streamed = { id: 'live-during-refresh', time: 130 };
    assert.equal(mergeLogHead([[streamed, ...pages[0]], pages[1]], incoming, new Set(['pending', 'old', 'history']))[0].some(log => log.id === streamed.id), true);
});
