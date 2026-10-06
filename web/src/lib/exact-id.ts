/** Never turn an already rounded JSON number into an apparently valid ID. */
export function exactId(value: string | number, exact?: string): string {
    const candidate = exact ?? value;
    if (typeof candidate === 'string' && /^\d+$/.test(candidate)) return candidate;
    if (typeof candidate === 'number' && Number.isSafeInteger(candidate) && candidate >= 0) return String(candidate);
    throw new Error('The server returned an unsafe record ID; refresh after updating the server.');
}

export function withExactId<T extends { id: string | number; id_str?: string }>(record: T): T {
    return { ...record, id: exactId(record.id, record.id_str) };
}

export function compareExactIds(a: string | number, b: string | number): number {
    const left = BigInt(exactId(a));
    const right = BigInt(exactId(b));
    return left < right ? -1 : left > right ? 1 : 0;
}
