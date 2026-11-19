// Bound work before parsing and after formatting, including when reading an
// older server that still returns entire bodies. Raw downloads remain exact.
export const LOG_TEXT_PREVIEW_CHARS = 32_768;

export function boundedLogText(text: string): { text: string; truncated: boolean } {
    if (text.length <= LOG_TEXT_PREVIEW_CHARS) return { text, truncated: false };
    let end = LOG_TEXT_PREVIEW_CHARS;
    if (text.charCodeAt(end - 1) >= 0xd800 && text.charCodeAt(end - 1) <= 0xdbff) end--;
    return { text: text.slice(0, end), truncated: true };
}

export function formatLogText(text: string, complete: boolean): { text: string; truncated: boolean } {
    const preview = boundedLogText(text);
    if (preview.truncated || !complete) return preview;
    try {
        const formatted = JSON.stringify(JSON.parse(text), null, 2);
        return formatted.length > LOG_TEXT_PREVIEW_CHARS ? preview : { text: formatted, truncated: false };
    } catch {
        // SSE and plain text are valid captured payloads too.
        return preview;
    }
}
