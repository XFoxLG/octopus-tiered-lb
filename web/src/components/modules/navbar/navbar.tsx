"use client"

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { motion, useReducedMotion } from "motion/react"
import { Ellipsis, X } from "lucide-react"
import { cn } from "@/lib/utils"
import { useNavStore, type NavItem } from "@/components/modules/navbar"
import { ROUTES } from "@/route/config"
import { usePreload } from "@/route/use-preload"
import { ENTRANCE_VARIANTS } from "@/lib/animations/fluid-transitions"
import { useTranslations } from "next-intl"
import { useIsMobile } from "@/hooks/use-mobile"
import { splitMobileNavItems } from "./navbar-mobile"

type LabelAnchor = {
    x: number
    y: number
}

const MORE_ITEM_ID = "more-navigation"
const MORE_SHEET_ID = "mobile-nav-more-sheet"
const MORE_SHEET_TITLE_ID = "mobile-nav-more-title"

export function NavBar() {
    const { activeItem, orderedItems, visibleItems, setActiveItem } = useNavStore()
    const { preload } = usePreload()
    const t = useTranslations('navbar')
    const isMobile = useIsMobile()
    const reduceMotion = useReducedMotion()
    const lightweightMotion = isMobile || reduceMotion
    const [pressedItem, setPressedItem] = useState<NavItem | typeof MORE_ITEM_ID | null>(null)
    const [moreOpen, setMoreOpen] = useState(false)
    const [labelAnchors, setLabelAnchors] = useState<Record<string, LabelAnchor>>({})
    const navRef = useRef<HTMLElement | null>(null)
    const buttonRefs = useRef(new Map<string, HTMLButtonElement>())
    const moreButtonRef = useRef<HTMLButtonElement | null>(null)
    const moreDialogRef = useRef<HTMLDialogElement | null>(null)
    const visibleRouteSet = useMemo(() => new Set(visibleItems), [visibleItems])
    const routeById = useMemo(
        () => new Map(ROUTES.map((route) => [route.id as NavItem, route])),
        []
    )
    const orderedRoutes = useMemo(
        () =>
            orderedItems
                .filter((item) => visibleRouteSet.has(item))
                .map((item) => routeById.get(item))
                .filter((route) => route !== undefined),
        [orderedItems, routeById, visibleRouteSet]
    )
    const { primary: mobilePrimaryRoutes, more: mobileMoreRoutes } = useMemo(
        () => splitMobileNavItems(orderedRoutes),
        [orderedRoutes]
    )
    const mobileMoreRouteSet = useMemo(
        () => new Set(mobileMoreRoutes.map((route) => route.id)),
        [mobileMoreRoutes]
    )
    const displayedRoutes = isMobile ? mobilePrimaryRoutes : orderedRoutes
    const isMoreActive = isMobile && mobileMoreRouteSet.has(activeItem)

    // Mobile labels must not live inside the overflow-x-auto nav, or the vertical
    // axis is also clipped (CSS: non-visible overflow on one axis forces the other).
    // Render them as fixed layers anchored to each button's viewport rect instead.
    const visibleLabelEntries = useMemo(() => {
        if (!isMobile) return []

        const entries = new Map<string, NavItem | typeof MORE_ITEM_ID>()
        const activeSurfaceId = mobileMoreRouteSet.has(activeItem) ? MORE_ITEM_ID : activeItem
        entries.set(activeSurfaceId, activeItem)

        if (pressedItem) {
            const pressedSurfaceId = pressedItem === MORE_ITEM_ID || mobileMoreRouteSet.has(pressedItem)
                ? MORE_ITEM_ID
                : pressedItem
            entries.set(pressedSurfaceId, pressedItem)
        }

        return [...entries].map(([surfaceId, item]) => ({ surfaceId, item }))
    }, [activeItem, isMobile, mobileMoreRouteSet, pressedItem])

    const updateLabelAnchors = useCallback(() => {
        if (!isMobile || visibleLabelEntries.length === 0) {
            setLabelAnchors((prev) => (Object.keys(prev).length === 0 ? prev : {}))
            return
        }

        const next: Record<string, LabelAnchor> = {}
        for (const { surfaceId } of visibleLabelEntries) {
            const el = buttonRefs.current.get(surfaceId)
            if (!el) continue
            const rect = el.getBoundingClientRect()
            next[surfaceId] = {
                x: rect.left + rect.width / 2,
                y: rect.top,
            }
        }
        setLabelAnchors(next)
    }, [isMobile, visibleLabelEntries])

    useLayoutEffect(() => {
        updateLabelAnchors()
    }, [displayedRoutes, mobileMoreRoutes, updateLabelAnchors])

    useEffect(() => {
        if (!isMobile) return

        const onScrollOrResize = () => updateLabelAnchors()
        const nav = navRef.current

        window.addEventListener("resize", onScrollOrResize)
        // capture: catch scroll from the nav strip and any page scrollers
        window.addEventListener("scroll", onScrollOrResize, true)
        nav?.addEventListener("scroll", onScrollOrResize)

        return () => {
            window.removeEventListener("resize", onScrollOrResize)
            window.removeEventListener("scroll", onScrollOrResize, true)
            nav?.removeEventListener("scroll", onScrollOrResize)
        }
    }, [isMobile, updateLabelAnchors])

    useEffect(() => {
        if (!moreOpen || !isMobile) return

        const dialog = moreDialogRef.current
        if (!dialog) return

        if (!dialog.open) {
            dialog.showModal()
        }

        const previousBodyOverflow = document.body.style.overflow
        document.body.style.overflow = "hidden"
        const frame = window.requestAnimationFrame(() => {
            const activeMoreRoute = mobileMoreRouteSet.has(activeItem)
                ? buttonRefs.current.get(`${MORE_SHEET_ID}-${activeItem}`)
                : undefined
            const firstMoreRoute = mobileMoreRoutes[0]
                ? buttonRefs.current.get(`${MORE_SHEET_ID}-${mobileMoreRoutes[0].id}`)
                : undefined
            ;(activeMoreRoute ?? firstMoreRoute ?? moreButtonRef.current)?.focus()
        })

        return () => {
            window.cancelAnimationFrame(frame)
            document.body.style.overflow = previousBodyOverflow
            if (dialog.open) dialog.close()
        }
    }, [activeItem, isMobile, mobileMoreRouteSet, mobileMoreRoutes, moreOpen])

    useEffect(() => {
        if (isMobile) return
        setMoreOpen(false)
        moreDialogRef.current?.close()
    }, [isMobile])

    const setButtonRef = useCallback((id: string, node: HTMLButtonElement | null) => {
        if (node) {
            buttonRefs.current.set(id, node)
        } else {
            buttonRefs.current.delete(id)
        }
    }, [])

    const closeMoreSheet = useCallback(() => {
        if (moreDialogRef.current?.open) {
            moreDialogRef.current.close()
        }
        setMoreOpen(false)
        moreButtonRef.current?.focus()
    }, [])

    const selectMobileRoute = useCallback((item: NavItem) => {
        setActiveItem(item)
        closeMoreSheet()
    }, [closeMoreSheet, setActiveItem])

    const setMoreButtonRef = useCallback((node: HTMLButtonElement | null) => {
        moreButtonRef.current = node
        setButtonRef(MORE_ITEM_ID, node)
    }, [setButtonRef])

    return (
        <div className="relative z-50 md:min-h-full">
            <motion.nav
                ref={navRef}
                aria-label={t('ariaLabel')}
                className={cn(
                    "fixed left-1/2 bottom-[calc(1.25rem+env(safe-area-inset-bottom,0px))] flex max-w-[calc(100vw-1.5rem)] -translate-x-1/2 items-center gap-1 overflow-x-auto p-2.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
                    "max-w-[calc(100vw-1.5rem-env(safe-area-inset-left,0px)-env(safe-area-inset-right,0px))]",
                    "rounded-2xl border border-border bg-sidebar text-sidebar-foreground",
                    "md:sticky md:top-6 md:left-auto md:bottom-auto md:h-[calc(100dvh-3rem)] md:max-w-none md:translate-x-0 md:flex-col md:gap-2 md:overflow-visible md:p-3"
                )}
                variants={lightweightMotion ? undefined : ENTRANCE_VARIANTS.navbar}
                initial={lightweightMotion ? false : "initial"}
                animate={lightweightMotion ? undefined : "animate"}
            >
                <div className="pointer-events-none absolute inset-y-0 left-0 z-30 w-5 rounded-l-2xl bg-gradient-to-r from-sidebar/50 via-sidebar/20 to-transparent md:hidden" />
                <div className="pointer-events-none absolute inset-y-0 right-0 z-30 w-5 rounded-r-2xl bg-gradient-to-l from-sidebar/50 via-sidebar/20 to-transparent md:hidden" />
                {displayedRoutes.map((route, index) => {
                    const isActive = activeItem === route.id
                    return (
                        <motion.button
                            key={route.id}
                            ref={(node) => setButtonRef(route.id, node)}
                            type="button"
                            aria-label={t(route.id as NavItem)}
                            aria-current={isActive ? "page" : undefined}
                            onClick={() => setActiveItem(route.id as NavItem)}
                            onMouseEnter={() => {
                                if (!isMobile) {
                                    preload(route.id)
                                }
                            }}
                            className={cn(
                                "group relative z-20 flex items-center justify-center rounded-lg border transition-[color,background-color,border-color,box-shadow] duration-150 md:w-full",
                                isMobile ? "size-11" : "h-11 md:justify-start md:gap-3 md:px-3",
                                isActive
                                    ? cn(
                                        "border-transparent bg-primary/15 text-primary border-t-2 border-t-primary md:border-t-0 md:border-l-2 md:border-l-primary md:border-r-0 md:border-b-0",
                                        isMobile && "shadow-[0_0_12px_rgba(var(--primary),0.25)]"
                                    )
                                    : "border-transparent text-sidebar-foreground/50 hover:bg-muted/60 hover:text-sidebar-foreground"
                            )}
                            initial={lightweightMotion ? false : { opacity: 0, scale: 0.8 }}
                            animate={lightweightMotion ? undefined : {
                                opacity: 1,
                                scale: 1,
                                transition: {
                                    delay: index * 0.05,
                                    duration: 0.3,
                                }
                            }}
                            whileTap={lightweightMotion
                                ? { scale: 0.88, transition: { type: "spring", stiffness: 400, damping: 17 } }
                                : { scale: 0.97 }}
                            onPointerDown={() => {
                                if (isMobile) setPressedItem(route.id)
                            }}
                            onPointerUp={() => {
                                if (isMobile) setPressedItem(null)
                            }}
                            onPointerLeave={() => {
                                if (isMobile) setPressedItem(null)
                            }}
                            onPointerCancel={() => {
                                if (isMobile) setPressedItem(null)
                            }}
                        >
                            <route.icon className="size-4 shrink-0 md:size-[1.125rem]" strokeWidth={1.5} />
                            <span className="hidden text-sm font-medium md:inline">
                                {t(route.id as NavItem)}
                            </span>
                        </motion.button>
                    )
                })}
                {isMobile && mobileMoreRoutes.length > 0 && (
                    <motion.button
                        ref={setMoreButtonRef}
                        type="button"
                        aria-label={t('moreLabel')}
                        aria-haspopup="dialog"
                        aria-controls={MORE_SHEET_ID}
                        aria-expanded={moreOpen}
                        aria-current={isMoreActive ? "page" : undefined}
                        onClick={() => setMoreOpen(true)}
                        className={cn(
                            "group relative z-20 flex size-11 shrink-0 items-center justify-center rounded-lg border transition-[color,background-color,border-color,box-shadow] duration-150",
                            isMoreActive
                                ? "border-transparent bg-primary/15 text-primary shadow-[0_0_12px_rgba(var(--primary),0.25)]"
                                : "border-transparent text-sidebar-foreground/50 hover:bg-muted/60 hover:text-sidebar-foreground"
                        )}
                        whileTap={{ scale: 0.88, transition: { type: "spring", stiffness: 400, damping: 17 } }}
                        onPointerDown={() => setPressedItem(MORE_ITEM_ID)}
                        onPointerUp={() => setPressedItem(null)}
                        onPointerLeave={() => setPressedItem(null)}
                        onPointerCancel={() => setPressedItem(null)}
                    >
                        <Ellipsis className="size-5 shrink-0" strokeWidth={1.5} />
                    </motion.button>
                )}
            </motion.nav>

            {isMobile &&
                visibleLabelEntries.map(({ surfaceId, item }) => {
                    const anchor = labelAnchors[surfaceId]
                    if (!anchor) return null
                    return (
                        <motion.span
                            key={`nav-label-${surfaceId}`}
                            initial={{ opacity: 0, y: 2 }}
                            animate={{ opacity: 1, y: 0 }}
                            exit={{ opacity: 0 }}
                            transition={{ duration: 0.12 }}
                            style={{ left: anchor.x, top: anchor.y }}
                            className="pointer-events-none fixed z-[60] -translate-x-1/2 -translate-y-[calc(100%+0.35rem)] whitespace-nowrap rounded-md bg-popover px-1.5 py-0.5 text-[10px] font-medium text-popover-foreground shadow-md ring-1 ring-border"
                        >
                            {item === MORE_ITEM_ID ? t('more') : t(item as NavItem)}
                        </motion.span>
                    )
                })}

            <dialog
                ref={moreDialogRef}
                id={MORE_SHEET_ID}
                aria-labelledby={MORE_SHEET_TITLE_ID}
                className="fixed inset-0 m-0 h-dvh max-h-none w-screen max-w-none overflow-hidden border-0 bg-transparent p-0 text-sidebar-foreground backdrop:bg-black/30"
                onCancel={(event) => {
                    event.preventDefault()
                    closeMoreSheet()
                }}
                onClick={(event) => {
                    if (event.target === event.currentTarget) closeMoreSheet()
                }}
                onClose={() => {
                    setMoreOpen(false)
                    moreButtonRef.current?.focus()
                }}
            >
                <section
                    className={cn(
                        "absolute inset-x-0 bottom-0 max-h-[min(70dvh,32rem)] overflow-hidden rounded-t-2xl border border-b-0 border-border bg-sidebar shadow-[0_-12px_36px_rgba(0,0,0,0.18)]",
                        "pb-[calc(1rem+env(safe-area-inset-bottom,0px))] pl-[max(1rem,env(safe-area-inset-left,0px))] pr-[max(1rem,env(safe-area-inset-right,0px))] pt-3"
                    )}
                >
                    <div className="mx-auto mb-2 h-1 w-10 rounded-full bg-muted-foreground/25" />
                    <div className="flex items-center justify-between gap-3 pb-3">
                        <h2 id={MORE_SHEET_TITLE_ID} className="text-sm font-semibold text-sidebar-foreground">
                            {t('moreSheetTitle')}
                        </h2>
                        <button
                            type="button"
                            aria-label={t('close')}
                            onClick={closeMoreSheet}
                            className="flex size-11 shrink-0 items-center justify-center rounded-lg text-sidebar-foreground/60 transition-colors hover:bg-muted/60 hover:text-sidebar-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                        >
                            <X className="size-5" strokeWidth={1.5} />
                        </button>
                    </div>
                    <div className="grid max-h-[calc(min(70dvh,32rem)-5.5rem-env(safe-area-inset-bottom,0px))] grid-cols-3 gap-2 overflow-y-auto overscroll-contain pb-1 sm:grid-cols-4">
                        {mobileMoreRoutes.map((route) => {
                            const isActive = activeItem === route.id
                            return (
                                <button
                                    key={route.id}
                                    ref={(node) => setButtonRef(`${MORE_SHEET_ID}-${route.id}`, node)}
                                    type="button"
                                    aria-label={t(route.id as NavItem)}
                                    aria-current={isActive ? "page" : undefined}
                                    onClick={() => selectMobileRoute(route.id as NavItem)}
                                    className={cn(
                                        "flex min-h-14 flex-col items-center justify-center gap-1.5 rounded-xl border px-2 py-2 text-center transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary",
                                        isActive
                                            ? "border-primary/50 bg-primary/15 text-primary"
                                            : "border-transparent text-sidebar-foreground/70 hover:bg-muted/60 hover:text-sidebar-foreground"
                                    )}
                                >
                                    <route.icon className="size-5 shrink-0" strokeWidth={1.5} />
                                    <span className="text-xs font-medium leading-tight">
                                        {t(route.id as NavItem)}
                                    </span>
                                </button>
                            )
                        })}
                    </div>
                </section>
            </dialog>
        </div>
    )
}
