import type { CacheConfig, CacheConfigRequest } from '@/api/endpoints/setting';

export interface CacheDraft {
    cacheType: '' | 'redis';
    address: string;
    username: string;
    password: string;
    database: string;
    tls: boolean;
    caFile: string;
    poolSize: string;
    dialTimeout: string;
    readTimeout: string;
}

export type CacheFieldErrors = Partial<Record<'address' | 'password' | 'database' | 'poolSize',
    'addressRequired' | 'passwordPlaceholder' | 'databaseInteger' | 'poolSizeInteger'>>;

export function createCacheDraft(config?: CacheConfig): CacheDraft {
    return {
        cacheType: config?.type ?? '',
        // Saved identity is display-only. These fields always describe a new connection.
        address: '',
        username: '',
        password: '',
        database: '0',
        tls: false,
        caFile: '',
        poolSize: config?.redis.pool_size ? String(config.redis.pool_size) : '',
        dialTimeout: config?.redis.dial_timeout ?? '',
        readTimeout: config?.redis.read_timeout ?? '',
    };
}

export function isCacheUrlInput(address: string): boolean {
    // This only gates manual controls; recognition and parsing belong to the server.
    return address.includes('://');
}

export function allowsCacheCertificate(draft: CacheDraft): boolean {
    if (!draft.address.trim()) return false;
    return isCacheUrlInput(draft.address)
        ? draft.address.trim().toLowerCase().startsWith('rediss://')
        : draft.tls;
}

export function validateCacheDraft(draft: CacheDraft, hasSavedConnection: boolean): CacheFieldErrors {
    const errors: CacheFieldErrors = {};
    if (draft.cacheType !== 'redis') return errors;

    const address = draft.address.trim();
    if (!address && !hasSavedConnection) errors.address = 'addressRequired';
    if (address.includes('CLICK_TO:REVEAL_PASSWORD')) errors.address = 'passwordPlaceholder';

    if (address && !isCacheUrlInput(address)) {
        const database = Number(draft.database.trim());
        if (!Number.isSafeInteger(database) || database < 0) errors.database = 'databaseInteger';
        if (draft.password.includes('CLICK_TO:REVEAL_PASSWORD')) errors.password = 'passwordPlaceholder';
    }

    const poolSize = Number(draft.poolSize.trim());
    if (!Number.isSafeInteger(poolSize) || poolSize < 0) errors.poolSize = 'poolSizeInteger';
    return errors;
}

export function buildCacheConfigRequest(draft: CacheDraft): CacheConfigRequest {
    if (draft.cacheType === '') return { type: '' };

    const request: CacheConfigRequest = {
        type: 'redis',
        tuning: {
            pool_size: Number(draft.poolSize.trim()),
            dial_timeout: draft.dialTimeout.trim(),
            read_timeout: draft.readTimeout.trim(),
        },
    };
    const address = draft.address.trim();
    if (!address) return request;

    const certificate = allowsCacheCertificate(draft) ? draft.caFile.trim() : '';
    request.redis = isCacheUrlInput(address)
        ? { addr: address, ...(certificate ? { ca_file: certificate } : {}) }
        : {
            addr: address,
            username: draft.username,
            password: draft.password,
            db: Number(draft.database.trim()),
            tls: draft.tls,
            ca_file: certificate,
        };
    return request;
}
