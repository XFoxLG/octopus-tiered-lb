export function mergeLogPages<T extends { id: string | number }>(pages: T[][], incoming: T): T[][] {
    if (pages.some(page => page.some(entry => entry.id === incoming.id))) {
        return pages.map(page => page.map(entry => entry.id === incoming.id ? incoming : entry));
    }
    return [[incoming, ...(pages[0] ?? [])].slice(0, 500), ...pages.slice(1)];
}

export function mergeLiveRequest<T extends { trace_id: string; state: string }>(entries: T[], incoming: T): T[] {
    const remaining = entries.filter(entry => entry.trace_id !== incoming.trace_id);
    return incoming.state === 'recorded' ? remaining : [incoming, ...remaining].slice(0, 256);
}
