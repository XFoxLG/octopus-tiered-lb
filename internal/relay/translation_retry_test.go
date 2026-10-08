package relay

import (
	"errors"
	"net/http"
	"testing"

	dbmodel "github.com/lingyuins/octopus/internal/model"
)

var translationCompatibilityErrors = []string{
	`400: {"error":{"message":"upstream: 1 validation error: {'type': 'literal_error', 'loc': ('body', 'reasoning_effort'), 'msg': \"Input should be 'low', 'medium' or 'high'\", 'input': 'minimal'}","type":"upstream_error"}}`,
	`400: {"error":{"message":"upstream: reasoning_effort is not enabled for this model","type":"bad_response_status_code"}}`,
	`400: {"error":{"message":"Unexpected reasoning effort minimal. Supported types are xhigh (default), medium, and low.","type":"BadRequest"}}`,
	`400: {"error":{"message":"Unsupported value: 'reasoning_effort' does not support 'minimal' with this model. Supported values are: 'low', 'medium', 'high'.","param":"reasoning_effort","code":"unsupported_value"}}`,
	`400: {"error":{"message":"Unsupported parameter: 'reasoning_effort' is not supported with this model.","param":"reasoning_effort","code":"unsupported_parameter"}}`,
	`400: {"error":{"message":"[{\"type\":\"extra_forbidden\",\"loc\":[\"body\",\"reasoning_effort\"],\"msg\":\"Extra inputs are not permitted\",\"input\":\"minimal\"}]"}}`,
}

func TestTranslationCompatibilityErrorsTryNextCandidate(t *testing.T) {
	for _, message := range translationCompatibilityErrors {
		t.Run(message, func(t *testing.T) {
			decision := applyErrorPolicy(ClassifyRelayError(400, errors.New(message), false), &dbmodel.Channel{}, 400, message)
			if decision.Scope != ScopeNextChannel || !decision.IsError {
				t.Fatalf("compatible candidate must remain reachable: %+v", decision)
			}
			if shouldRecordChannelFailure(decision) {
				t.Fatal("request-specific reasoning incompatibility must not trip a healthy model's circuit")
			}
		})
	}
	checkpoint := `400: {"error":{"message":"Model 'qwen35-9b-v34-i0400-v1' is an internal development checkpoint and not directly accessible. Please use 'Index-Translate-35B-A3B' or official product model names."}}`
	if got := applyErrorPolicy(ClassifyRelayError(400, errors.New(checkpoint), false), nil, 400, checkpoint); got.Scope != ScopeNextChannel {
		t.Fatalf("inaccessible checkpoint must not stop the route: %+v", got)
	}
}

func TestTranslationCompatibilityPolicyKeepsBoundaries(t *testing.T) {
	for _, message := range []string{
		`400: {"error":{"code":"3051","message":"3051","type":"upstream_error"}}`,
		`400: {"error":{"message":"bad_response_status_code","type":"upstream_error"}}`,
		`400: {"error":{"message":"Invalid messages: Input should be a valid list","input":{"reasoning_effort":"minimal"}}}`,
		`400: {"error":{"message":"reasoning_effort was supplied; messages must be a list"}}`,
		`400: {"error":{"message":"Requests ending with a model turn are not supported."}}`,
	} {
		if got := applyErrorPolicy(ClassifyRelayError(400, errors.New(message), false), nil, 400, message); got.Scope != ScopeNone {
			t.Fatalf("unknown/unrelated client error was broadened: %+v; %s", got, message)
		}
	}
	message := translationCompatibilityErrors[0]
	if got := applyErrorPolicy(ClassifyRelayError(400, errors.New(message), true), nil, 400, message); got.Scope != ScopeAbortAll {
		t.Fatalf("committed response must never retry: %+v", got)
	}
	if got := applyErrorPolicy(ClassifyRelayError(400, errors.New(message), false), &dbmodel.Channel{NonRetryableStatusCodes: "400"}, 400, message); got.Scope != ScopeNone {
		t.Fatalf("explicit channel policy must win: %+v", got)
	}
	if got := applyErrorPolicy(ClassifyRelayError(http.StatusOK, nil, false), nil, http.StatusOK, message); got.IsError {
		t.Fatal("successful response must not be scanned as an error")
	}
}
