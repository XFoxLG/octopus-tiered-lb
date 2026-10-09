'use client';

import { useState } from 'react';
import { Wrench, Trash2, Stethoscope, ChevronDown } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { useGroupList, useDeleteAllGroups, usePurgeUnavailableGroupItems } from '@/api/endpoints/group';
import { toast } from '@/components/common/Toast';
import { Button, buttonVariants } from '@/components/ui/button';
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import { TOOLBAR_TEXT_ACTION_CLASS } from '../toolbar/action-styles';

type MaintenanceAction = 'purge' | 'delete-all' | null;

type MaintenanceButtonProps = {
    variant?: 'ghost' | 'default';
    className?: string;
};

export function MaintenanceButton({ variant = 'ghost', className }: MaintenanceButtonProps) {
    const t = useTranslations('group');
    const settingT = useTranslations('setting');
    const { data: groups = [] } = useGroupList();
    const deleteAllGroups = useDeleteAllGroups();
    const purge = usePurgeUnavailableGroupItems();

    const [popoverOpen, setPopoverOpen] = useState(false);
    const [action, setAction] = useState<MaintenanceAction>(null);

    const groupCount = groups.length;
    const pending = deleteAllGroups.isPending || purge.isPending;

    const handlePurge = () => {
        purge.mutate(undefined, {
            onSuccess: (result) => {
                setAction(null);
                if (result.deleted_count === 0) {
                    toast.success(settingT('purgeUnavailable.successNone'));
                    return;
                }
                toast.success(settingT('purgeUnavailable.success', { count: result.deleted_count }), {
                    description: settingT('purgeUnavailable.summary', {
                        disabled: result.channel_disabled,
                        missing: result.model_missing,
                        removed: result.channel_missing,
                    }),
                });
            },
            onError: (error: Error) => {
                toast.error(settingT('purgeUnavailable.failed'), { description: error.message });
            },
        });
    };

    const handleDeleteAll = () => {
        deleteAllGroups.mutate(undefined, {
            onSuccess: (result) => {
                setAction(null);
                toast.success(settingT('routeGroups.success', { count: result.deleted_count }));
            },
            onError: (error: Error) => {
                toast.error(settingT('routeGroups.failed'), { description: error.message });
            },
        });
    };

    const triggerClassName = cn(
        buttonVariants({
            variant: 'ghost',
            size: 'default',
            className: TOOLBAR_TEXT_ACTION_CLASS,
        }),
        className,
    );

    return (
        <>
            <Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
                <PopoverTrigger asChild>
                    {variant === 'default' ? (
                        <Button type="button" className={cn('rounded-lg', className)}>
                            <Wrench className="size-4" />
                            {t('actions.maintenance')}
                        </Button>
                    ) : (
                        <button type="button" className={triggerClassName}>
                            <Wrench className="size-4" />
                            <span>{t('actions.maintenance')}</span>
                            <ChevronDown className="size-3.5 opacity-70" />
                        </button>
                    )}
                </PopoverTrigger>
                <PopoverContent align="end" className="w-64 p-2">
                    <button
                        type="button"
                        disabled={pending}
                        onClick={() => {
                            setPopoverOpen(false);
                            setAction('purge');
                        }}
                        className="flex w-full items-start gap-3 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-accent/50 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                        <Stethoscope className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
                        <div className="min-w-0 flex-1">
                            <div className="truncate text-sm font-medium">{settingT('purgeUnavailable.title')}</div>
                            <div className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{settingT('purgeUnavailable.description')}</div>
                        </div>
                    </button>
                    <button
                        type="button"
                        disabled={pending || groupCount === 0}
                        onClick={() => {
                            setPopoverOpen(false);
                            setAction('delete-all');
                        }}
                        className="flex w-full items-start gap-3 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-accent/50 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                        <Trash2 className="mt-0.5 size-4 shrink-0 text-destructive" />
                        <div className="min-w-0 flex-1">
                            <div className="truncate text-sm font-medium">{settingT('routeGroups.button')}</div>
                            <div className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{settingT('routeGroups.description')}</div>
                        </div>
                    </button>
                </PopoverContent>
            </Popover>

            <AlertDialog open={action === 'purge'} onOpenChange={(open) => { if (!open && !purge.isPending) setAction(null); }}>
                <AlertDialogContent className="rounded-xl">
                    <AlertDialogHeader>
                        <AlertDialogTitle>{settingT('purgeUnavailable.confirmTitle')}</AlertDialogTitle>
                        <AlertDialogDescription className="whitespace-pre-line">
                            {settingT('purgeUnavailable.confirmDescription')}
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel disabled={purge.isPending}>{settingT('purgeUnavailable.cancel')}</AlertDialogCancel>
                        <AlertDialogAction
                            disabled={purge.isPending}
                            onClick={(event) => {
                                event.preventDefault();
                                handlePurge();
                            }}
                        >
                            {purge.isPending ? settingT('purgeUnavailable.purging') : settingT('purgeUnavailable.confirm')}
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>

            <AlertDialog open={action === 'delete-all'} onOpenChange={(open) => { if (!open && !deleteAllGroups.isPending) setAction(null); }}>
                <AlertDialogContent className="rounded-xl">
                    <AlertDialogHeader>
                        <AlertDialogTitle>{settingT('routeGroups.confirmTitle')}</AlertDialogTitle>
                        <AlertDialogDescription className="whitespace-pre-line">
                            {settingT('routeGroups.confirmDescription', { count: groupCount })}
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel disabled={deleteAllGroups.isPending}>{settingT('routeGroups.cancel')}</AlertDialogCancel>
                        <AlertDialogAction
                            disabled={deleteAllGroups.isPending}
                            onClick={(event) => {
                                event.preventDefault();
                                handleDeleteAll();
                            }}
                        >
                            {deleteAllGroups.isPending ? settingT('routeGroups.deleting') : settingT('routeGroups.confirm')}
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </>
    );
}
