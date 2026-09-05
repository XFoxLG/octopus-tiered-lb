
'use client';

import { useState, useEffect, useRef, useCallback } from 'react';
import { motion, AnimatePresence, useReducedMotion } from "motion/react"
import { RefreshCw, Search } from 'lucide-react';
import { useAuth } from '@/api/endpoints/user';
import { LoginForm } from '@/components/modules/login';
import { APIKeyDashboard } from '@/components/modules/apikey-dashboard';
import { ContentLoader } from '@/route/content-loader';
import { NavBar, useNavStore } from '@/components/modules/navbar';
import { useTranslations } from 'next-intl'
import Logo, { LOGO_DRAW_END_MS } from '@/components/modules/logo';
import { Toolbar } from '@/components/modules/toolbar';
import { DEFAULT_LOG_PAGE_SIZE, useLogRefresh } from '@/api/endpoints/log';
import { SettingKey, type Setting } from '@/api/endpoints/setting';
import { Button } from '@/components/ui/button';
import { toast } from '@/components/common/Toast';
import { useQueryClient, useQuery } from '@tanstack/react-query';
import { REFETCH_INTERVAL_CONFIG } from '@/api/constants';
import { CONTENT_MAP } from '@/route';
import { parseNavOrder, parseNavVisible } from '@/components/modules/navbar';
import { useSubTabStore, parseSubTabOrder, parseSubTabVisible, type ModuleId } from '@/components/modules/navbar/sub-tab-store';
import { initErrorReporting } from '@/lib/error-report';
import { AppErrorBoundary } from '@/components/common/AppErrorBoundary';
import { apiClient } from '@/api/client';
import { logger } from '@/lib/logger';
import { cn } from '@/lib/utils';
import { FirstRunSetup } from '@/components/modules/first-run-setup';
import { ProxyPoolDialog } from '@/components/modules/proxy-pool/ProxyPoolDialog';
import { useIsMobile } from '@/hooks/use-mobile';
import type { BootstrapStatusResponse } from '@/api/endpoints/bootstrap';
import type { NavItem } from '@/components/modules/navbar';
import { useLogModelSearchStore } from '@/components/modules/log/ui-store';
import { NotificationBell } from '@/components/modules/notification/NotificationBell';

function timeout(ms: number) {
    return new Promise<void>((resolve) => setTimeout(resolve, ms));
}

function getSettingsListQueryOptions() {
    return {
        queryKey: ['settings', 'list'] as const,
        queryFn: async () => apiClient.get<Setting[]>('/api/v1/setting/list'),
    };
}

function getNavOrderFromSettings(settings: Setting[] | undefined): NavItem[] {
    const navOrderValue = settings?.find((item) => item.key === SettingKey.NavOrder)?.value;
    return parseNavOrder(navOrderValue) as NavItem[];
}

function getNavVisibleFromSettings(settings: Setting[] | undefined): NavItem[] {
    const navVisibleValue = settings?.find((item) => item.key === SettingKey.NavVisible)?.value;
    return parseNavVisible(navVisibleValue);
}

const SUB_TAB_SETTING_KEYS: Record<ModuleId, { order: string; visible: string }> = {
    analytics: { order: SettingKey.AnalyticsTabOrder, visible: SettingKey.AnalyticsTabVisible },
    ops: { order: SettingKey.OpsTabOrder, visible: SettingKey.OpsTabVisible },
};

function hydrateSubTabsFromSettings(settings: Setting[] | undefined) {
    if (!settings) return;
    const store = useSubTabStore.getState();
    for (const modId of ['analytics', 'ops'] as ModuleId[]) {
        const keys = SUB_TAB_SETTING_KEYS[modId];
        const orderValue = settings.find((s) => s.key === keys.order)?.value;
        const visibleValue = settings.find((s) => s.key === keys.visible)?.value;
        const orderedTabs = parseSubTabOrder(modId, orderValue);
        const visibleTabs = parseSubTabVisible(modId, visibleValue, orderedTabs);
        store.setOrderedTabs(modId, orderedTabs);
        store.setVisibleTabs(modId, visibleTabs);
    }
}

