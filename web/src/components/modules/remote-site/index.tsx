'use client';

import { useEffect } from 'react';
import { useTranslations } from 'next-intl';
import { Plus } from 'lucide-react';
import { PageWrapper } from '@/components/common/PageWrapper';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContents, TabsContent, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { Site } from '@/components/modules/site';
import { SiteChannelSection } from '@/components/modules/site-channel';
import { SettingSiteAutomation } from '@/components/modules/setting/SiteAutomation';
import { useHubTabStore } from './hub-tab-store';
import { useSiteUIStore } from '@/components/modules/site/ui-store';
import { useSubTabStore, type HubTab } from '@/components/modules/navbar/sub-tab-store';
import { getJumpTargetRoute, useJumpStore } from '@/stores/jump';

const TAB_LABEL_KEY: Record<HubTab, string> = {
    sites: 'tabs.sites',
    'site-channels': 'tabs.siteChannels',
    automation: 'tabs.automation',
};

export function RemoteSite() {
    const t = useTranslations('hub');
    const { activeTab, setActiveTab } = useHubTabStore();
    const { orderedTabs, visibleTabs } = useSubTabStore((s) => s.hub);
    const requestOpenCreateDialog = useSiteUIStore((state) => state.requestOpenCreateDialog);

    // 首次挂载：默认选中显示顺序中的第一个子标签（用户可在外观设置中拖拽排序/隐藏）。
    // 仅挂载时执行一次，keep-alive 下切换模块不会重新挂载，故不影响用户后续切换。
    // 若本次挂载源于跳转（pending jump 指向 hub），保留跳转目标 tab，不覆盖默认值。
    useEffect(() => {
        const pending = useJumpStore.getState().pending;
        const jumpingToHub = pending != null && getJumpTargetRoute(pending.target) === 'hub';
        if (!jumpingToHub && visibleTabs.length > 0) {
            setActiveTab(visibleTabs[0] as HubTab);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useEffect(() => {
        if (visibleTabs.length > 0 && !visibleTabs.includes(activeTab)) {
            setActiveTab(visibleTabs[0] as HubTab);
        }
    }, [visibleTabs, activeTab, setActiveTab]);

    const visibleSet = new Set(visibleTabs);
    const orderedVisible = orderedTabs.filter((tab) => visibleSet.has(tab));

    return (
        <PageWrapper className="h-full min-h-0 overflow-y-auto overscroll-contain space-y-4 sm:space-y-6 rounded-t-xl pb-3 md:pb-4">
            <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as HubTab)}>
                <section className="rounded-xl border border-border bg-card p-3 sm:p-5 text-card-foreground">
                    <div className="flex items-center justify-between gap-3">
                        <div className="overflow-x-auto -mx-1 px-1 scrollbar-none min-w-0">
                            <TabsList className="w-max min-w-full xl:min-w-0">
                                {orderedVisible.map((tab) => (
                                    <TabsTrigger key={tab} value={tab}>
                                        {t(TAB_LABEL_KEY[tab as HubTab])}
                                    </TabsTrigger>
                                ))}
                            </TabsList>
                        </div>
                        {activeTab === 'sites' && (
                            <Button
                                size="sm"
                                className="shrink-0 rounded-xl"
                                onClick={requestOpenCreateDialog}
                            >
                                <Plus className="size-4" />
                                <span className="hidden sm:inline">{t('addSite')}</span>
                            </Button>
                        )}
                    </div>
                </section>

                <TabsContents>
                    <TabsContent value="sites">
                        <Site />
                    </TabsContent>
                    <TabsContent value="site-channels">
                        {activeTab === 'site-channels' ? <SiteChannelSection /> : <div />}
                    </TabsContent>
                    <TabsContent value="automation">
                        {activeTab === 'automation' ? (
                            <div className="mx-auto max-w-2xl">
                                <SettingSiteAutomation />
                            </div>
                        ) : <div />}
                    </TabsContent>
                </TabsContents>
            </Tabs>
        </PageWrapper>
    );
}
