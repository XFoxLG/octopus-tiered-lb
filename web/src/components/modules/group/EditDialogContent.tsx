'use client';

import { useCallback, useId, useRef, useState } from 'react';
import { Loader2, Pencil, TestTubeDiagonal, Trash2, Waves } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { Group, GroupTestResult } from '@/api/endpoints/group';
import { Button } from '@/components/ui/button';
import {
    AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
    AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
    MorphingDialogClose, MorphingDialogDescription, MorphingDialogTitle, useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { cn } from '@/lib/utils';
import { AIRouteButton } from './AIRouteButton';
import { AvailabilityResultsPanel } from './AvailabilityResultsPanel';
import { GroupEditor, type GroupEditorValues } from './Editor';
import type { SelectedMember } from './ItemList';
import { normalizeEndpointType, supportsGroupTest } from './utils';

export const GROUP_EDIT_DIALOG_CLASS = 'relative flex h-[calc(100dvh-1rem)] max-h-[calc(100dvh-1rem)] w-[calc(100vw-1rem)] max-w-none flex-col overflow-hidden rounded-lg border border-border bg-card p-3 text-card-foreground shadow-lg sm:max-h-[calc(100dvh-2rem)] sm:max-w-none md:h-[calc(100dvh-2rem)] md:w-[min(100vw-2rem,100rem)] md:p-5';

export interface EditDialogContentProps {
    group: Group;
    editMembers: SelectedMember[];
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues, onDone?: () => void) => void;
    onTestAvailability: (draft?: GroupEditorValues) => void;
    onRemoveFailedModels: (draft?: GroupEditorValues) => void;
    isTestingAvailability: boolean;
    canRemoveFailedModels: boolean;
    testProgressCompleted: number;
    testProgressTotal: number;
    testProgressValue: number;
    testResults: GroupTestResult[];
    testTaskId?: string | null;
    testError?: string;
    confirmRemoval?: boolean;
    availabilitySummary?: {
        unavailableCount: number;
        availableCount: number;
        allAvailable: boolean;
        fullyMatched: boolean;
    };
}

