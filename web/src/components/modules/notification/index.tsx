'use client';

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Archive, Check, Inbox, Loader2, RefreshCw, Search, Trash2 } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { PageWrapper } from '@/components/common/PageWrapper';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { useIsMobile } from '@/hooks/use-mobile';
import { resolveNotifContent, resolveNotifTitle } from './notif-text';
import {
    useArchiveNotification,
    useDeleteNotification,
    useMarkAllNotificationsRead,
    useMarkNotificationRead,
    useMarkNotificationUnread,
    useNotificationDetail,
    useNotificationsInfinite,
    useUnreadNotificationCount,
    type NotificationFilter,
    type NotificationItem,
} from '@/api/endpoints/notification';

const NOTIFICATION_TYPES = ['', 'channel_expire', 'system', 'backup', 'key_health'];
const NOTIFICATION_SEVERITIES = ['', 'info', 'success', 'warning', 'error', 'critical'];
type NotificationSubTab = 'inbox' | 'archived';

function formatNotificationTime(timestamp?: number) {
    if (!timestamp) return '-';
    return new Date(timestamp).toLocaleString();
}

function getSeverityClassName(severity: string) {
    switch (severity) {
        case 'success':
            return 'bg-emerald-500/10 text-emerald-600 border-emerald-500/30';
        case 'warning':
            return 'bg-amber-500/10 text-amber-600 border-amber-500/30';
        case 'error':
        case 'critical':
            return 'bg-destructive/10 text-destructive border-destructive/30';
        default:
            return 'bg-muted text-muted-foreground';
    }
}

function SectionButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
    return (
        <Button variant={active ? 'default' : 'outline'} onClick={onClick} className="shrink-0">
            {children}
        </Button>
    );
}

function NotificationCard({ item, selected, onSelect }: { item: NotificationItem; selected: boolean; onSelect: () => void }) {
    const translateNotification = useTranslations('notification');
    const translateNotificationText = useTranslations('notif');

    return (
        <button
            onClick={onSelect}
            className={`w-full rounded-2xl border p-3 text-left transition hover:bg-muted/60 sm:p-4 ${selected ? 'border-primary bg-primary/5' : 'border-border bg-card'}`}
        >
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                        {!item.read_at && <span className="h-2 w-2 rounded-full bg-primary" />}
                        <Badge variant="outline">{translateNotification(`type.${item.type}`)}</Badge>
                        <Badge variant="outline" className={getSeverityClassName(item.severity)}>
                            {translateNotification(`severity.${item.severity}`)}
                        </Badge>
                    </div>
                    <h3 className="mt-2 truncate text-base font-semibold">
                        {resolveNotifTitle(item, translateNotificationText)}
                    </h3>
                    <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">
                        {resolveNotifContent(item, translateNotificationText)}
                    </p>
                </div>
                <div className="shrink-0 text-xs text-muted-foreground">
                    {formatNotificationTime(item.created_at)}
                </div>
            </div>
        </button>
    );
}

function NotificationDetailContent({ selected, onMarkRead, onMarkUnread, onArchive, onDelete }: {
    selected: NotificationItem;
    onMarkRead: () => void;
    onMarkUnread: () => void;
    onArchive: () => void;
    onDelete: () => void;
}) {
    const translateNotification = useTranslations('notification');
    const translateNotificationText = useTranslations('notif');

    return (
        <div className="space-y-4">
            <div>
                <div className="flex flex-wrap gap-2">
                    <Badge>{translateNotification(`type.${selected.type}`)}</Badge>
                    <Badge variant="outline" className={getSeverityClassName(selected.severity)}>
                        {translateNotification(`severity.${selected.severity}`)}
                    </Badge>
                </div>
                <h2 className="mt-3 break-words text-lg font-semibold">
                    {resolveNotifTitle(selected, translateNotificationText)}
                </h2>
                <p className="mt-1 text-xs text-muted-foreground">
                    {formatNotificationTime(selected.created_at)}
                </p>
            </div>
            <p className="whitespace-pre-wrap break-words text-sm text-muted-foreground">
                {resolveNotifContent(selected, translateNotificationText)}
            </p>
            <div className="flex flex-wrap gap-2">
                {selected.read_at ? (
                    <Button size="sm" variant="outline" onClick={onMarkUnread}>
                        {translateNotification('actions.markUnread')}
                    </Button>
                ) : (
                    <Button size="sm" onClick={onMarkRead}>
                        {translateNotification('actions.markRead')}
                    </Button>
                )}
                {!selected.archived_at && (
                    <Button size="sm" variant="outline" onClick={onArchive}>
                        {translateNotification('actions.archive')}
                    </Button>
                )}
                <Button size="sm" variant="destructive" onClick={onDelete}>
                    <Trash2 className="h-4 w-4" />
                    {translateNotification('actions.delete')}
                </Button>
            </div>
        </div>
    );
}

