'use client';

import {
    type ReactNode,
    useCallback,
    useEffect,
    useMemo,
    useRef,
    useState,
} from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { cn } from '@/lib/utils';
import { resolveReachEndTrigger } from './reach-end';

const BREAKPOINTS = {
    sm: 640,
    md: 768,
    lg: 960,
    xl: 1280,
    '2xl': 1536,
} as const;

type Breakpoint = keyof typeof BREAKPOINTS;
type ResponsiveColumns = Partial<Record<Breakpoint | 'default', number>>;
type ColumnConfig = ResponsiveColumns | ((width: number) => number);

interface VirtualizedGridProps<T> {
    items: T[];
    layout?: 'grid' | 'list' | 'compact';
    columns: ColumnConfig;
    estimateItemHeight: number;
    gap?: number;
    overscan?: number;
    getItemKey: (item: T, index: number) => string | number;
    renderItem: (item: T, index: number) => ReactNode;
    header?: ReactNode;
    footer?: ReactNode;
    onReachEnd?: () => void;
    reachEndEnabled?: boolean;
    reachEndOffset?: number;
    bottomPaddingClassName?: string;
}

function getColumnsForWidth(
    width: number,
    columns: ColumnConfig,
): number {
    if (typeof columns === 'function') return columns(width);
    if (width >= BREAKPOINTS['2xl'] && columns['2xl'] !== undefined) return columns['2xl'];
    if (width >= BREAKPOINTS.xl && columns.xl !== undefined) return columns.xl;
    if (width >= BREAKPOINTS.lg && columns.lg !== undefined) return columns.lg;
    if (width >= BREAKPOINTS.md && columns.md !== undefined) return columns.md;
    if (width >= BREAKPOINTS.sm && columns.sm !== undefined) return columns.sm;
    return columns.default ?? 1;
}

