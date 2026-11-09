'use client';

import { Suspense, lazy } from 'react';
import { Activity } from './activity';
import { NonChatDaily } from './nonchat-daily';
import { HomeHero } from './hero';
import { HomeAnalyticsOverview } from './analytics-overview';
import { Rank } from './rank';
import { PageWrapper } from '@/components/common/PageWrapper';

// StatsChart 拉入了整个 recharts（数百 KB）。home 是默认首页路由，若同步引入
// 会把 recharts 压进首屏关键路径。改为懒加载，让 recharts 进入独立 chunk，
// 在首屏其余内容渲染后再加载图表。
const StatsChart = lazy(() => import('./chart').then((m) => ({ default: m.StatsChart })));

// ChartSkeleton 在图表 chunk 加载期间占位，保持与图表区域相近的高度避免布局跳动。
function ChartSkeleton() {
    return (
        <div className="relative rounded-xl border border-border bg-card pt-5">
            <div className="mx-3 mb-3 h-[20rem] animate-pulse rounded-lg bg-muted/40 md:h-[24rem]" />
        </div>
    );
}

export function Home() {
    return (
        <PageWrapper className="h-full min-h-0 overflow-y-auto overscroll-contain space-y-4 md:space-y-5 xl:space-y-6 rounded-t-xl pb-3 md:pb-4">
            <HomeHero />
            <HomeAnalyticsOverview />
            <NonChatDaily />
            <Suspense fallback={<ChartSkeleton />}>
                <StatsChart />
            </Suspense>
            <div className="grid items-start gap-4 md:gap-5 xl:grid-cols-[auto_minmax(320px,1fr)]">
                <div className="min-w-0">
                    <Activity />
                </div>
                <div className="min-w-0">
                    <Rank />
                </div>
            </div>
        </PageWrapper>
    );
}
