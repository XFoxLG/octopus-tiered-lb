'use client';

import { useState, useEffect } from 'react';
import { useTranslations } from 'next-intl';
import { PageWrapper } from '@/components/common/PageWrapper';
import { Tabs, TabsContents, TabsContent, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { Maintenance } from './Maintenance';
import { Audit } from './Audit';
import { useSubTabStore, type OpsTab } from '@/components/modules/navbar/sub-tab-store';

const TAB_LABEL_KEY: Record<OpsTab, string> = {
    maintenance: 'tabs.maintenance',
    audit: 'tabs.audit',
};

export function Ops() {
    const t = useTranslations('ops');
    const { orderedTabs, visibleTabs } = useSubTabStore((s) => s.ops);
    // 默认显示显示顺序中的第一个子标签（用户可在外观设置中拖拽排序/隐藏）
    const [activeTab, setActiveTab] = useState<OpsTab>(() => (visibleTabs[0] as OpsTab) ?? 'maintenance');

    // 当可见列表变化且当前 tab 被隐藏时，回退到第一个可见 tab
    useEffect(() => {
        if (visibleTabs.length > 0 && !visibleTabs.includes(activeTab)) {
            setActiveTab(visibleTabs[0] as OpsTab);
        }
    }, [visibleTabs, activeTab]);

    const visibleSet = new Set(visibleTabs);
    const orderedVisible = orderedTabs.filter((tab) => visibleSet.has(tab));

    return (
        <PageWrapper className="h-full min-h-0 overflow-y-auto overscroll-contain space-y-6 pb-3 md:pb-4 rounded-t-xl">
            <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as OpsTab)}>
                <section className="rounded-xl border border-border bg-card p-5 text-card-foreground">
                    <div className="-mx-5 overflow-x-auto px-5 scrollbar-hide">
                        <TabsList className="w-max min-w-full xl:min-w-0">
                            {orderedVisible.map((tab) => (
                                <TabsTrigger key={tab} value={tab}>
                                    {t(TAB_LABEL_KEY[tab as OpsTab])}
                                </TabsTrigger>
                            ))}
                        </TabsList>
                    </div>
                </section>

                <TabsContents>
                    <TabsContent value="maintenance">
                        <Maintenance />
                    </TabsContent>
                    <TabsContent value="audit">
                        <Audit />
                    </TabsContent>
                </TabsContents>
            </Tabs>
        </PageWrapper>
    );
}
