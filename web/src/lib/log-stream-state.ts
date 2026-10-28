export function mergeLogPages<T extends { id: string | number }>(pages: T[][], incoming: T): T[][] {
    if (pages.some(page => page.some(entry => entry.id === incoming.id))) {
        return pages.map(page => page.map(entry => entry.id === incoming.id ? incoming : entry));
    }
    return [[incoming, ...(pages[0] ?? [])].slice(0, 500), ...pages.slice(1)];
}

export function mergeLiveRequest<T extends { trace_id: string; state: string; instance_id?: string; sequence?: number }>(entries: T[], incoming: T): T[] {
    const previous = entries.find(entry => entry.trace_id === incoming.trace_id && entry.instance_id === incoming.instance_id);
    if (previous && (incoming.sequence ?? 0) < (previous.sequence ?? 0)) return entries;
    const terminal = (state: string) => ['completed', 'recorded', 'unavailable'].includes(state);
    if (previous && terminal(previous.state) && !terminal(incoming.state)) return entries;
    const remaining = entries.filter(entry => entry.trace_id !== incoming.trace_id || entry.instance_id !== incoming.instance_id);
    // Keep bounded tombstones for out-of-order SSE events; the view only shows active entries.
    return [incoming, ...remaining].slice(0, 256);
}

export function visibleLiveRequests<T>(entries: T[], connected: boolean): T[] {
    return connected ? entries : [];
}

// Refresh the server's newest window without refetching loaded history. Entries
// that no longer match a filter inside that window must disappear as well.
export function mergeLogHead<T extends { id: string | number; time: number }>(pages: T[][], incoming: T[], previousIds = new Set(pages.flat().map(entry => entry.id))): T[][] {
    if (incoming.length === 0) return pages.map(page => page.filter(entry => !previousIds.has(entry.id)));
    const cutoff = Math.min(...incoming.map(entry => entry.time));
    const ids = new Set(incoming.map(entry => entry.id));
    const history = pages.map(page => page.filter(entry => !ids.has(entry.id) && (!previousIds.has(entry.id) || entry.time <= cutoff)));
    return [[...incoming, ...(history[0] ?? [])].slice(0, 500), ...history.slice(1)];
}
