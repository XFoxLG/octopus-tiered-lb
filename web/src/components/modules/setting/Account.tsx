'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { User, KeyRound, Lock, Eye, EyeOff, LogOut, Fingerprint, Trash2, Plus, ShieldAlert } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Hint } from '@/components/ui/hint';
import { Button } from '@/components/ui/button';
import { useChangeUsername, useChangePassword, useAuth } from '@/api/endpoints/user';
import {
    useWebAuthnCredentials,
    useRegisterPasskey,
    useDeletePasskey,
    isWebAuthnSupported,
    isSecureContextForWebAuthn,
} from '@/api/endpoints/webauthn';
import { toast } from '@/components/common/Toast';

const LOGOUT_DELAY_MS = 1000;

export function SettingAccount() {
    const t = useTranslations('setting');
    const { logout } = useAuth();
    const changeUsername = useChangeUsername();
    const changePassword = useChangePassword();
    const webauthnCreds = useWebAuthnCredentials();
    const registerPasskey = useRegisterPasskey();
    const deletePasskey = useDeletePasskey();

    const webauthnAvailable = isWebAuthnSupported();
    const secureContext = isSecureContextForWebAuthn();
    const passkeyBlocked = !webauthnAvailable || !secureContext;

    const [newUsername, setNewUsername] = useState('');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [confirmPassword, setConfirmPassword] = useState('');
    const [passkeyName, setPasskeyName] = useState('');

    const [showOldPassword, setShowOldPassword] = useState(false);
    const [showNewPassword, setShowNewPassword] = useState(false);
    const [showConfirmPassword, setShowConfirmPassword] = useState(false);

    const handleChangeUsername = () => {
        if (!newUsername.trim()) {
            toast.error(t('account.username.empty'));
            return;
        }

        changeUsername.mutate(
            { newUsername: newUsername.trim() },
            {
                onSuccess: () => {
                    toast.success(t('account.username.success'));
                    setTimeout(() => logout(), LOGOUT_DELAY_MS);
                },
                onError: () => {
                    toast.error(t('account.username.failed'));
                },
            }
        );
    };

    const handleChangePassword = () => {
        if (!oldPassword) {
            toast.error(t('account.password.oldEmpty'));
            return;
        }
        if (!newPassword) {
            toast.error(t('account.password.newEmpty'));
            return;
        }
        if (newPassword !== confirmPassword) {
            toast.error(t('account.password.mismatch'));
            return;
        }
        if (newPassword.length < 6) {
            toast.error(t('account.password.tooShort'));
            return;
        }

        changePassword.mutate(
            { oldPassword, newPassword },
            {
                onSuccess: () => {
                    toast.success(t('account.password.success'));
                    setTimeout(() => logout(), LOGOUT_DELAY_MS);
                },
                onError: () => {
                    toast.error(t('account.password.failed'));
                },
            }
        );
    };

    const handleRegisterPasskey = () => {
        registerPasskey.mutate(passkeyName.trim(), {
            onSuccess: () => {
                setPasskeyName('');
                toast.success(t('account.passkey.addSuccess'));
            },
            onError: (error) => {
                const detail = (error instanceof Error && error.message) ? error.message : '';
                toast.error(detail ? `${t('account.passkey.addFailed')}: ${detail}` : t('account.passkey.addFailed'));
            },
        });
    };

    const handleDeletePasskey = (id: number) => {
        deletePasskey.mutate(id, {
            onSuccess: () => toast.success(t('account.passkey.deleteSuccess')),
            onError: (error) => {
                const detail = (error instanceof Error && error.message) ? error.message : '';
                toast.error(detail ? `${t('account.passkey.deleteFailed')}: ${detail}` : t('account.passkey.deleteFailed'));
            },
        });
    };

    const sectionClassName = 'relative overflow-hidden rounded-xl border border-border/30 bg-card p-5 shadow-sm';

    return (
        <div className="relative overflow-hidden rounded-xl border-border/35 bg-card p-4 sm:p-6 text-card-foreground shadow-md ">
            <div className="space-y-4 sm:space-y-5">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="space-y-1.5">
                        <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                            <User className="h-5 w-5" />
                            {t('account.title')}
                        </h2>
                        <p className="text-sm text-muted-foreground">{t('account.logout.label')}</p>
                    </div>
                </div>

                <div className={sectionClassName}>
                    <div className="mb-4 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
                        <div className="flex items-start gap-3">
                            <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/12 text-xs font-semibold text-primary shadow-sm">
                                01
                            </span>
                            <div className="space-y-1 min-w-0">
                                <div className="flex items-center gap-2 text-sm font-medium text-card-foreground">
                                    <KeyRound className="size-4 text-muted-foreground shrink-0" />
                                    <span className="truncate">{t('account.username.label')}</span>
                                </div>
                                <p className="text-xs text-muted-foreground">{t('account.username.placeholder')}</p>
                            </div>
                        </div>
                        <Button
                            onClick={handleChangeUsername}
                            disabled={changeUsername.isPending || !newUsername.trim()}
                            className="hidden rounded-lg lg:inline-flex"
                        >
                            {changeUsername.isPending ? t('account.saving') : t('account.save')}
                        </Button>
                    </div>

                    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto]">
                        <Input
                            value={newUsername}
                            onChange={(e) => setNewUsername(e.target.value)}
                            placeholder={t('account.username.placeholder')}
                            className="rounded-lg w-full"
                        />
                        <Button
                            onClick={handleChangeUsername}
                            disabled={changeUsername.isPending || !newUsername.trim()}
                            className="rounded-lg w-full lg:hidden"
                        >
                            {changeUsername.isPending ? t('account.saving') : t('account.save')}
                        </Button>
                    </div>
                </div>

                <div className={sectionClassName}>
                    <div className="mb-4 flex items-start gap-3">
                        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/12 text-xs font-semibold text-primary shadow-sm">
                            02
                        </span>
                        <div className="space-y-1 min-w-0">
                            <div className="flex items-center gap-2 text-sm font-medium text-card-foreground">
                                <Lock className="size-4 text-muted-foreground shrink-0" />
                                <span className="truncate">{t('account.password.label')}</span>
                            </div>
                            <p className="text-xs text-muted-foreground">{t('account.password.change')}</p>
                        </div>
                    </div>

                    <div className="grid gap-3 sm:grid-cols-2">
                        <div className="relative sm:col-span-2">
                            <Input
                                type={showOldPassword ? 'text' : 'password'}
                                value={oldPassword}
                                onChange={(e) => setOldPassword(e.target.value)}
                                placeholder={t('account.password.oldPlaceholder')}
                                className="rounded-lg pr-10 w-full"
                            />
                            <button
                                type="button"
                                onClick={() => setShowOldPassword(!showOldPassword)}
                                aria-label={showOldPassword ? t('account.password.hideOld') : t('account.password.showOld')}
                                aria-pressed={showOldPassword}
                                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                            >
                                {showOldPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                            </button>
                        </div>
                        <div className="relative">
                            <Input
                                type={showNewPassword ? 'text' : 'password'}
                                value={newPassword}
                                onChange={(e) => setNewPassword(e.target.value)}
                                placeholder={t('account.password.newPlaceholder')}
                                className="rounded-lg pr-10 w-full"
                            />
                            <button
                                type="button"
                                onClick={() => setShowNewPassword(!showNewPassword)}
                                aria-label={showNewPassword ? t('account.password.hideNew') : t('account.password.showNew')}
                                aria-pressed={showNewPassword}
                                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                            >
                                {showNewPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                            </button>
                        </div>
                        <div className="relative">
                            <Input
                                type={showConfirmPassword ? 'text' : 'password'}
                                value={confirmPassword}
                                onChange={(e) => setConfirmPassword(e.target.value)}
                                placeholder={t('account.password.confirmPlaceholder')}
                                className="rounded-lg pr-10 w-full"
                            />
                            <button
                                type="button"
                                onClick={() => setShowConfirmPassword(!showConfirmPassword)}
                                aria-label={showConfirmPassword ? t('account.password.hideConfirm') : t('account.password.showConfirm')}
                                aria-pressed={showConfirmPassword}
                                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                            >
                                {showConfirmPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                            </button>
                        </div>
                    </div>

                    <div className="mt-4 flex justify-end">
                        <Button
                            onClick={handleChangePassword}
                            disabled={changePassword.isPending || !oldPassword || !newPassword || !confirmPassword}
                            className="w-full rounded-lg sm:w-auto sm:min-w-36"
                        >
                            {changePassword.isPending ? t('account.saving') : t('account.password.change')}
                        </Button>
                    </div>
                </div>

                <div className={sectionClassName}>
                    <div className="mb-4 flex items-start gap-3">
                        <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/12 text-xs font-semibold text-primary shadow-sm">
                            03
                        </span>
                        <div className="space-y-1 min-w-0">
                            <div className="flex items-center gap-2 text-sm font-medium text-card-foreground">
                                <Fingerprint className="size-4 text-muted-foreground shrink-0" />
                                <span className="truncate">{t('account.passkey.title')}</span>
                            </div>
                            <p className="text-xs text-muted-foreground">{t('account.passkey.description')}</p>
                        </div>
                    </div>

                    {webauthnCreds.data && webauthnCreds.data.length > 0 && (
                        <ul className="mb-4 space-y-2">
                            {webauthnCreds.data.map((cred) => (
                                <li
                                    key={cred.id}
                                    className="flex items-center justify-between gap-3 rounded-lg border border-border/30 bg-muted/20 px-3 py-2"
                                >
                                    <div className="min-w-0">
                                        <div className="truncate text-sm font-medium">
                                            {cred.name || t('account.passkey.unnamed')}
                                        </div>
                                        <div className="text-xs text-muted-foreground">
                                            {cred.last_used_at
                                                ? `${t('account.passkey.lastUsed')} ${new Date(cred.last_used_at).toLocaleString()}`
                                                : `${t('account.passkey.created')} ${new Date(cred.created_at).toLocaleString()}`}
                                        </div>
                                    </div>
                                    <Button
                                        variant="ghost"
                                        size="icon"
                                        className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                                        onClick={() => handleDeletePasskey(cred.id)}
                                        disabled={deletePasskey.isPending}
                                        aria-label={t('account.passkey.delete')}
                                    >
                                        <Trash2 className="size-4" />
                                    </Button>
                                </li>
                            ))}
                        </ul>
                    )}

                    {passkeyBlocked && (
                        <div className="mb-4 flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2.5">
                            <ShieldAlert className="size-4 shrink-0 text-amber-600 dark:text-amber-400 mt-0.5" />
                            <p className="text-xs leading-relaxed text-amber-700 dark:text-amber-300">
                                {t('account.passkey.insecureContext')}
                            </p>
                        </div>
                    )}
                    <div className="mb-4 flex items-start gap-2 rounded-lg border border-blue-500/30 bg-blue-500/10 px-3 py-2.5">
                        <ShieldAlert className="size-4 shrink-0 text-blue-600 dark:text-blue-400 mt-0.5" />
                        <div className="text-xs leading-relaxed text-blue-700 dark:text-blue-300">
                            <div className="font-medium mb-1 flex items-center gap-1.5">
                                {t('account.passkey.currentOrigin')}
                                <Hint text={t('account.passkey.originHint')} />
                            </div>
                            <div className="font-mono text-[11px] break-all">
                                {typeof window !== 'undefined' ? window.location.origin : ''}
                            </div>
                        </div>
                    </div>

                    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto]">
                        <Input
                            value={passkeyName}
                            onChange={(e) => setPasskeyName(e.target.value)}
                            placeholder={t('account.passkey.namePlaceholder')}
                            className="rounded-lg w-full"
                            maxLength={64}
                        />
                        <Button
                            onClick={handleRegisterPasskey}
                            disabled={registerPasskey.isPending || passkeyBlocked}
                            className="rounded-lg w-full lg:w-auto"
                        >
                            <Plus className="size-4" />
                            {registerPasskey.isPending ? t('account.passkey.adding') : t('account.passkey.add')}
                        </Button>
                    </div>
                </div>

                <div className="relative overflow-hidden rounded-xl border border-destructive/20 bg-destructive/6 p-4 sm:p-5 shadow-sm">
                    <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                        <div className="flex items-start gap-3">
                            <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-destructive/12 text-xs font-semibold text-destructive shadow-sm">
                                04
                            </span>
                            <div className="space-y-1 min-w-0">
                                <div className="flex items-center gap-2 text-sm font-medium text-card-foreground">
                                    <LogOut className="size-4 text-destructive shrink-0" />
                                    <span className="truncate">{t('account.logout.label')}</span>
                                </div>
                                <p className="text-xs text-muted-foreground">{t('account.logout.button')}</p>
                            </div>
                        </div>
                        <Button
                            variant="destructive"
                            size="sm"
                            onClick={logout}
                            className="w-full rounded-lg sm:w-auto sm:min-w-32"
                        >
                            {t('account.logout.button')}
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}

