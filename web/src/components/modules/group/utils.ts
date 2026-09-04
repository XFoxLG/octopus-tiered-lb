// 相对路径导入：本文件被 node --test 直接加载（utils.test.ts），@/ 别名与无扩展名导入在该环境下不可解析；
// GroupMode 所在的 api/endpoints/group.ts 含 enum（node strip-only 模式不支持），
// 故 MODE_LABELS 用与 GroupMode 枚举值一致的字面量键（1-5），不再导入该枚举。
import type { LLMChannel } from '../../../api/endpoints/model.ts';
export {
    ALL_CAPABILITIES,
    CAPABILITY_COLORS,
    CAPABILITY_LABEL_KEYS,
    inferCapabilities,
    inferGroupCapabilities,
    matchesGroupEndpointFilter,
    type CapabilityType,
    type GroupEndpointFilter,
} from './capabilities.ts';

export const MODE_LABELS: Record<number, string> = {
    1: 'roundRobin',
    2: 'random',
    3: 'failover',
    4: 'weighted',
    5: 'auto',
} as const;

export const ENDPOINT_TYPE_OPTIONS = [
    { labelKey: 'form.endpointType.options.chat', value: 'chat' },
    { labelKey: 'form.endpointType.options.embeddings', value: 'embeddings' },
    { labelKey: 'form.endpointType.options.rerank', value: 'rerank' },
    { labelKey: 'form.endpointType.options.moderations', value: 'moderations' },
    { labelKey: 'form.endpointType.options.imageGeneration', value: 'image_generation' },
    { labelKey: 'form.endpointType.options.audioSpeech', value: 'audio_speech' },
    { labelKey: 'form.endpointType.options.audioTranscription', value: 'audio_transcription' },
    { labelKey: 'form.endpointType.options.videoGeneration', value: 'video_generation' },
    { labelKey: 'form.endpointType.options.musicGeneration', value: 'music_generation' },
    { labelKey: 'form.endpointType.options.search', value: 'search' },
] as const;


export const MUSIC_ENDPOINT_PROVIDER_OPTIONS = [
    { label: 'Auto', value: '' },
    { label: 'New API', value: 'newapi' },
    { label: 'MiniMax', value: 'minimax' },
] as const;

export const VIDEO_ENDPOINT_PROVIDER_OPTIONS = [
    { label: 'Auto', value: '' },
    { label: 'Agnes', value: 'agnes' },
] as const;

export const IMAGE_ENDPOINT_PROVIDER_OPTIONS = [
    { label: 'Auto', value: '' },
    { label: 'Agnes', value: 'agnes' },
    { label: '透传（信息体）', value: 'raw' },
] as const;

export const AUDIO_SPEECH_ENDPOINT_PROVIDER_OPTIONS = [
    { label: 'Auto', value: '' },
    { label: 'MiMo', value: 'mimo' },
] as const;

export const CHAT_ENDPOINT_PROVIDER_OPTIONS = [
    { label: 'Auto', value: '' },
    { label: 'OpenAI', value: 'openai' },
    { label: 'DeepSeek', value: 'deepseek' },
    { label: 'MiMo', value: 'mimo' },
] as const;

export const OUTBOUND_FORMAT_OPTIONS = [
    { labelKey: 'form.outboundFormat.options.auto', value: '' },
    { labelKey: 'form.outboundFormat.options.chat', value: 'chat' },
    { labelKey: 'form.outboundFormat.options.responses', value: 'responses' },
    { labelKey: 'form.outboundFormat.options.messages', value: 'messages' },
    { labelKey: 'form.outboundFormat.options.passthrough', value: 'passthrough' },
    { labelKey: 'form.outboundFormat.options.raw', value: 'raw' },
    { labelKey: 'form.outboundFormat.options.chatOnly', value: 'chat_only' },
    { labelKey: 'form.outboundFormat.options.responsesOnly', value: 'responses_only' },
    { labelKey: 'form.outboundFormat.options.messagesOnly', value: 'messages_only' },
] as const;

export function normalizeOutboundFormat(value?: string | null) {
    const normalized = value?.trim().toLowerCase();
    if (normalized === 'chat' || normalized === 'responses' || normalized === 'messages' || normalized === 'passthrough' || normalized === 'raw' || normalized === 'chat_only' || normalized === 'responses_only' || normalized === 'messages_only') return normalized;
    return '';
}

export function normalizeEndpointProvider(value?: string | null) {
    return value?.trim().toLowerCase() || '';
}

export function normalizeEndpointType(value?: string | null) {
    const normalized = value?.trim().toLowerCase();
    if (normalized === 'responses' || normalized === 'messages' || normalized === 'deepseek' || normalized === 'mimo') {
        return 'chat';
    }
    return normalized || 'chat';
}

const CONVERSATION_ENDPOINT_TYPES = new Set(['chat', 'deepseek', 'mimo', 'responses', 'messages']);

export function supportsGroupTest(endpointType?: string | null) {
    return CONVERSATION_ENDPOINT_TYPES.has(normalizeEndpointType(endpointType));
}

export function endpointTypeLabelKey(value?: string | null) {
    const endpointType = normalizeEndpointType(value);
    // 存量/测试日志可能带无分组语义的 '*' endpoint_type，映射为 "全部" 标签
    if (endpointType === '*') return 'form.endpointType.options.all';
    return ENDPOINT_TYPE_OPTIONS.find((option) => option.value === endpointType)?.labelKey;
}

export function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

export function modelChannelKey(channelId: number, modelName: string) {
    return `${channelId}-${modelName}`;
}

export function memberKey(member: Pick<LLMChannel, 'channel_id' | 'name'>) {
    return modelChannelKey(member.channel_id, member.name);
}

export function matchesGroupName(modelName: string, groupKey: string) {
    if (!groupKey) return false;
    return modelName.toLowerCase().includes(groupKey);
}

export function buildChannelNameByModelKey(modelChannels: LLMChannel[]) {
    const map = new Map<string, string>();
    modelChannels.forEach((mc) => {
        map.set(modelChannelKey(mc.channel_id, mc.name), mc.channel_name);
    });
    return map;
}

/**
 * 过滤掉禁用渠道的模型，用于分组编辑器的"添加模型"候选列表，
 * 避免把请求路由到不会启用的渠道。
 */
export function filterEnabledModelChannels(modelChannels: LLMChannel[]) {
    return modelChannels.filter((mc) => mc.enabled !== false);
}
