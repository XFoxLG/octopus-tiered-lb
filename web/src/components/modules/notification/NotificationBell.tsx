'use client';

import { Bell, CheckCheck, ExternalLink } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useMarkAllNotificationsRead, useNotifications, useNotificationStream, useUnreadNotificationCount } from '@/api/endpoints/notification';
import { useNavStore } from '@/components/modules/navbar';
import { resolveNotifContent, resolveNotifTitle } from './notif-text';

function formatTime(ts?: number) {
    if (!ts) return '';
    return new Date(ts).toLocaleString();
}

export function NotificationBell() {
    const t = useTranslations('notification');
    const tn = useTranslations('notif');
    const setActiveItem = useNavStore((state) => state.setActiveItem);
    const { data: countData } = useUnreadNotificationCount();
    const { data: latest = [] } = useNotifications({ page: 1, page_size: 5 });
    const markAllRead = useMarkAllNotificationsRead();
    useNotificationStream();

    const unread = countData?.count ?? 0;

    return (
        <Popover>
            <PopoverTrigger asChild>
                <Button variant="ghost" size="icon" className="relative rounded-xl" aria-label={t('bell')}>
                    <Bell className="header-action-icon h-4 w-4" />
                    {unread > 0 && (
                        <span className="absolute -right-1 -top-1 min-w-5 rounded-full bg-destructive px-1 text-[10px] font-semibold leading-5 text-destructive-foreground">
                            {unread > 99 ? '99+' : unread}
                        </span>
                    )}
                </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-96 max-w-[calc(100vw-2rem)] p-0">
                <div className="flex items-center justify-between border-b px-4 py-3">
                    <div>
                        <div className="text-sm font-semibold">{t('title')}</div>
                        <div className="text-xs text-muted-foreground">{t('unreadCount', { count: unread })}</div>
                    </div>
                    <Button variant="ghost" size="sm" onClick={() => markAllRead.mutate()} disabled={markAllRead.isPending || unread === 0}>
                        <CheckCheck className="h-4 w-4" />
                        {t('actions.markAllRead')}
                    </Button>
                </div>
                <div className="max-h-96 overflow-y-auto p-2">
                    {latest.length === 0 ? (
                        <div className="py-8 text-center text-sm text-muted-foreground">{t('empty')}</div>
                    ) : latest.map((item) => (
                        <div key={item.id} className="min-w-0 rounded-xl px-3 py-2 hover:bg-muted/70">
                            <div className="flex items-start gap-2">
                                <Badge variant={item.read_at ? 'outline' : 'default'} className="shrink-0 text-[10px]">{t(`severity.${item.severity}`)}</Badge>
                                <span className="min-w-0 line-clamp-2 break-words text-sm font-medium [overflow-wrap:anywhere]">{resolveNotifTitle(item, tn)}</span>
                            </div>
                            <p className="mt-1 line-clamp-2 break-words text-xs text-muted-foreground [overflow-wrap:anywhere]">{resolveNotifContent(item, tn)}</p>
                            <div className="mt-1 text-[11px] text-muted-foreground">{formatTime(item.created_at)}</div>
                        </div>
                    ))}
                </div>
                <button
                    className="flex w-full items-center justify-center gap-2 border-t px-4 py-3 text-sm font-medium text-primary hover:bg-muted"
                    onClick={() => setActiveItem('notification')}
                >
                    {t('actions.openCenter')}
                    <ExternalLink className="h-4 w-4" />
                </button>
            </PopoverContent>
        </Popover>
    );
}
