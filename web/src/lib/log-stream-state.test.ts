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
