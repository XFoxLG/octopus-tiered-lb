import type { TranslationValues } from 'next-intl';
import type { NotificationItem } from '@/api/endpoints/notification';

interface NotificationTranslator {
    (key: string, params?: TranslationValues): string;
    has: (key: string) => boolean;
}

function resolveNotificationText(
    messageKey: string | undefined,
    serializedArguments: string | undefined,
    field: 'title' | 'content',
    fallbackText: string,
    translate: NotificationTranslator,
): string {
    if (!messageKey) return fallbackText;

    const translationKey = `${messageKey}.${field}`;
    try {
        if (!translate.has(translationKey)) return fallbackText;

        let translationArguments: TranslationValues | undefined;
        if (serializedArguments) {
            const parsedArguments: unknown = JSON.parse(serializedArguments);
            if (!parsedArguments || typeof parsedArguments !== 'object' || Array.isArray(parsedArguments)) {
                return fallbackText;
            }
            translationArguments = {};
            for (const [argumentName, argumentValue] of Object.entries(parsedArguments)) {
                if (typeof argumentValue !== 'string' && typeof argumentValue !== 'number') {
                    return fallbackText;
                }
                translationArguments[argumentName] = argumentValue;
            }
        }

        const translatedText = translate(translationKey, translationArguments);
        // next-intl returns a key path instead of throwing for many formatting errors.
        if (!translatedText.trim() || translatedText === translationKey || translatedText === `notif.${translationKey}`) {
            return fallbackText;
        }
        return translatedText;
    } catch {
        return fallbackText;
    }
}

/**
 * 解析通知的本地化标题/正文。
 *
 * 后端为每条通知存储 i18n 键 + 参数（title_key/content_key + *_args JSON），
 * 前端按当前 UI 语言用 t() 渲染。键为空（历史通知）时回退到 title/content 原文。
 *
 * `t` 为 next-intl 的翻译函数，命名空间为 'notif'，按 `${key}.title` / `${key}.content`
 * 取模板。参数对象由 *_args JSON 反序列化得到，直接作为 t() 的第二参数（ICU 插值）。
 */
export function resolveNotifTitle(item: NotificationItem, translate: NotificationTranslator): string {
    return resolveNotificationText(item.title_key, item.title_args, 'title', item.title, translate);
}

export function resolveNotifContent(item: NotificationItem, translate: NotificationTranslator): string {
    return resolveNotificationText(item.content_key, item.content_args, 'content', item.content, translate);
}
