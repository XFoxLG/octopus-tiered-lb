import assert from 'node:assert/strict';
import test from 'node:test';
import { readableLogBody, logIssueSummary } from './body-preview.ts';

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
