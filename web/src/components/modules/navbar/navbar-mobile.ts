export const MOBILE_PRIMARY_NAV_COUNT = 4

export function splitMobileNavItems<T>(items: readonly T[]) {
    return {
        primary: items.slice(0, MOBILE_PRIMARY_NAV_COUNT),
        more: items.slice(MOBILE_PRIMARY_NAV_COUNT),
    }
}