export function EditDialogContent({
    group, editMembers, isSubmitting, onSubmit, onTestAvailability, onRemoveFailedModels,
    isTestingAvailability, canRemoveFailedModels, testProgressCompleted, testProgressTotal,
    testProgressValue, testResults, availabilitySummary, testTaskId, testError, confirmRemoval = false,
}: EditDialogContentProps) {
    const t = useTranslations('group');
    const { setIsOpen } = useMorphingDialog();
    const hasResults = Boolean(testTaskId || isTestingAvailability || availabilitySummary || testResults.length || testError);
    const [view, setView] = useState<'edit' | 'results'>('edit');
    const viewId = useId();
    const [showRemoveConfirm, setShowRemoveConfirm] = useState(false);
    const draftRef = useRef<GroupEditorValues | null>(null);
    const [draftMemberCount, setDraftMemberCount] = useState(editMembers.length);
    const [testedMembers, setTestedMembers] = useState<SelectedMember[]>(editMembers);
    const canTest = Boolean(group.id && supportsGroupTest(group.endpoint_type));

    const handleDraftChange = useCallback((draft: GroupEditorValues) => {
        draftRef.current = draft;
        setDraftMemberCount((current) => current === draft.members.length ? current : draft.members.length);
    }, []);

    const handleTest = () => {
        const draft = draftRef.current;
        setTestedMembers((draft?.members ?? editMembers).map((member) => ({ ...member })));
        setView('results');
        onTestAvailability(draft ?? undefined);
    };
    const handleRemoveFailed = () => {
        if (confirmRemoval) setShowRemoveConfirm(true);
        else onRemoveFailedModels(draftRef.current ?? undefined);
    };

    return (
        <div className="relative flex h-full min-h-0 w-full max-w-full flex-1 flex-col 2xl:h-auto gap-3 sm:gap-4">
            <MorphingDialogTitle className="shrink-0">
                <header className="relative mb-4 grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-3 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center">
                    <div className="min-w-0">
                        <div className="mb-3 inline-flex items-center gap-2 rounded-full border border-primary/15 bg-card px-3 py-1 text-[0.68rem] font-semibold text-primary"><Waves className="size-3.5" />{t('detail.actions.edit')}</div>
                        <h2 className="truncate text-lg font-semibold sm:text-xl" title={group.name}>{group.name}</h2>
                    </div>
                    <div className="col-span-2 row-start-2 flex min-w-0 flex-wrap items-center gap-2 empty:hidden sm:col-span-1 sm:col-start-2 sm:row-start-1 sm:justify-end">
                        {canTest ? (
                            <>
                                <AIRouteButton scope="group" groupId={group.id} variant="ghost"
                                    className="h-11 max-w-full gap-2 rounded-lg border border-border/60 bg-muted/30 px-3 text-xs font-medium text-muted-foreground shadow-none transition-colors duration-150 hover:translate-y-0 hover:border-border hover:bg-muted hover:text-foreground dark:hover:bg-muted sm:h-9 [&>span]:truncate"
                                    onSuccess={() => setIsOpen(false)} />
                                <Button type="button" variant="ghost" onClick={handleTest}
                                    disabled={isTestingAvailability || isSubmitting || draftMemberCount === 0}
                                    className="h-11 max-w-full gap-2 rounded-lg border border-border/60 bg-muted/30 px-3 text-xs font-medium text-muted-foreground shadow-none transition-colors duration-150 hover:translate-y-0 hover:border-border hover:bg-muted hover:text-foreground dark:hover:bg-muted sm:h-9 [&>span]:truncate">
                                    {isTestingAvailability ? <Loader2 className="size-4 animate-spin" /> : <TestTubeDiagonal className="size-4" />}
                                    {t('detail.availability.testAll')}
                                </Button>
                            </>
                        ) : null}
                    </div>
                    <MorphingDialogClose className="relative col-start-2 row-start-1 right-auto top-auto size-11 shrink-0 rounded-lg border-transparent bg-transparent p-2 text-muted-foreground hover:bg-muted sm:col-start-3 sm:right-auto sm:top-auto sm:size-9 sm:p-2 [&>svg]:size-4" />
                </header>
            </MorphingDialogTitle>
            {hasResults || view === 'results' ? (
                <div role="tablist" aria-label={t('detail.availability.viewLabel')} className="flex shrink-0 gap-1 border-b border-border">
                    {(['edit', 'results'] as const).map((item) => (
                        <button key={item} type="button" role="tab" id={`${viewId}-${item}-tab`} aria-controls={`${viewId}-${item}-panel`} aria-selected={view === item}
                            onClick={() => setView(item)} className={cn(
                                'inline-flex h-10 items-center gap-2 border-b-2 px-3 text-sm font-medium transition-colors',
                                view === item ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground',
                            )}>
                            {item === 'edit' ? <Pencil className="size-4" /> : <TestTubeDiagonal className="size-4" />}
                            {item === 'edit' ? t('detail.actions.edit') : t('detail.availability.resultsTab')}
                            {item === 'results' && isTestingAvailability ? <Loader2 className="size-3.5 animate-spin" /> : null}
                        </button>
                    ))}
                </div>
            ) : null}
            <MorphingDialogDescription disableLayoutAnimation className="flex min-h-0 flex-1 overflow-hidden">
                <div role="tabpanel" id={`${viewId}-edit-panel`} aria-labelledby={hasResults || view === 'results' ? `${viewId}-edit-tab` : undefined} aria-label={t('detail.actions.edit')} aria-hidden={view !== 'edit'} inert={view !== 'edit'}
                    className={cn('min-h-0 min-w-0 flex-1', view === 'edit' ? 'flex' : 'hidden')}>
                    <GroupEditor key={`edit-group-${group.id}`} className="min-h-0 flex-1"
                        initial={{
                            name: group.name, category: group.category ?? '',
                            endpoint_type: normalizeEndpointType(group.endpoint_type),
                            endpoint_provider: group.endpoint_provider ?? '', outbound_format: group.outbound_format ?? '',
                            match_regex: group.match_regex ?? '', condition: group.condition ?? '', mode: group.mode,
                            first_token_time_out: group.first_token_time_out ?? 0, attempt_time_out: group.attempt_time_out ?? 0,
                            request_timeout_seconds: group.request_timeout_seconds ?? 0,
                            relay_retry_count: group.relay_retry_count ?? -1,
                            relay_route_retries: group.relay_route_retries ?? -1,
                            stream_keepalive_enabled: group.stream_keepalive_enabled ?? false,
                            assistant_prefill_compat: group.assistant_prefill_compat ?? false,
                            forward_mode: group.forward_mode ?? 'inherit',
                            hedge_enabled: group.hedge_enabled ?? false, hedge_delay_ms: group.hedge_delay_ms ?? 300,
                            session_keep_time: group.session_keep_time ?? 0, reasoning_buffer_strategy: group.reasoning_buffer_strategy ?? '',
                            stream_idle_timeout: group.stream_idle_timeout ?? 0,
                            default_reasoning_effort: group.default_reasoning_effort ?? '', reasoning_force_override: group.reasoning_force_override ?? false,
                            param_override: group.param_override ?? '', custom_header: group.custom_header ?? [],
                            members: editMembers,
                        }}
                        submitText={t('detail.actions.save')} submittingText={t('create.submitting')} isSubmitting={isSubmitting}
                        onCancel={() => setIsOpen(false)} onDraftChange={handleDraftChange}
                        onSubmit={(values) => onSubmit(values, () => setIsOpen(false))} />
                </div>
                <div role="tabpanel" id={`${viewId}-results-panel`} aria-labelledby={`${viewId}-results-tab`} aria-label={t('detail.availability.resultsTab')} aria-hidden={view !== 'results'} inert={view !== 'results'}
                    className={cn('min-h-0 min-w-0 flex-1 flex-col', view === 'results' ? 'flex' : 'hidden')}>
                    <AvailabilityResultsPanel isTesting={isTestingAvailability} members={testedMembers}
                        completed={testProgressCompleted} total={testProgressTotal || testedMembers.length} progressValue={testProgressValue}
                        results={testResults} summary={availabilitySummary} error={testError} />
                    <div className="flex shrink-0 flex-wrap justify-end gap-2 border-t border-border pt-3 pb-[env(safe-area-inset-bottom)]">
                        {canRemoveFailedModels ? (
                            <Button type="button" variant="outline" onClick={handleRemoveFailed}
                                className="h-10 rounded-lg border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive">
                                <Trash2 className="size-4" />{t('detail.actions.removeFailedModels')}
                            </Button>
                        ) : null}
                        <Button type="button" variant="secondary" className="h-10 rounded-lg" onClick={() => setView('edit')}>
                            <Pencil className="size-4" />{t('detail.actions.edit')}
                        </Button>
                    </div>
                </div>
            </MorphingDialogDescription>
            <AlertDialog open={showRemoveConfirm} onOpenChange={setShowRemoveConfirm}>
                <AlertDialogContent>
                    <AlertDialogHeader>
                        <AlertDialogTitle>{t('detail.actions.removeFailedModels')}</AlertDialogTitle>
                        <AlertDialogDescription>{t('detail.availability.removeFailedConfirm')}</AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel>{t('detail.actions.cancel')}</AlertDialogCancel>
                        <AlertDialogAction disabled={!canRemoveFailedModels} onClick={() => { onRemoveFailedModels(draftRef.current ?? undefined); setShowRemoveConfirm(false); }}>
                            {t('detail.actions.confirmRemove')}
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </div>
    );
}
