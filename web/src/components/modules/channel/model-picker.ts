export function normalizeFetchedModels(data: unknown): string[] {
    if (!Array.isArray(data)) return [];

    const models = data.map((item) => {
        if (typeof item === 'string') return item.trim();
        if (!item || typeof item !== 'object') return '';

        const candidate =
            ('id' in item && typeof item.id === 'string' && item.id) ||
            ('name' in item && typeof item.name === 'string' && item.name) ||
            ('display_name' in item && typeof item.display_name === 'string' && item.display_name) ||
            ('displayName' in item && typeof item.displayName === 'string' && item.displayName) ||
            '';

        return candidate.trim();
    }).filter(Boolean);

    return Array.from(new Set(models)).sort();
}

export function toggleModelSelection(selected: string[], models: string[]): string[] {
    const targets = normalizeFetchedModels(models);
    if (targets.length === 0) return selected;

    const current = new Set(selected);
    if (targets.every((model) => current.has(model))) {
        const removed = new Set(targets);
        return selected.filter((model) => !removed.has(model));
    }

    return Array.from(new Set([...selected, ...targets]));
}
