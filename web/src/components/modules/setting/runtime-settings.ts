export interface RuntimeSettingField {
    key: string;
    labelKey: string;
    placeholderKey: string;
    hintKey?: string;
    min: string;
    max?: string;
}

export const RETRY_FIELDS: RuntimeSettingField[] = [
    {
        key: 'relay_retry_count',
        labelKey: 'retry.count.label',
        hintKey: 'retry.count.hint',
        placeholderKey: 'retry.count.placeholder',
        min: '0',
    },
    {
        key: 'relay_route_retries',
        labelKey: 'retry.routeRetries.label',
        hintKey: 'retry.routeRetries.hint',
        placeholderKey: 'retry.routeRetries.placeholder',
        min: '1',
    },
    {
        key: 'relay_max_total_attempts',
        labelKey: 'retry.maxTotalAttempts.label',
        placeholderKey: 'retry.maxTotalAttempts.placeholder',
        hintKey: 'retry.maxTotalAttempts.hint',
        min: '0',
    },
    {
        key: 'ratelimit_cooldown',
        labelKey: 'retry.ratelimitCooldown.label',
        placeholderKey: 'retry.ratelimitCooldown.placeholder',
        hintKey: 'retry.ratelimitCooldown.hint',
        min: '0',
    },
    {
        key: 'auth_error_cooldown',
        labelKey: 'retry.authErrorCooldown.label',
        placeholderKey: 'retry.authErrorCooldown.placeholder',
        hintKey: 'retry.authErrorCooldown.hint',
        min: '0',
    },
    {
        key: 'server_error_cooldown',
        labelKey: 'retry.serverErrorCooldown.label',
        placeholderKey: 'retry.serverErrorCooldown.placeholder',
        hintKey: 'retry.serverErrorCooldown.hint',
        min: '0',
    },
    {
        key: 'rate_limit_channel_threshold',
        labelKey: 'retry.rateLimitChannel.threshold.label',
        placeholderKey: 'retry.rateLimitChannel.threshold.placeholder',
        hintKey: 'retry.rateLimitChannel.threshold.hint',
        min: '1',
    },
    {
        key: 'rate_limit_channel_window',
        labelKey: 'retry.rateLimitChannel.window.label',
        placeholderKey: 'retry.rateLimitChannel.window.placeholder',
        hintKey: 'retry.rateLimitChannel.window.hint',
        min: '1',
    },
    {
        key: 'rate_limit_channel_cooldown',
        labelKey: 'retry.rateLimitChannel.cooldown.label',
        placeholderKey: 'retry.rateLimitChannel.cooldown.placeholder',
        hintKey: 'retry.rateLimitChannel.cooldown.hint',
        min: '1',
    },
    {
        key: 'rate_limit_hold_interval',
        labelKey: 'retry.rateLimitHold.interval.label',
        placeholderKey: 'retry.rateLimitHold.interval.placeholder',
        hintKey: 'retry.rateLimitHold.interval.hint',
        min: '1',
    },
    {
        key: 'rate_limit_hold_max_wait',
        labelKey: 'retry.rateLimitHold.maxWait.label',
        placeholderKey: 'retry.rateLimitHold.maxWait.placeholder',
        hintKey: 'retry.rateLimitHold.maxWait.hint',
        min: '1',
    },
];

export const AUTO_STRATEGY_FIELDS: RuntimeSettingField[] = [
    {
        key: 'auto_strategy_min_samples',
        labelKey: 'autoStrategy.minSamples.label',
        placeholderKey: 'autoStrategy.minSamples.placeholder',
        hintKey: 'autoStrategy.minSamples.hint',
        min: '1',
    },
    {
        key: 'auto_strategy_time_window',
        labelKey: 'autoStrategy.timeWindow.label',
        placeholderKey: 'autoStrategy.timeWindow.placeholder',
        hintKey: 'autoStrategy.timeWindow.hint',
        min: '1',
    },
    {
        key: 'auto_strategy_sample_threshold',
        labelKey: 'autoStrategy.sampleThreshold.label',
        placeholderKey: 'autoStrategy.sampleThreshold.placeholder',
        hintKey: 'autoStrategy.sampleThreshold.hint',
        min: '1',
    },
    {
        key: 'auto_strategy_latency_weight',
        labelKey: 'autoStrategy.latencyWeight.label',
        placeholderKey: 'autoStrategy.latencyWeight.placeholder',
        hintKey: 'autoStrategy.latencyWeight.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_strategy_ttft_weight',
        labelKey: 'autoStrategy.ttftWeight.label',
        placeholderKey: 'autoStrategy.ttftWeight.placeholder',
        hintKey: 'autoStrategy.ttftWeight.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_strategy_price_weight',
        labelKey: 'autoStrategy.priceWeight.label',
        placeholderKey: 'autoStrategy.priceWeight.placeholder',
        hintKey: 'autoStrategy.priceWeight.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_strategy_explore_rate',
        labelKey: 'autoStrategy.exploreRate.label',
        placeholderKey: 'autoStrategy.exploreRate.placeholder',
        hintKey: 'autoStrategy.exploreRate.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_strategy_bucket_tolerance',
        labelKey: 'autoStrategy.bucketTolerance.label',
        placeholderKey: 'autoStrategy.bucketTolerance.placeholder',
        hintKey: 'autoStrategy.bucketTolerance.hint',
        min: '0',
        max: '100',
    },
];