export function VirtualizedGrid<T>({
    items,
    layout = 'grid',
    columns,
    estimateItemHeight,
    gap = 16,
    overscan = 4,
    getItemKey,
    renderItem,
    header = null,
    footer = null,
    onReachEnd,
    reachEndEnabled = false,
    reachEndOffset = 1,
    bottomPaddingClassName = 'pb-3 md:pb-4',
}: VirtualizedGridProps<T>) {
    'use no memo';

    const [containerWidth, setContainerWidth] = useState(() =>
        typeof window === 'undefined' ? 1024 : window.innerWidth
    );
    const containerRef = useRef<HTMLDivElement | null>(null);
    const headerRef = useRef<HTMLDivElement | null>(null);
    const [headerHeight, setHeaderHeight] = useState(0);
    const reachEndTriggeredKeyRef = useRef<string | number | null>(null);

    useEffect(() => {
        const el = containerRef.current;
        if (!el) return;

        const updateWidth = () => {
            const nextWidth = el.clientWidth;
            setContainerWidth((prev) => (prev === nextWidth ? prev : nextWidth));
        };

        updateWidth();

        if (typeof ResizeObserver === 'undefined') return;
        const observer = new ResizeObserver(updateWidth);
        observer.observe(el);

        return () => {
            observer.disconnect();
        };
    }, []);

    // Track the header height so the virtualizer can offset its rows past it
    // (header scrolls together with the list, like the hub overview).
    useEffect(() => {
        if (header === null) {
            setHeaderHeight(0);
            return;
        }
        const el = headerRef.current;
        if (!el) return;
        const updateHeight = () => {
            const nextHeight = el.offsetHeight;
            setHeaderHeight((prev) => (prev === nextHeight ? prev : nextHeight));
        };
        updateHeight();
        if (typeof ResizeObserver === 'undefined') return;
        const observer = new ResizeObserver(updateHeight);
        observer.observe(el);
        return () => {
            observer.disconnect();
        };
    }, [header]);

    const columnCount = useMemo(() => {
        if (layout === 'list' || layout === 'compact') return 1;
        return Math.max(1, getColumnsForWidth(containerWidth, columns));
    }, [layout, containerWidth, columns]);
    const effectiveOverscan = containerWidth < BREAKPOINTS.md ? Math.min(overscan, 2) : overscan;

    const itemRowCount = useMemo(
        () => (items.length === 0 ? 0 : Math.ceil(items.length / columnCount)),
        [items.length, columnCount]
    );
    const hasFooterRow = footer !== null;
    const rowCount = itemRowCount + (hasFooterRow ? 1 : 0);
    const getVirtualRowKey = useCallback((rowIndex: number) => {
        if (hasFooterRow && rowIndex === itemRowCount) {
            return '__virtual-footer__';
        }

        const rowStartIndex = rowIndex * columnCount;
        const firstItem = items[rowStartIndex];
        if (!firstItem) return `row-empty-${rowIndex}`;

        // Keep row keys stable across prepend/append updates (especially log stream updates),
        // otherwise virtualizer measurements are constantly invalidated and spacing falls back to estimates.
        return `row-${String(getItemKey(firstItem, rowStartIndex))}`;
    }, [hasFooterRow, itemRowCount, columnCount, items, getItemKey]);

    // eslint-disable-next-line react-hooks/incompatible-library
    const rowVirtualizer = useVirtualizer({
        count: rowCount,
        getScrollElement: () => containerRef.current,
        getItemKey: getVirtualRowKey,
        estimateSize: () => estimateItemHeight + gap,
        // Offset rows past the header so the header shares the scroll flow
        // (scrolls away with the list, like the hub overview).
        scrollMargin: headerHeight,
        // Use layout height (not transformed visual height) to avoid scale-animation
        // shrinking measurements during page enter transitions.
        measureElement: (element) =>
            element instanceof HTMLElement
                ? element.offsetHeight
                : element.getBoundingClientRect().height,
        overscan: effectiveOverscan,
    });

    const virtualRows = rowVirtualizer.getVirtualItems();
    const lastItemKey = items.length > 0
        ? getItemKey(items[items.length - 1]!, items.length - 1)
        : '';

    useEffect(() => {
        if (!onReachEnd) {
            reachEndTriggeredKeyRef.current = null;
            return;
        }

        const lastVirtualIndex = virtualRows.length > 0 ? virtualRows[virtualRows.length - 1]!.index : -1;
        const { shouldTrigger, nextTriggeredKey } = resolveReachEndTrigger({
            enabled: reachEndEnabled,
            itemRowCount,
            lastVirtualIndex,
            reachEndOffset,
            lastItemKey,
            triggeredKey: reachEndTriggeredKeyRef.current,
        });
        reachEndTriggeredKeyRef.current = nextTriggeredKey;
        if (shouldTrigger) onReachEnd();
    }, [onReachEnd, reachEndEnabled, itemRowCount, reachEndOffset, virtualRows, lastItemKey]);

    return (
        <div className="relative h-full min-h-0 w-full">
            <div
                ref={containerRef}
                className={cn('relative h-full w-full overflow-y-auto overscroll-contain rounded-t-3xl touch-pan-y', bottomPaddingClassName)}
            >
                {header !== null ? (
                    <div ref={headerRef} className="relative w-full">
                        {header}
                    </div>
                ) : null}
                {rowCount === 0 ? null : (
                    <div className="relative w-full" style={{ height: `${rowVirtualizer.getTotalSize()}px` }}>
                        {virtualRows.map((virtualRow) => {
                            // When a header precedes the list, subtract scrollMargin from the
                            // translate so rows are positioned within the sizing container
                            // (which sits below the header), per the @tanstack/virtual docs.
                            const translateY = virtualRow.start - headerHeight;
                            if (hasFooterRow && virtualRow.index === itemRowCount) {
                                return (
                                    <div
                                        key={virtualRow.key}
                                        data-index={virtualRow.index}
                                        ref={rowVirtualizer.measureElement}
                                        className="absolute left-0 top-0 w-full"
                                        style={{
                                            transform: `translateY(${translateY}px)`,
                                        }}
                                    >
                                        {footer}
                                    </div>
                                );
                            }

                            const rowStartIndex = virtualRow.index * columnCount;
                            const rowEndIndex = Math.min(rowStartIndex + columnCount, items.length);
                            const rowItems = items.slice(rowStartIndex, rowEndIndex);
                            const rowPaddingBottom = virtualRow.index === itemRowCount - 1 && !hasFooterRow ? 0 : gap;

                            return (
                                <div
                                    key={virtualRow.key}
                                    data-index={virtualRow.index}
                                    ref={rowVirtualizer.measureElement}
                                    className="absolute left-0 top-0 w-full"
                                    style={{
                                        transform: `translateY(${translateY}px)`,
                                    }}
                                >
                                    <div
                                        className="grid"
                                        style={{
                                            gridTemplateColumns: `repeat(${columnCount}, minmax(0, 1fr))`,
                                            gap: `${gap}px`,
                                            paddingBottom: `${rowPaddingBottom}px`,
                                        }}
                                    >
                                        {rowItems.map((item, columnIndex) => {
                                            const itemIndex = rowStartIndex + columnIndex;
                                            return (
                                                <div key={String(getItemKey(item, itemIndex))} className="min-w-0">
                                                    {renderItem(item, itemIndex)}
                                                </div>
                                            );
                                        })}
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>
        </div>
    );
}