function HeaderActions({ activeItem }: { activeItem: NavItem }) {
    const t = useTranslations('log');
    const { isRefreshing, refresh } = useLogRefresh(DEFAULT_LOG_PAGE_SIZE);

    const handleRefresh = useCallback(async () => {
        try {
            await refresh();
            toast.success(t('actions.refreshSuccess'));
        } catch {
            toast.error(t('actions.refreshFailed'));
        }
    }, [refresh, t]);

    if (activeItem !== 'log') return null;

    return (
        <Button
            variant="outline"
            size="sm"
            onClick={() => void handleRefresh()}
            disabled={isRefreshing}
            className="h-9 shrink-0 rounded-lg px-2.5 sm:h-10 sm:px-4"
        >
            <RefreshCw className={`header-action-icon h-4 w-4 sm:mr-2 ${isRefreshing ? 'animate-spin' : ''}`} />
            <span className="sr-only sm:not-sr-only">{t('actions.refresh')}</span>
        </Button>
    );
}

function HeaderModelSearch({ activeItem }: { activeItem: NavItem }) {
    const t = useTranslations('log.filter');
    const modelSearch = useLogModelSearchStore((s) => s.modelSearch);
    const setModelSearch = useLogModelSearchStore((s) => s.setModelSearch);
    const [input, setInput] = useState(modelSearch);
    const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => { setInput(modelSearch); }, [modelSearch]);

    if (activeItem !== 'log') return null;

    return (
        <div className="flex items-center gap-1.5 min-w-0">
            <Search className="header-action-icon size-3.5 shrink-0 text-muted-foreground" />
            <input
                value={input}
                onChange={(e) => {
                    const val = e.target.value;
                    setInput(val);
                    if (timer.current) clearTimeout(timer.current);
                    timer.current = setTimeout(() => setModelSearch(val.trim()), 400);
                }}
                placeholder={t('modelPlaceholder')}
                className="h-7 min-w-0 w-28 rounded-md border border-border/50 bg-background px-2 text-xs outline-none placeholder:text-muted-foreground/50 focus:border-primary/30 focus:ring-1 focus:ring-primary/20 sm:w-36 lg:w-48"
            />
        </div>
    );
}