// v2.9.0：Auto 软信号罚分与先验（百分点，0-100）。截断默认 0 = 只记录不罚分；
// 各罚分累加后受 cap 约束，样本不足时使用中性先验，避免冷启动误判。
export const AUTO_SOFT_SIGNAL_FIELDS: RuntimeSettingField[] = [
    {
        key: 'auto_soft_penalty_fake_stream',
        labelKey: 'autoStrategy.softSignals.fakeStream.label',
        placeholderKey: 'autoStrategy.softSignals.fakeStream.placeholder',
        hintKey: 'autoStrategy.softSignals.fakeStream.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_soft_penalty_empty_output',
        labelKey: 'autoStrategy.softSignals.emptyOutput.label',
        placeholderKey: 'autoStrategy.softSignals.emptyOutput.placeholder',
        hintKey: 'autoStrategy.softSignals.emptyOutput.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_soft_penalty_missing_terminal',
        labelKey: 'autoStrategy.softSignals.missingTerminal.label',
        placeholderKey: 'autoStrategy.softSignals.missingTerminal.placeholder',
        hintKey: 'autoStrategy.softSignals.missingTerminal.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_soft_penalty_truncated',
        labelKey: 'autoStrategy.softSignals.truncated.label',
        placeholderKey: 'autoStrategy.softSignals.truncated.placeholder',
        hintKey: 'autoStrategy.softSignals.truncated.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_soft_penalty_cap',
        labelKey: 'autoStrategy.softSignals.cap.label',
        placeholderKey: 'autoStrategy.softSignals.cap.placeholder',
        hintKey: 'autoStrategy.softSignals.cap.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_soft_neutral_prior',
        labelKey: 'autoStrategy.softSignals.neutralPrior.label',
        placeholderKey: 'autoStrategy.softSignals.neutralPrior.placeholder',
        hintKey: 'autoStrategy.softSignals.neutralPrior.hint',
        min: '0',
        max: '100',
    },
    {
        key: 'auto_dead_channel_penalty',
        labelKey: 'autoStrategy.softSignals.deadChannelPenalty.label',
        placeholderKey: 'autoStrategy.softSignals.deadChannelPenalty.placeholder',
        hintKey: 'autoStrategy.softSignals.deadChannelPenalty.hint',
        min: '0',
        max: '100',
    },
];

// v2.9.0：请求前上下文预检阈值（百分比；估算输入达到已知上限的该比例时告警）。
export const CONTEXT_PREFLIGHT_FIELDS: RuntimeSettingField[] = [
    {
        key: 'relay_context_warn_threshold',
        labelKey: 'autoStrategy.contextGuard.warnThreshold.label',
        placeholderKey: 'autoStrategy.contextGuard.warnThreshold.placeholder',
        hintKey: 'autoStrategy.contextGuard.warnThreshold.hint',
        min: '1',
        max: '100',
    },
];

// v2.9.0：上限学习触发阈值（同一渠道×模型累计多少次独立超限后写入学习值）。
export const CONTEXT_LIMIT_LEARN_FIELDS: RuntimeSettingField[] = [
    {
        key: 'channel_model_limit_learn_threshold',
        labelKey: 'autoStrategy.limitLearn.threshold.label',
        placeholderKey: 'autoStrategy.limitLearn.threshold.placeholder',
        hintKey: 'autoStrategy.limitLearn.threshold.hint',
        min: '1',
    },
];
