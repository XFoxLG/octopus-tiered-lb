import assert from 'node:assert/strict';
import test from 'node:test';

import {
    buildOctopusAIRoutePreset,
    getAIRouteConfigurationIssues,
    getAIRouteModelAliases,
    inspectAIRouteServices,
    type AIRouteConfiguration,
} from './airoute-config.ts';

const defaultPresetOptions = {
    publicBaseURL: '',
    consoleAPIBaseURL: '.',
    browserOrigin: 'https://console.example',
    modelAlias: 'analysis-group',
};

const configuredService: AIRouteConfiguration = {
    groupID: '0',
    baseURL: 'https://provider.example/custom/v2',
    apiKey: 'test-only-key',
    model: 'external-analysis-model',
    timeoutSeconds: '180',
    parallelism: '3',
    servicesJSON: '[]',
};

test('self API preset uses origin/v1 and requires an explicitly supplied client key', () => {
    const preset = buildOctopusAIRoutePreset(defaultPresetOptions);
    assert.deepEqual(preset, {
        baseURL: 'https://console.example/v1',
        apiKey: '',
        model: 'analysis-group',
    });
    assert.deepEqual(getAIRouteConfigurationIssues({ ...configuredService, ...preset }), ['apiKey']);
});

test('self API preset prefers the configured public URL and preserves deployment prefixes', () => {
    for (const publicBaseURL of [' https://public.example/octopus/ ', 'https://public.example/octopus/v1/']) {
        const preset = buildOctopusAIRoutePreset({
            ...defaultPresetOptions,
            publicBaseURL,
            consoleAPIBaseURL: 'https://backend.example',
        });
        assert.equal(preset?.baseURL, 'https://public.example/octopus/v1');
    }
});

test('self API preset respects a separate console backend or relative proxy prefix', () => {
    const separateBackend = buildOctopusAIRoutePreset({
        ...defaultPresetOptions,
        consoleAPIBaseURL: 'http://localhost:8080',
    });
    assert.equal(separateBackend?.baseURL, 'http://localhost:8080/v1');
    const relativeBackend = buildOctopusAIRoutePreset({
        ...defaultPresetOptions,
        consoleAPIBaseURL: '/octopus',
    });
    assert.equal(relativeBackend?.baseURL, 'https://console.example/octopus/v1');
});

test('self API preset never guesses past an invalid or credential-bearing public address', () => {
    for (const publicBaseURL of [
        'not-a-url',
        '/relative',
        'https:public.example',
        'file:///api',
        'https://user:password@public.example',
        'https://public.example?api_key=secret',
        'https://public.example/#settings',
    ]) {
        assert.equal(buildOctopusAIRoutePreset({ ...defaultPresetOptions, publicBaseURL }), null);
    }
});

test('analysis model choices use distinct populated conversational group aliases, not raw members', () => {
    const groups = [
        { name: 'analysis-group', endpoint_type: 'chat', items: ['raw-provider-model'] },
        { name: 'analysis-group', endpoint_type: 'responses', items: ['another-provider-model'] },
        { name: 'claude-alias', endpoint_type: 'messages', items: ['claude-model'] },
        { name: 'deepseek-alias', endpoint_type: 'deepseek', items: ['deepseek-model'] },
        { name: 'mimo-alias', endpoint_type: 'mimo', items: ['mimo-model'] },
        { name: 'legacy-alias', items: ['legacy-model'] },
        { name: 'embedding-group', endpoint_type: 'embeddings', items: ['embedding-model'] },
        { name: 'image-group', endpoint_type: 'image_generation', items: ['image-model'] },
        { name: 'unclassified-group', endpoint_type: '*', items: ['embedding-model'] },
        { name: 'empty-group', endpoint_type: 'chat', items: [] },
    ];
    assert.deepEqual(getAIRouteModelAliases(groups), [
        'analysis-group', 'claude-alias', 'deepseek-alias', 'legacy-alias', 'mimo-alias',
    ]);
});

test('empty advanced service arrays normalize to the backend single-service sentinel', () => {
    for (const raw of ['', '[]', ' [ \n ] ']) {
        assert.deepEqual(inspectAIRouteServices(raw), {
            normalizedJSON: '[]', usesServicePool: false, valid: true,
        });
    }
    assert.deepEqual(getAIRouteConfigurationIssues(configuredService), []);
    assert.deepEqual(getAIRouteConfigurationIssues({ ...configuredService, apiKey: '', model: '' }), ['apiKey', 'model']);
});

test('external service pool configuration is retained verbatim and takes precedence over single fields', () => {
    const servicesJSON = '[\n {"base_url":"https://provider.example/custom/deployment?api-version=2026-01-01","api_key":"test-only-key","model":"provider-model","enabled":true}\n]';
    assert.deepEqual(inspectAIRouteServices(servicesJSON), {
        normalizedJSON: servicesJSON, usesServicePool: true, valid: true,
    });
    assert.deepEqual(getAIRouteConfigurationIssues({
        ...configuredService, baseURL: '', apiKey: '', model: '', servicesJSON,
    }), []);
});

test('optional service names and enabled flags accept null like the backend decoder', () => {
    const servicesJSON = JSON.stringify([{
        base_url: 'https://provider.example/v1',
        api_key: 'test-only-key',
        model: 'analysis',
        enabled: null,
        name: null,
    }]);
    assert.equal(inspectAIRouteServices(servicesJSON).valid, true);
});

test('incomplete or all-disabled service pools cannot appear ready', () => {
    const service = { base_url: 'https://provider.example/v1', api_key: 'test-only-key', model: 'analysis' };
    for (const raw of [
        '{invalid', '{}', 'null', '[null]', '[{}]',
        JSON.stringify([{ ...service, enabled: false }]),
        JSON.stringify([{ ...service, api_key: '' }]),
        JSON.stringify([{ ...service, enabled: 'true' }]),
        JSON.stringify([{ ...service, base_url: '/v1' }]),
    ]) {
        assert.equal(inspectAIRouteServices(raw).valid, false, raw);
        assert.deepEqual(getAIRouteConfigurationIssues({ ...configuredService, servicesJSON: raw }), ['servicesJSON']);
    }
});

test('completeness requires an HTTP address and positive whole-number limits, but no default target', () => {
    assert.deepEqual(getAIRouteConfigurationIssues(configuredService), []);
    assert.deepEqual(getAIRouteConfigurationIssues({
        ...configuredService, baseURL: '/v1', timeoutSeconds: '0', parallelism: '1.5',
    }), ['baseURL', 'timeoutSeconds', 'parallelism']);
});
