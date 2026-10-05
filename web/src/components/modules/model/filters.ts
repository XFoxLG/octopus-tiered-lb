import { canonicalEndpoint } from '../apikey/endpoint-grouping.ts';

export const MODEL_CAPABILITY_OPTIONS = [
  'all', 'chat', 'embeddings', 'rerank', 'moderations', 'image_generation',
  'audio_speech', 'audio_transcription', 'video_generation', 'music_generation', 'search',
] as const;

export type ModelCapabilityFilter = typeof MODEL_CAPABILITY_OPTIONS[number];

interface FilterItem {
  name: string;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
}

interface Capability {
  name: string;
  endpoints: string[];
  conversation: boolean;
}

export function filterMarketItems<T extends FilterItem>({
  items, capabilities, searchTerm, capability, provider, dedupe, pricing,
  inferProvider, normalizeName,
}: {
  items: T[];
  capabilities: Capability[];
  searchTerm: string;
  capability: ModelCapabilityFilter;
  provider: string;
  dedupe: boolean;
  pricing: 'all' | 'priced' | 'free';
  inferProvider: (name: string) => string;
  normalizeName: (name: string) => string;
}): T[] {
  const term = searchTerm.trim().toLowerCase();
  const capabilityByName = new Map(capabilities.map((entry) => [entry.name, entry]));
  const seen = new Set<string>();

  return items.filter((item) => {
    if (term && !item.name.toLowerCase().includes(term)) return false;
    const priced = [item.input, item.output, item.cache_read, item.cache_write].some((value) => value > 0);
    if (pricing === 'priced' && !priced || pricing === 'free' && priced) return false;
    if (provider && inferProvider(item.name) !== provider) return false;
    if (capability !== 'all') {
      const entry = capabilityByName.get(item.name);
      if (!entry) return false;
      const matches = capability === 'chat'
        ? entry.conversation || entry.endpoints.length === 0 || entry.endpoints.some((endpoint) => canonicalEndpoint(endpoint) === 'chat')
        : entry.endpoints.includes(capability);
      if (!matches) return false;
    }
    if (dedupe) {
      const canonical = normalizeName(item.name);
      if (seen.has(canonical)) return false;
      seen.add(canonical);
    }
    return true;
  });
}
