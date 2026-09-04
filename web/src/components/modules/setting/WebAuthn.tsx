'use client';

import { useEffect, useState } from 'react';
import { Fingerprint, Globe, Server, Tag } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { SettingKey, useSetSetting, useSettingList } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import { Input } from '@/components/ui/input';
import { Hint } from '@/components/ui/hint';
import { Button } from '@/components/ui/button';

const fieldKey = (settings: { key: string; value: string }[] | undefined, key: string, fallback: string) =>
    settings?.find((item) => item.key === key)?.value ?? fallback;

export function SettingWebAuthn() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [rpID, setRpID] = useState('');
    const [rpName, setRpName] = useState('Octopus');
    const [origins, setOrigins] = useState('');
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        if (loaded || !settings) return;
        setRpID(fieldKey(settings, SettingKey.WebAuthnRPID, ''));
        setRpName(fieldKey(settings, SettingKey.WebAuthnRPName, 'Octopus'));
        setOrigins(fieldKey(settings, SettingKey.WebAuthnOrigins, ''));
        setLoaded(true);
    }, [settings, loaded]);

    const handleSave = async () => {
        const trimmedID = rpID.trim();
        const trimmedOrigins = origins.split(/[,\n]/).map((s) => s.trim()).filter(Boolean);
        if (trimmedOrigins.length > 0 && !trimmedID) {
            toast.error(t('webauthn.needRpid'));
            return;
        }
        try {
            await setSetting.mutateAsync({ key: SettingKey.WebAuthnRPID, value: trimmedID });
            await setSetting.mutateAsync({ key: SettingKey.WebAuthnRPName, value: rpName.trim() || 'Octopus' });
            await setSetting.mutateAsync({ key: SettingKey.WebAuthnOrigins, value: trimmedOrigins.join(', ') });
            toast.success(t('webauthn.saved'));
        } catch {
            toast.error(t('webauthn.saveFailed'));
        }
    };

    return (
        <div className="relative overflow-hidden rounded-xl border-border/35 bg-card p-4 sm:p-6 text-card-foreground shadow-md">
            <div className="space-y-4 sm:space-y-5">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="space-y-1.5">
                        <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                            <Fingerprint className="h-5 w-5" />
                            {t('webauthn.title')}
                            <Hint text={t('webauthn.hint')} />
                        </h2>
                        <p className="text-sm text-muted-foreground">{t('webauthn.description')}</p>
                    </div>
                </div>

                <div className="space-y-4 rounded-xl border border-border/30 bg-muted/10 p-4">
                    <div className="space-y-1.5">
                        <label className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
                            <Globe className="size-3.5" />
                            {t('webauthn.rpId')}
                            <Hint text={t('webauthn.rpIdHint')} />
                        </label>
                        <Input
                            value={rpID}
                            onChange={(e) => setRpID(e.target.value)}
                            placeholder="example.com"
                            className="rounded-lg"
                            autoComplete="off"
                            spellCheck={false}
                        />
                    </div>

                    <div className="space-y-1.5">
                        <label className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
                            <Tag className="size-3.5" />
                            {t('webauthn.rpName')}
                        </label>
                        <Input
                            value={rpName}
                            onChange={(e) => setRpName(e.target.value)}
                            placeholder="Octopus"
                            className="rounded-lg"
                            autoComplete="off"
                        />
                    </div>

                    <div className="space-y-1.5">
                        <label className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
                            <Server className="size-3.5" />
                            {t('webauthn.origins')}
                            <Hint text={t('webauthn.originsHint')} />
                        </label>
                        <Input
                            value={origins}
                            onChange={(e) => setOrigins(e.target.value)}
                            placeholder="https://example.com"
                            className="rounded-lg"
                            autoComplete="off"
                            spellCheck={false}
                        />
                    </div>

                    <div className="flex justify-end">
                        <Button onClick={handleSave} disabled={setSetting.isPending} className="rounded-lg min-w-32">
                            {setSetting.isPending ? t('account.saving') : t('account.save')}
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}