export function AppContainer() {
    const { isAuthenticated, isAPIKeyAuth, isLoading: authLoading } = useAuth();
    const { activeItem, direction, visibleItems, setNavOrder, setVisibleItems, resetNavOrder } = useNavStore();
    const t = useTranslations('navbar');
    const queryClient = useQueryClient();
    const isMobile = useIsMobile();
    const reduceMotion = useReducedMotion();
    const lightweightMotion = reduceMotion || isMobile;

    const {
        data: bootstrapStatus,
        isLoading: bootstrapStatusLoading,
    } = useQuery({
        queryKey: ['bootstrap', 'status'],
        queryFn: async () => apiClient.get<BootstrapStatusResponse>('/api/v1/bootstrap/status', undefined, false),
        retry: false,
        staleTime: 0,
        refetchOnWindowFocus: false,
    });
    const { data: settings } = useQuery({
        ...getSettingsListQueryOptions(),
        enabled: isAuthenticated && !isAPIKeyAuth,
        // 全局设置变更极少，且 AppContainer 常驻不卸载；60s 轮询足够，
        // 并依赖 mutation 后 invalidate 即时刷新，无需 refetchOnMount: 'always'。
        refetchInterval: REFETCH_INTERVAL_CONFIG,
    });

    // Logo 动画完成状态
    const [logoAnimationComplete, setLogoAnimationComplete] = useState(false);
    const [bootstrapComplete, setBootstrapComplete] = useState(false);
    const bootstrapStartedRef = useRef(false);
    const warmedRoutesRef = useRef<Set<NavItem>>(new Set());

    // 挂载全局错误监听（JS 错误 / 未处理 Promise），捕获后上报后端错误日志。
    useEffect(() => {
        initErrorReporting();
    }, []);

    // 首屏最早的 server-rendered loader：一旦客户端开始渲染，就淡出移除
    useEffect(() => {
        const el = document.getElementById('initial-loader');
        if (!el) return;

        el.classList.add('octo-hide');
        const timer = setTimeout(() => el.remove(), 220);
        return () => clearTimeout(timer);
    }, []);

    useEffect(() => {
        const timer = setTimeout(() => setLogoAnimationComplete(true), LOGO_DRAW_END_MS);
        return () => clearTimeout(timer);
    }, []);

    useEffect(() => {
        if (!isAuthenticated || isAPIKeyAuth) {
            resetNavOrder();
            useSubTabStore.getState().resetAll();
            return;
        }

        if (!settings) return;
        setNavOrder(getNavOrderFromSettings(settings));
        setVisibleItems(getNavVisibleFromSettings(settings));
        hydrateSubTabsFromSettings(settings);
    }, [isAPIKeyAuth, isAuthenticated, resetNavOrder, setNavOrder, setVisibleItems, settings]);

    useEffect(() => {
        if (authLoading) return;
        if (!isAuthenticated) {
            bootstrapStartedRef.current = false;
            setBootstrapComplete(true);
            return;
        }

        if (bootstrapStartedRef.current) return;
        bootstrapStartedRef.current = true;
        setBootstrapComplete(false);

        let cancelled = false;

        (async () => {
            try {
                const prefetches: Array<Promise<unknown>> = [];

                // API Key 认证模式：预取 dashboard stats
                if (isAPIKeyAuth) {
                    prefetches.push(
                        queryClient.prefetchQuery({
                            queryKey: ['apikey', 'dashboard', 'stats'],
                            queryFn: async () => apiClient.get('/api/v1/apikey/stats'),
                        })
                    );
                } else {
                    const settingsPromise = queryClient.fetchQuery(getSettingsListQueryOptions());
                    prefetches.push(
                        settingsPromise.then((nextSettings) => {
                            if (cancelled) {
                                return;
                            }
                            useNavStore.getState().setNavOrder(getNavOrderFromSettings(nextSettings));
                            useNavStore.getState().setVisibleItems(getNavVisibleFromSettings(nextSettings));
                            hydrateSubTabsFromSettings(nextSettings);
                        })
                    );

                    // 普通用户认证模式：预取对应页面数据
                    const component = CONTENT_MAP[activeItem];
                    if (component?.preload) {
                        prefetches.push(component.preload());
                    }

                    switch (activeItem) {
                        case 'home': {
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['stats', 'total'],
                                    queryFn: async () => apiClient.get('/api/v1/stats/total'),
                                })
                            );
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['stats', 'daily'],
                                    queryFn: async () => apiClient.get('/api/v1/stats/daily'),
                                })
                            );
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['stats', 'hourly'],
                                    queryFn: async () => apiClient.get('/api/v1/stats/hourly'),
                                })
                            );
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['stats', 'channel'],
                                    queryFn: async () => apiClient.get('/api/v1/stats/channel'),
                                })
                            );
                            break;
                        }
                        case 'channel': {
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['channels', 'list'],
                                    queryFn: async () => apiClient.get('/api/v1/channel/list'),
                                })
                            );
                            break;
                        }
                        case 'group': {
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['groups', 'list'],
                                    queryFn: async () => apiClient.get('/api/v1/group/list'),
                                })
                            );
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['channels', 'list'],
                                    queryFn: async () => apiClient.get('/api/v1/channel/list'),
                                })
                            );
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['apikeys', 'list'],
                                    queryFn: async () => apiClient.get('/api/v1/apikey/list'),
                                })
                            );
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['stats', 'apikey'],
                                    queryFn: async () => apiClient.get('/api/v1/stats/apikey'),
                                })
                            );
                            break;
                        }
                        case 'model': {
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['models', 'market'],
                                    queryFn: async () => apiClient.get('/api/v1/model/market'),
                                })
                            );
                            break;
                        }
                        case 'setting': {
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['apikeys', 'list'],
                                    queryFn: async () => apiClient.get('/api/v1/apikey/list'),
                                })
                            );
                            break;
                        }
                        case 'ops': {
                            prefetches.push(
                                queryClient.prefetchQuery({
                                    queryKey: ['ops', 'health'],
                                    queryFn: async () => apiClient.get('/api/v1/ops/health'),
                                })
                            );
                            break;
                        }
                        default:
                            break;
                    }
                }

                await Promise.race([
                    Promise.allSettled(prefetches),
                    timeout(5000),
                ]);
            } catch (e) {
                logger.warn('bootstrap prefetch failed:', e);
            } finally {
                if (!cancelled) setBootstrapComplete(true);
            }
        })();

        return () => {
            cancelled = true;
        };
        // dependencies intentionally exclude activeItem; bootstrap should only run when auth state changes
    }, [authLoading, isAPIKeyAuth, isAuthenticated]);

    useEffect(() => {
        if (!bootstrapComplete || !isAuthenticated || isAPIKeyAuth || visibleItems.length === 0) {
            return;
        }

        const pendingRoutes = visibleItems.filter((routeId) => routeId !== activeItem && !warmedRoutesRef.current.has(routeId));
        if (pendingRoutes.length === 0) {
            return;
        }

        const warm = () => {
            pendingRoutes.forEach((routeId, index) => {
                window.setTimeout(() => {
                    CONTENT_MAP[routeId]?.preload?.();
                    warmedRoutesRef.current.add(routeId);
                }, index * 120);
            });
        };

        const windowWithIdle = window as Window & {
            requestIdleCallback?: (callback: IdleRequestCallback) => number;
            cancelIdleCallback?: (handle: number) => void;
        };

        if (typeof windowWithIdle.requestIdleCallback === 'function') {
            const idleId = windowWithIdle.requestIdleCallback(() => warm());
            return () => windowWithIdle.cancelIdleCallback?.(idleId);
        }

        const timer = globalThis.setTimeout(warm, 200);
        return () => globalThis.clearTimeout(timer);
    }, [activeItem, bootstrapComplete, isAPIKeyAuth, isAuthenticated, visibleItems]);

    const shouldShowFirstRunSetup =
        !isAuthenticated &&
        !bootstrapStatusLoading &&
        bootstrapStatus?.initialized === false;

    // 加载状态
    const isLoading =
        authLoading ||
        bootstrapStatusLoading ||
        !logoAnimationComplete ||
        (isAuthenticated && !bootstrapComplete);

    // 加载页面
    if (isLoading) {
        return (
            <div className="min-h-screen flex flex-col items-center justify-center gap-4 bg-background">
                <Logo size={120} animate />
                <motion.span
                    initial={{ opacity: 0 }}
                    animate={{ opacity: [0.4, 0.7, 0.4] }}
                    transition={{ duration: 2, repeat: Infinity, ease: "easeInOut" }}
                    className="text-sm text-muted-foreground"
                >
                    Loading...
                </motion.span>
            </div>
        );
    }

    if (shouldShowFirstRunSetup) {
        return (
            <AnimatePresence mode="wait">
                <FirstRunSetup />
            </AnimatePresence>
        );
    }

    // API Key 认证模式 - 显示 API Key Dashboard
    if (isAPIKeyAuth) {
        return (
            <AnimatePresence mode="wait">
                <APIKeyDashboard key="apikey-dashboard" />
            </AnimatePresence>
        );
    }

    // 登录页面
    if (!isAuthenticated) {
        return (
            <AnimatePresence mode="wait">
                <LoginForm key="login" />
            </AnimatePresence>
        );
    }

    // 主界面
    return (
        <motion.div
            key="main-app"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ duration: lightweightMotion ? 0.2 : 0.6, ease: [0.16, 1, 0.3, 1] }}
            className="relative mx-auto flex h-dvh max-w-[92rem] flex-col overflow-visible px-3 pt-3 pb-3 md:grid md:grid-cols-[auto_minmax(0,1fr)] md:gap-7 md:px-6 md:py-6"
        >
            <NavBar />
            <main className="relative z-10 flex min-h-0 w-full min-w-0 flex-1 flex-col gap-4 md:gap-5">
                <header className="relative z-20 flex flex-none items-center gap-2 overflow-visible rounded-xl border border-border bg-card px-3 py-2.5 md:gap-3 md:px-5 md:py-3 lg:gap-5 [&_.header-action-icon]:max-[380px]:hidden [&_.header-action-icon]:max-[380px]:size-0">
                    <div className="flex min-w-0 flex-1 items-center gap-2.5 md:gap-3">
                        <div className="hidden size-9 shrink-0 place-items-center overflow-hidden rounded-lg bg-card min-[420px]:grid md:size-10">
                            <Logo size={28} />
                        </div>
                        <div className="min-w-0 flex-1 overflow-hidden">
                            <AnimatePresence mode="wait" custom={direction}>
                                <motion.div
                                    key={activeItem}
                                    custom={direction}
                                    variants={lightweightMotion ? {
                                        initial: { opacity: 0 },
                                        animate: { opacity: 1 },
                                        exit: { opacity: 0 },
                                    } : {
                                        initial: (direction: number) => ({
                                            y: 32 * direction,
                                            opacity: 0
                                        }),
                                        animate: {
                                            y: 0,
                                            opacity: 1
                                        },
                                        exit: (direction: number) => ({
                                            y: -32 * direction,
                                            opacity: 0
                                        })
                                    }}
                                    initial="initial"
                                    animate="animate"
                                    exit="exit"
                                    transition={{ duration: lightweightMotion ? 0.18 : 0.4, ease: [0.16, 1, 0.3, 1] }}
                                    className="flex min-w-0 flex-col"
                                >
                                    <div className="flex min-w-0 items-center gap-2 sm:gap-3">
                                        <span className="min-w-0 truncate text-xl font-bold leading-tight tracking-[-0.04em] text-foreground sm:text-2xl md:text-3xl">
                                            {t(activeItem)}
                                        </span>
                                        <HeaderActions activeItem={activeItem} />
                                    </div>
                                </motion.div>
                            </AnimatePresence>
                        </div>
                    </div>
                    <div className="ml-auto flex min-w-0 shrink items-center gap-1.5 justify-end sm:gap-2 lg:gap-3">
                        <HeaderModelSearch activeItem={activeItem} />
                        <div className={cn(
                            "flex min-w-0 shrink items-center gap-1.5 sm:gap-2 lg:gap-3",
                            "[&_.header-action-icon]:max-[380px]:hidden [&_.header-action-icon]:max-[380px]:size-0",
                            "[&_.header-action-label]:max-[520px]:sr-only"
                        )}>
                            {activeItem !== 'notification' && <NotificationBell />}
                            <Toolbar />
                        </div>
                    </div>
                </header>
                <div className="h-full min-h-0 flex-1 pb-[calc(5.5rem+env(safe-area-inset-bottom,0px))] md:pb-[calc(1rem+env(safe-area-inset-bottom,0px))]">
                    <AppErrorBoundary>
                        <ContentLoader activeRoute={activeItem} />
                    </AppErrorBoundary>
                </div>
            </main>
            <ProxyPoolDialog />
        </motion.div>
    );
}
