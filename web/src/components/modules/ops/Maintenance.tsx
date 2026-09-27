'use client';

import { useTranslations } from 'next-intl';
import { SettingCircuitBreaker } from '@/components/modules/setting/CircuitBreaker';
import { SettingRetry } from '@/components/modules/setting/Retry';
import { SettingRequestFilter } from '@/components/modules/setting/RequestFilter';
import { SettingErrorPolicy } from '@/components/modules/setting/ErrorPolicy';

type MaintenanceSectionId = 'retry' | 'error-policy' | 'circuit-breaker' | 'request-filter';

// 常用:日常最常调的重试与错误策略。高级:阈值偏高、改动风险大的熔断器与输入拦截。
// 先配置重试策略,再配置熔断器保护阈值,符合用户操作的自然逻辑(见 issue #95 改动4)。
const COMMON_SECTIONS: MaintenanceSectionId[] = ['retry', 'error-policy'];
const ADVANCED_SECTIONS: MaintenanceSectionId[] = ['circuit-breaker', 'request-filter'];

function MaintenanceSection({ id }: { id: MaintenanceSectionId }) {
    return (
        <article className="rounded-xl border border-border/35 bg-card p-1 text-card-foreground shadow-sm">
            {id === 'circuit-breaker' && <SettingCircuitBreaker />}
            {id === 'retry' && <SettingRetry />}
            {id === 'error-policy' && <SettingErrorPolicy />}
            {id === 'request-filter' && <SettingRequestFilter />}
        </article>
    );
}

export function Maintenance() {
    const t = useTranslations('ops');

    return (
        <section className="space-y-4">
            <div className="space-y-1 px-1">
                <h3 className="text-base font-semibold">{t('tabs.maintenance')}</h3>
                <p className="text-sm leading-6 text-muted-foreground">{t('maintenance.description')}</p>
            </div>

            <div className="space-y-4">
                <p className="px-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t('maintenance.common')}
                </p>
                {COMMON_SECTIONS.map((id) => <MaintenanceSection key={id} id={id} />)}

                <p className="px-1 pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t('maintenance.advanced')}
                </p>
                {ADVANCED_SECTIONS.map((id) => <MaintenanceSection key={id} id={id} />)}
            </div>
        </section>
    );
}