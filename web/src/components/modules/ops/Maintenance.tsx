'use client';

import { useTranslations } from 'next-intl';
import { SettingCircuitBreaker } from '@/components/modules/setting/CircuitBreaker';
import { SettingRetry } from '@/components/modules/setting/Retry';
import { SettingRequestFilter } from '@/components/modules/setting/RequestFilter';

type MaintenanceSectionId = 'retry' | 'circuit-breaker' | 'request-filter';

// 先配置重试策略，再配置熔断器保护阈值，符合用户操作的自然逻辑（见 issue #95 改动4）。
const SECTIONS: MaintenanceSectionId[] = ['retry', 'circuit-breaker', 'request-filter'];

export function Maintenance() {
    const t = useTranslations('ops');

    return (
        <section className="space-y-4">
            <div className="space-y-1 px-1">
                <h3 className="text-base font-semibold">{t('tabs.maintenance')}</h3>
                <p className="text-sm leading-6 text-muted-foreground">{t('maintenance.description')}</p>
            </div>

            <div className="space-y-4">
                {SECTIONS.map((id) => (
                    <article
                        key={id}
                        className="rounded-xl border border-border/35 bg-card p-1 text-card-foreground shadow-sm"
                    >
                        {id === 'circuit-breaker' && <SettingCircuitBreaker />}
                        {id === 'retry' && <SettingRetry />}
                        {id === 'request-filter' && <SettingRequestFilter />}
                    </article>
                ))}
            </div>
        </section>
    );
}
