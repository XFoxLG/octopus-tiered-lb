// Read only user-visible text. Unknown shapes remain available as raw content.
type RecordValue = Record<string, unknown>;
export function logIssueSummary(error: string): 'blocked' | 'limited' | 'empty' | 'timeout' | 'failed' {
    if (/content_filter|prompt_blocked|upstream policy notice/i.test(error)) return 'blocked';
    if (/rate.limit|too many requests|\b429\b/i.test(error)) return 'limited';
    if (/empty.response|empty.output|no parseable text/i.test(error)) return 'empty';
    if (/timeout|timed out|deadline exceeded/i.test(error)) return 'timeout';
    return 'failed';
}
function record(value: unknown): RecordValue | undefined {
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : undefined;
}
function textParts(value: unknown): string {
    if (typeof value === 'string') return value;
    if (!Array.isArray(value)) return '';
    return value.map(part => {
        const item = record(part);
        if (!item || (item.type && !['text', 'input_text', 'output_text'].includes(String(item.type)))) return '';
        return typeof item.text === 'string' ? item.text : '';
    }).filter(Boolean).join('\n');
}

export function readableLogBody(raw: string): string {
    if (/^data:/m.test(raw)) {
        const text = raw.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => {
            let value: unknown;
            try { value = JSON.parse(line.slice(5).trim()); } catch { return ''; }
            const event = record(value);
            if (!event) return '';
            if (event.type === 'response.output_text.delta' && typeof event.delta === 'string') return event.delta;
            if (event.type === 'content_block_delta') return textParts(record(event.delta)?.text);
            if (Array.isArray(event.choices)) return event.choices.map(choice => textParts(record(record(choice)?.delta)?.content)).join('');
            if (Array.isArray(event.candidates)) return event.candidates.map(candidate => textParts(record(record(candidate)?.content)?.parts)).join('');
            return '';
        }).join('');
        return text || raw;
    }
    let parsed: unknown;
    try { parsed = JSON.parse(raw); } catch { return raw; }
    const body = record(parsed);
    if (!body) return typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2);
    const messages = Array.isArray(body.messages) ? body.messages : Array.isArray(body.contents) ? body.contents : null;
    if (messages) {
        const lines = messages.map(value => {
            const message = record(value);
            if (!message) return '';
            const text = textParts(message.content ?? message.parts);
            return text ? `${String(message.role || 'user')}\n${text}` : '';
        }).filter(Boolean);
        // Do not hide tool-only or media-only messages behind an empty preview.
        if (lines.length === messages.length && lines.length) return lines.join('\n\n');
    }
    if (typeof body.input === 'string') return body.input;
    if (typeof body.output_text === 'string' && body.output_text) return body.output_text;
    const choices = Array.isArray(body.choices) ? body.choices : [];
    const choiceText = choices.map(value => {
        const choice = record(value);
        return textParts(record(choice?.message)?.content);
    }).filter(Boolean).join('\n\n');
    if (choiceText) return choiceText;
    const output = Array.isArray(body.output) ? body.output : [];
    const outputText = output.map(value => textParts(record(value)?.content)).filter(Boolean).join('\n\n');
    if (outputText) return outputText;
    const contentText = textParts(body.content);
    if (contentText) return contentText;
    const candidates = Array.isArray(body.candidates) ? body.candidates : [];
    const candidateText = candidates.map(value => textParts(record(record(value)?.content)?.parts)).filter(Boolean).join('\n\n');
    return candidateText || JSON.stringify(parsed, null, 2);
}
