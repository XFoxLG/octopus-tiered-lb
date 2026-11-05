import assert from 'node:assert/strict';
import test from 'node:test';
import {
    readableLogBody,
    logIssueSummary,
    resolveContentFilterTextEvidence,
    strictLogTextEvidence,
} from './body-preview.ts';

test('reads visible answer text across supported response envelopes', () => {
    for (const body of [
        {choices:[{message:{content:'Hello'}}]},
        {output:[{type:'message',content:[{type:'output_text',text:'Hello'}]}]},
        {content:[{type:'thinking',thinking:'private'},{type:'text',text:'Hello'}]},
        {candidates:[{content:{parts:[{text:'Hello'}]}}]},
    ]) assert.equal(readableLogBody(JSON.stringify(body)), 'Hello');
});
test('keeps raw fallback for tool-only output and unknown envelopes', () => {
    for (const body of [{choices:[{message:{tool_calls:[{id:'call'}]}}]}, {error:{message:'failed'}}]) {
        assert.equal(readableLogBody(JSON.stringify(body)), JSON.stringify(body, null, 2));
    }
    assert.equal(readableLogBody('plain response'), 'plain response');
});
test('formats conversation and does not silently drop media-only turns', () => {
    assert.equal(readableLogBody(JSON.stringify({messages:[{role:'user',content:'Hello'}]})), 'user\nHello');
    const body = {messages:[{role:'user',content:[{type:'image_url',image_url:{url:'image'}}]}]};
    assert.equal(readableLogBody(JSON.stringify(body)), JSON.stringify(body,null,2));
});

test('joins streaming answer fragments without displaying protocol framing or reasoning', () => {
    const stream = [
        'data: {"choices":[{"delta":{"reasoning_content":"private"}}]}',
        'data: {"choices":[{"delta":{"content":"Hello "}}]}',
        'data: {"choices":[{"delta":{"content":"world"},"finish_reason":"stop"}]}',
        'data: [DONE]',
    ].join('\n\n');
    assert.equal(readableLogBody(stream), 'Hello world');
    assert.equal(readableLogBody('data: {"type":"response.output_text.delta","delta":"Hi"}\n\n'), 'Hi');
    assert.equal(readableLogBody('data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}\n\n'), 'Hi');
});

test('display summaries do not change stored error evidence', () => {
    assert.equal(logIssueSummary('content_filter: upstream policy notice'), 'blocked');
    assert.equal(logIssueSummary('upstream 429'), 'limited');
    assert.equal(logIssueSummary('context deadline exceeded'), 'timeout');
    assert.equal(logIssueSummary('unknown adapter failure'), 'failed');
});

test('content-filter evidence prefers exact backend state and treats zero bytes as unknown', () => {
    assert.equal(resolveContentFilterTextEvidence({ output_text_present: true }, undefined).state, 'present');
    assert.equal(resolveContentFilterTextEvidence({ output_text_present: false }, undefined).state, 'absent');
    assert.equal(resolveContentFilterTextEvidence(undefined, undefined).state, 'unknown');
    assert.equal(resolveContentFilterTextEvidence({ visible_content_bytes: 0 }, undefined).state, 'unknown');
    assert.equal(resolveContentFilterTextEvidence({ visible_content_bytes: 32 }, undefined).state, 'present');
    assert.equal(
        resolveContentFilterTextEvidence(
            { output_text_present: false, visible_content_bytes: 32 },
            undefined,
        ).state,
        'absent',
    );
});

test('strict evidence accepts only complete captured visible response text', () => {
    const bodies = [
        JSON.stringify({ choices:[{message:{content:'Hello'}}] }),
        JSON.stringify({ choices:[{message:{content:'Hello'}},{message:{content:'World'}}] }),
        JSON.stringify({ output:[{type:'message',content:[{type:'output_text',text:'Hello'}]}] }),
        JSON.stringify({ content:[{type:'thinking',thinking:'private'},{type:'text',text:'Hello'}] }),
        JSON.stringify({ content:['Hello','World'] }),
        JSON.stringify({ candidates:[{content:{parts:[{text:'Hello'}]}}] }),
    ];
    for (const body of bodies) assert.equal(strictLogTextEvidence(body, true).state, 'present');

    assert.equal(strictLogTextEvidence(JSON.stringify({ choices:[] }), true).state, 'absent');
    assert.equal(strictLogTextEvidence(JSON.stringify({ content:[{type:'thinking',thinking:'private'}] }), true).state, 'absent');
    assert.equal(strictLogTextEvidence(JSON.stringify({ choices:[{message:{tool_calls:[{id:'call'}]}}] }), true).state, 'absent');
    assert.equal(strictLogTextEvidence(JSON.stringify({ content:[{type:'image_url',image_url:{url:'image'}}] }), true).state, 'absent');
    assert.equal(strictLogTextEvidence(JSON.stringify({ error:{message:'failed'} }), true).state, 'unknown');
    assert.equal(strictLogTextEvidence(JSON.stringify({ choices:[{message:{content:'Hello'}}] }), false).state, 'unknown');
    assert.equal(strictLogTextEvidence('plain response', true).state, 'unknown');
});

test('strict evidence excludes unrecognized and error SSE frames but reads text deltas', () => {
    const stream = [
        'data: {"type":"response.output_text.delta","delta":"Hello "}',
        'data: {"type":"response.output_text.delta","delta":"World"}',
        'data: [DONE]',
    ].join('\n\n');
    assert.equal(strictLogTextEvidence(stream, true).state, 'present');
    assert.equal(strictLogTextEvidence('data: [DONE]', true).state, 'absent');
    assert.equal(strictLogTextEvidence('data: [DONE]', false).state, 'unknown');
    assert.equal(strictLogTextEvidence('data: {"error":{"message":"failed"}}', true).state, 'unknown');
    assert.equal(strictLogTextEvidence('data: {"type":"keepalive"}', true).state, 'unknown');
    assert.equal(strictLogTextEvidence([
        'data: {"choices":[{"delta":{"content":"Hello"}}]}',
        'data: [DONE]',
    ].join('\n\n'), true).state, 'present');
    assert.equal(strictLogTextEvidence([
        'data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}',
        'data: [DONE]',
    ].join('\n\n'), true).state, 'present');
});

test('strict SSE absence requires an observed protocol terminal', () => {
    for (const frame of [
        { choices: [{ index: 0, delta: { role: 'assistant' } }] },
        { type: 'response.created', response: {} },
        { type: 'content_block_delta', delta: { type: 'thinking_delta', thinking: 'private' } },
        { type: 'content_block_stop', index: 0 },
        { candidates: [{ index: 0, content: { parts: [{ thought: true, text: 'private' }] } }] },
    ]) {
        assert.equal(strictLogTextEvidence('data: ' + JSON.stringify(frame), true).state, 'unknown');
    }
    for (const frame of [
        { choices: [{ index: 0, delta: {}, finish_reason: 'content_filter' }] },
        { candidates: [{ index: 0, finishReason: 'SAFETY' }] },
        { type: 'response.incomplete', response: { output: [] } },
        { type: 'message_stop' },
    ]) {
        assert.equal(strictLogTextEvidence('data: ' + JSON.stringify(frame), true).state, 'absent');
    }
    assert.equal(strictLogTextEvidence([
        'data: {"choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}',
        'data: {"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}',
    ].join('\n\n'), true).state, 'unknown');
    assert.equal(strictLogTextEvidence([
        'data: {"type":"error","error":{"message":"interrupted"}}',
        'data: [DONE]',
    ].join('\n\n'), true).state, 'unknown');
});
