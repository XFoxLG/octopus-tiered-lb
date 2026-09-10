/**
 * 客户端 UA 识别：把 User-Agent 关键词映射为易读的客户端名称。
 * 识别只是"尽力而为"的关键词匹配——认不出时返回空串，调用方应原样展示 UA，
 * 因为 WebView/OkHttp 形态的客户端可能完全不带应用名。
 */
const CLIENT_NAME_RULES: ReadonlyArray<{ keyword: string; label: string }> = [
    { keyword: 'sillytavern', label: '酒馆 SillyTavern' },
    { keyword: 'tavo', label: 'Tavo' },
    { keyword: 'roche', label: 'Roche 小手机' },
    { keyword: 'internalbeyond', label: 'IB 小手机' },
    { keyword: 'ib-phone', label: 'IB 小手机' },
    { keyword: 'rikkahub', label: 'RikkaHub' },
    { keyword: 'orangechat', label: '橘瓣 OrangeChat' },
];

const BROWSER_RULES: ReadonlyArray<{ keyword: string; label: string }> = [
    { keyword: 'edg/', label: 'Edge 浏览器' },
    { keyword: 'firefox', label: 'Firefox 浏览器' },
    { keyword: 'crios', label: 'Chrome 浏览器' },
    { keyword: 'chrome', label: 'Chrome 浏览器' },
    { keyword: 'safari', label: 'Safari 浏览器' },
];

/** 从 UA 解析客户端友好名；无法识别时返回空串。 */
export function resolveClientName(userAgent: string | null | undefined): string {
    const normalized = (userAgent ?? '').toLowerCase();
    if (!normalized) return '';
    for (const rule of CLIENT_NAME_RULES) {
        if (normalized.includes(rule.keyword)) return rule.label;
    }
    // Android WebView（"; wv)"）要排在 Chrome 之前：WebView UA 同时含 "chrome"，
    // 应用内（Roche/IB 等 Capacitor/WebView 形态）应显示为应用内而不是普通浏览器。
    if (normalized.includes('; wv)') || normalized.includes('webview')) {
        return '应用内 WebView';
    }
    for (const rule of BROWSER_RULES) {
        if (normalized.includes(rule.keyword)) return rule.label;
    }
    return '';
}
