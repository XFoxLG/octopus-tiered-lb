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

export type StrictLogTextEvidenceState = 'present' | 'absent' | 'unknown';

export interface StrictLogTextEvidence {
    state: StrictLogTextEvidenceState;
}

interface StrictEvidenceInput {
    output_text_present?: boolean;
    visible_content_bytes?: number;
}

function strictTextParts(value: unknown): string {
    if (typeof value === 'string') return value;
    if (!Array.isArray(value)) return '';
    return value.map(part => {
        if (typeof part === 'string') return part;
        const item = record(part);
        if (!item || item.thought === true) return '';
        const itemType = item.type === undefined ? 'text' : String(item.type);
        if (!['text', 'output_text'].includes(itemType)) return '';
        return typeof item.text === 'string' ? item.text : '';
    }).filter(Boolean).join('\n');
}

function jsonEnvelopeText(body: RecordValue): string {
    if (typeof body.output_text === 'string' && body.output_text) return body.output_text;
    const choices = Array.isArray(body.choices) ? body.choices : [];
    const choiceText = choices.map(value => {
        const choice = record(value);
        return strictTextParts(record(choice?.message)?.content ?? record(choice?.delta)?.content);
    }).filter(Boolean).join('\n\n');
    if (choiceText) return choiceText;
    const output = Array.isArray(body.output) ? body.output : [];
    const outputText = output.map(value => strictTextParts(record(value)?.content)).filter(Boolean).join('\n\n');
    if (outputText) return outputText;
    const candidates = Array.isArray(body.candidates) ? body.candidates : [];
    const candidateText = candidates.map(value => strictTextParts(record(record(value)?.content)?.parts)).filter(Boolean).join('\n\n');
    if (candidateText) return candidateText;
    return strictTextParts(body.content);
}

function isErrorResponse(value: unknown): boolean {
    return Boolean(record(value)?.error);
}

function jsonStrictTextEvidence(raw: string): StrictLogTextEvidence {
    let parsed: unknown;
    try { parsed = JSON.parse(raw); } catch { return { state: 'unknown' }; }
    if (isErrorResponse(parsed)) return { state: 'unknown' };
    if (typeof parsed === 'string') return parsed.trim() ? { state: 'present' } : { state: 'unknown' };
    const body = record(parsed);
    if (!body) return { state: 'unknown' };
    const isKnownResponseEnvelope = (
        Array.isArray(body.choices) ||
        Array.isArray(body.output) ||
        Array.isArray(body.candidates) ||
        body.content !== undefined ||
        body.output_text !== undefined
    );
    if (!isKnownResponseEnvelope) return { state: 'unknown' };
    return jsonEnvelopeText(body).trim() ? { state: 'present' } : { state: 'absent' };
}

function streamStrictTextEvidence(raw: string): StrictLogTextEvidence {
    let sawTerminal = false;
    let sawError = false;
    const observedChoices = new Set<number>();
    const finishedChoices = new Set<number>();
    const textPartsByLine = raw.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => {
        const payload = line.slice(5).trim();
        if (payload === '[DONE]') {
            sawTerminal = true;
            return '';
        }
        let value: unknown;
        try { value = JSON.parse(payload); } catch { return ''; }
        const event = record(value);
        if (!event) return '';
        const eventType = typeof event.type === 'string' ? event.type : '';
        if (eventType === 'error' || isErrorResponse(event)) {
            sawError = true;
            return '';
        }
        if (['response.completed', 'response.incomplete', 'message_stop'].includes(eventType)) {
            sawTerminal = true;
        }
        if (eventType === 'response.failed' || eventType === 'response.error') sawError = true;
        const isRecognizedEvent = (
            eventType === 'response.output_text.delta' ||
            eventType.startsWith('response.') ||
            eventType === 'content_block_delta' ||
            eventType.startsWith('content_block_') ||
            eventType === 'message_stop' ||
            Array.isArray(event.choices) ||
            Array.isArray(event.candidates)
        );
        if (!isRecognizedEvent) return '';
        if (event.type === 'response.output_text.delta' && typeof event.delta === 'string') {
            return event.delta;
        }
        if (event.type === 'content_block_delta') {
            const delta = record(event.delta);
            return delta?.type === 'text_delta' && typeof delta.text === 'string' ? delta.text : '';
        }
        if (Array.isArray(event.choices)) {
            return event.choices.map((value, position) => {
                const choice = record(value);
                if (!choice) return '';
                const index = typeof choice.index === 'number' ? choice.index : position;
                observedChoices.add(index);
                if (typeof choice.finish_reason === 'string' && choice.finish_reason.trim()) finishedChoices.add(index);
                return strictTextParts(record(choice.delta)?.content);
            }).join('');
        }
        if (Array.isArray(event.candidates)) {
            return event.candidates.map((value, position) => {
                const candidate = record(value);
                if (!candidate) return '';
                const index = typeof candidate.index === 'number' ? candidate.index : position;
                observedChoices.add(index);
                if (typeof candidate.finishReason === 'string' && candidate.finishReason.trim()) finishedChoices.add(index);
                return strictTextParts(record(candidate.content)?.parts);
            }).join('');
        }
        return '';
    }).join('');
    if (textPartsByLine.trim()) return { state: 'present' };
    const allObservedChoicesFinished = observedChoices.size > 0 &&
        [...observedChoices].every(index => finishedChoices.has(index));
    if (sawError || (!sawTerminal && !allObservedChoicesFinished)) return { state: 'unknown' };
    return { state: 'absent' };
}

/**
 * Classify captured response text without treating display fallbacks as proof.
 * Plain strings, `text`, `output_text`, and stream `delta` content are the only
 * visible body candidates; tool, reasoning, media, control, and error frames
 * never become evidence.
 */
export function strictLogTextEvidence(raw: string | undefined, captureComplete: boolean): StrictLogTextEvidence {
    if (!raw || !captureComplete) return { state: 'unknown' };
    const bodyEvidence = jsonStrictTextEvidence(raw);
    if (bodyEvidence.state !== 'unknown') return bodyEvidence;
    return streamStrictTextEvidence(raw);
}

/**
 * Resolve content-filter body evidence. A backend boolean wins. For legacy
 * records, a positive gateway byte count proves text existed, but zero never
 * proves absence; otherwise the selected captured response is parsed strictly.
 */
export function resolveContentFilterTextEvidence(
    diagnostics: StrictEvidenceInput | undefined,
    capturedResponse: { text?: string; complete: boolean } | undefined,
): StrictLogTextEvidence {
    if (diagnostics?.output_text_present === true) return { state: 'present' };
    if (diagnostics?.output_text_present === false) return { state: 'absent' };
    if (typeof diagnostics?.visible_content_bytes === 'number' && Number.isFinite(diagnostics.visible_content_bytes) && diagnostics.visible_content_bytes > 0) {
        return { state: 'present' };
    }
    return strictLogTextEvidence(capturedResponse?.text, capturedResponse?.complete === true);
}