function useDebouncedValue<Value>(value: Value, delayMilliseconds = 300) {
    const [debouncedValue, setDebouncedValue] = useState(value);

    useEffect(() => {
        const timer = setTimeout(() => setDebouncedValue(value), delayMilliseconds);
        return () => clearTimeout(timer);
    }, [delayMilliseconds, value]);

    return debouncedValue;
}

export function Notification() {
    const translateNotification = useTranslations('notification');
    const translateNotificationText = useTranslations('notif');
    const [activeSubTab, setActiveSubTab] = useState<NotificationSubTab>('inbox');
    const [notificationType, setNotificationType] = useState('');
    const [notificationSeverity, setNotificationSeverity] = useState('');
    const [searchText, setSearchText] = useState('');
    const [selectedNotificationID, setSelectedNotificationID] = useState<number | undefined>();
    const debouncedSearchText = useDebouncedValue(searchText);
    const isMobile = useIsMobile();

    const notificationFilter: NotificationFilter = useMemo(() => ({
        archived: activeSubTab === 'archived',
        type: notificationType || undefined,
        severity: notificationSeverity || undefined,
        search: debouncedSearchText || undefined,
    }), [activeSubTab, debouncedSearchText, notificationSeverity, notificationType]);

    const {
        items,
        isLoading,
        isLoadingMore,
        hasMore,
        loadMore,
        refetch,
    } = useNotificationsInfinite(notificationFilter);
    const { data: unreadCountData } = useUnreadNotificationCount();
    const notificationDetail = useNotificationDetail(selectedNotificationID);
    const markNotificationRead = useMarkNotificationRead();
    const markNotificationUnread = useMarkNotificationUnread();
    const archiveNotification = useArchiveNotification();
    const deleteNotification = useDeleteNotification();
    const markAllNotificationsRead = useMarkAllNotificationsRead();
    const selectedNotification = notificationDetail.data;

    const canLoadMore = hasMore && !isLoading && !isLoadingMore && items.length > 0;
    const handleReachEnd = useCallback(() => {
        if (!canLoadMore) return;
        void loadMore();
    }, [canLoadMore, loadMore]);

    const listFooter = useMemo(() => {
        if (hasMore && (isLoading || isLoadingMore)) {
            return (
                <div className="flex justify-center py-6">
                    <div className="flex items-center gap-2 rounded-full border border-border/50 bg-card/80 px-4 py-2 shadow-sm backdrop-blur">
                        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                        <span className="text-xs text-muted-foreground">
                            {translateNotification('list.loadingMore')}
                        </span>
                    </div>
                </div>
            );
        }
        if (!hasMore && items.length > 0) {
            return (
                <div className="flex justify-center py-6">
                    <span className="text-xs text-muted-foreground/60">
                        {translateNotification('list.noMore')}
                    </span>
                </div>
            );
        }
        return null;
    }, [hasMore, isLoading, isLoadingMore, items.length, translateNotification]);

    const handleDeleteSelected = () => {
        if (!selectedNotification) return;
        deleteNotification.mutate(selectedNotification.id, {
            onSuccess: () => setSelectedNotificationID(undefined),
        });
    };

    return (
        <PageWrapper>
            <div className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-2 sm:gap-3">
                    <div className="min-w-0">
                        <h1 className="text-xl font-bold tracking-tight sm:text-2xl">
                            {translateNotification('title')}
                        </h1>
                        <p className="text-xs text-muted-foreground sm:text-sm">
                            {translateNotification('subtitle', { count: unreadCountData?.count ?? 0 })}
                        </p>
                    </div>
                    <div className="flex shrink-0 gap-2">
                        <Button variant="outline" onClick={() => void refetch()}>
                            <RefreshCw className="h-4 w-4" />
                            {translateNotification('actions.refresh')}
                        </Button>
                        <Button
                            onClick={() => markAllNotificationsRead.mutate()}
                            disabled={markAllNotificationsRead.isPending || (unreadCountData?.count ?? 0) === 0}
                        >
                            <Check className="h-4 w-4" />
                            {translateNotification('actions.markAllRead')}
                        </Button>
                    </div>
                </div>

                <div className="flex flex-wrap gap-2">
                    <SectionButton active={activeSubTab === 'inbox'} onClick={() => setActiveSubTab('inbox')}>
                        <Inbox className="h-4 w-4" />
                        {translateNotification('tabs.inbox')}
                    </SectionButton>
                    <SectionButton active={activeSubTab === 'archived'} onClick={() => setActiveSubTab('archived')}>
                        <Archive className="h-4 w-4" />
                        {translateNotification('tabs.archived')}
                    </SectionButton>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                    <div className="relative min-w-[12rem] flex-1">
                        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                        <Input
                            value={searchText}
                            onChange={(event) => setSearchText(event.target.value)}
                            placeholder={translateNotification('filters.search')}
                            className="h-9 pl-9"
                        />
                    </div>
                    <select
                        value={notificationType}
                        onChange={(event) => setNotificationType(event.target.value)}
                        className="h-9 rounded-xl border bg-background px-3 text-sm"
                    >
                        {NOTIFICATION_TYPES.map((value) => (
                            <option key={value} value={value}>
                                {value ? translateNotification(`type.${value}`) : translateNotification('filters.allTypes')}
                            </option>
                        ))}
                    </select>
                    <select
                        value={notificationSeverity}
                        onChange={(event) => setNotificationSeverity(event.target.value)}
                        className="h-9 rounded-xl border bg-background px-3 text-sm"
                    >
                        {NOTIFICATION_SEVERITIES.map((value) => (
                            <option key={value} value={value}>
                                {value ? translateNotification(`severity.${value}`) : translateNotification('filters.allSeverities')}
                            </option>
                        ))}
                    </select>
                </div>

                <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_24rem]">
                    <div className="flex min-h-0 flex-col">
                        {isLoading ? (
                            <div className="flex min-h-[24rem] items-center justify-center rounded-2xl border bg-card">
                                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                            </div>
                        ) : items.length === 0 ? (
                            <div className="flex min-h-[24rem] items-center justify-center rounded-2xl border bg-card p-8 text-center text-sm text-muted-foreground">
                                {translateNotification('empty')}
                            </div>
                        ) : (
                            <div className="h-[calc(100dvh-24rem)] min-h-[24rem]">
                                <VirtualizedGrid
                                    items={items}
                                    layout="list"
                                    columns={{ default: 1 }}
                                    estimateItemHeight={124}
                                    gap={12}
                                    overscan={6}
                                    getItemKey={(item) => `notification-${item.id}`}
                                    renderItem={(item) => (
                                        <NotificationCard
                                            item={item}
                                            selected={selectedNotificationID === item.id}
                                            onSelect={() => setSelectedNotificationID(item.id)}
                                        />
                                    )}
                                    footer={listFooter}
                                    onReachEnd={handleReachEnd}
                                    reachEndEnabled={canLoadMore}
                                    reachEndOffset={2}
                                    bottomPaddingClassName="pb-4"
                                />
                            </div>
                        )}
                    </div>

                    <aside className="hidden self-start rounded-2xl border bg-card p-4 lg:sticky lg:top-4 lg:block">
                        {!selectedNotification ? (
                            <div className="flex min-h-[16rem] items-center justify-center text-sm text-muted-foreground">
                                {translateNotification('detail.placeholder')}
                            </div>
                        ) : (
                            <NotificationDetailContent
                                selected={selectedNotification}
                                onMarkRead={() => markNotificationRead.mutate([selectedNotification.id])}
                                onMarkUnread={() => markNotificationUnread.mutate([selectedNotification.id])}
                                onArchive={() => archiveNotification.mutate([selectedNotification.id])}
                                onDelete={handleDeleteSelected}
                            />
                        )}
                    </aside>
                </div>

                <Dialog
                    open={isMobile && selectedNotificationID !== undefined}
                    onOpenChange={(open) => {
                        if (!open) setSelectedNotificationID(undefined);
                    }}
                >
                    <DialogContent className="max-h-[85dvh] overflow-y-auto">
                        {selectedNotification ? (
                            <>
                                <DialogHeader className="sr-only">
                                    <DialogTitle>{resolveNotifTitle(selectedNotification, translateNotificationText)}</DialogTitle>
                                    <DialogDescription>{formatNotificationTime(selectedNotification.created_at)}</DialogDescription>
                                </DialogHeader>
                                <NotificationDetailContent
                                    selected={selectedNotification}
                                    onMarkRead={() => markNotificationRead.mutate([selectedNotification.id])}
                                    onMarkUnread={() => markNotificationUnread.mutate([selectedNotification.id])}
                                    onArchive={() => {
                                        archiveNotification.mutate([selectedNotification.id]);
                                        setSelectedNotificationID(undefined);
                                    }}
                                    onDelete={handleDeleteSelected}
                                />
                            </>
                        ) : (
                            <div className="flex items-center justify-center py-8">
                                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                            </div>
                        )}
                    </DialogContent>
                </Dialog>
            </div>
        </PageWrapper>
    );
}
