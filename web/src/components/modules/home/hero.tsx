'use client';

import { motion } from 'motion/react';
import { Waves } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { useStatsToday } from '@/api/endpoints/stats';
import { useHomeStatsRefreshMs } from './store';
import { StatsRefreshControls } from './refresh-controls';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { EASING } from '@/lib/animations/fluid-transitions';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';

export function HomeHero() {
    const t = useTranslations('home.hero');
    const statsRefreshMs = useHomeStatsRefreshMs();
    const { data: statsToday } = useStatsToday({ refetchIntervalMs: statsRefreshMs });

    const requestCount = (statsToday?.request_success ?? 0) + (statsToday?.request_failed ?? 0);
    const successCount = statsToday?.request_success ?? 0;
    const totalCost = (statsToday?.input_cost ?? 0) + (statsToday?.output_cost ?? 0);
    const totalTokens = (statsToday?.input_token ?? 0) + (statsToday?.output_token ?? 0);
    const totalWaitTime = statsToday?.wait_time ?? 0;
    const successRate = requestCount > 0 ? (successCount / requestCount) * 100 : 0;
    const avgWait = requestCount > 0 ? totalWaitTime / requestCount : 0;

    const callsSuccess = formatCount(successCount).formatted;
    const callsTotal = formatCount(requestCount).formatted;
    const tokens = formatCount(totalTokens).formatted;

    // 2 列 × 2 行：平均响应时延、今日花费、今日调用和今日 Token 使用。
    const cards = [
        {
            key: 'avgWait',
            label: t('metrics.avgWait'),
            value: formatTime(avgWait).formatted.value,
            unit: formatTime(avgWait).formatted.unit,
        },
        {
            key: 'cost',
            label: t('signals.cost'),
            value: formatMoney(totalCost).formatted.value,
            unit: formatMoney(totalCost).formatted.unit,
        },
        {
            key: 'calls',
            label: t('signals.requests'),
            isComposite: true,
            mainValue: callsSuccess.value,
            mainUnit: callsSuccess.unit,
            dividerValue: callsTotal.value,
            dividerUnit: callsTotal.unit,
            rate: successRate.toFixed(2),
            rateLabel: t('signals.successRateShort'),
        },
        {
            key: 'tokens',
            label: t('metrics.tokens'),
            value: tokens.value,
            unit: tokens.unit,
        },
    ];

    return (
        <motion.section
            className="relative rounded-xl border border-border bg-card p-5 text-card-foreground md:p-6 xl:p-7"
            initial={{ opacity: 0, y: 16 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.45, ease: EASING.easeOutExpo }}
        >
            <div className="space-y-5">
                <div className="space-y-3">
                    <div className="flex flex-wrap items-center justify-between gap-3 sm:gap-4">
                        <div className="flex items-center gap-3 sm:gap-4">
                            <div className="grid h-11 w-11 sm:h-14 sm:w-14 shrink-0 place-items-center overflow-hidden rounded-lg border border-border bg-card text-primary">
                                <Waves className="h-5 w-5 sm:h-6 sm:w-6" strokeWidth={1.5} />
                            </div>
                            <div className="space-y-1">
                                <h1 className="text-[1.65rem] font-semibold tracking-tight sm:text-2xl md:text-3xl lg:text-4xl">{t('title')}</h1>
                                {t('subtitle') ? (
                                    <p className="text-sm leading-6 text-muted-foreground md:text-base">{t('subtitle')}</p>
                                ) : null}
                            </div>
                        </div>
                        <StatsRefreshControls />
                    </div>

                    {t('description') ? (
                        <p className="max-w-2xl text-sm leading-7 text-muted-foreground md:text-[15px]">
                            {t('description')}
                        </p>
                    ) : null}
                </div>

                <div className="grid grid-cols-2 gap-2.5 sm:gap-3">
                    {cards.map((card) => (
                        <article
                            key={card.key}
                            className="group rounded-lg border border-border bg-card px-3 py-2.5 transition-colors duration-200 hover:border-border/80 hover:bg-muted/30 sm:px-4 sm:py-3"
                        >
                            <div className="mb-2 h-1 w-10 rounded-full bg-primary/20 transition-all duration-300 group-hover:w-14 group-hover:bg-primary/30" />
                            <div className="text-xs font-medium text-muted-foreground">{card.label}</div>
                            {card.isComposite ? (
                                <div className="mt-1 space-y-0.5">
                                    <div className="flex items-baseline gap-1.5">
                                        <span className="text-xl font-semibold tracking-tight sm:text-2xl">
                                            <AnimatedNumber value={card.mainValue} />
                                            {card.mainUnit ? <span className="text-sm text-muted-foreground">{card.mainUnit}</span> : null}
                                        </span>
                                        <span className="text-sm text-muted-foreground">
                                            / {card.dividerValue}{card.dividerUnit}
                                        </span>
                                    </div>
                                    <div className="text-xs text-muted-foreground">
                                        {card.rateLabel} {card.rate}%
                                    </div>
                                </div>
                            ) : (
                                <div className="mt-1 flex items-baseline gap-1">
                                    <span className="text-xl font-semibold tracking-tight sm:text-2xl">
                                        <AnimatedNumber value={card.value} />
                                    </span>
                                    {card.unit ? <span className="text-sm text-muted-foreground">{card.unit}</span> : null}
                                </div>
                            )}
                        </article>
                    ))}
                </div>
            </div>
        </motion.section>
    );
}
