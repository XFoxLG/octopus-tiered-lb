package model_test

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestGeminiErrorEnvelopeAcceptsFlexibleCodes(testContext *testing.T) {
	testCases := []struct {
		name          string
		payload       string
		expectedCode  string
		expectedType  string
		expectedHTTP  int
		expectMessage string
	}{
		{
			name:          "native numeric code",
			payload:       `{"error":{"code":503,"message":"fixture upstream unavailable","status":"UNAVAILABLE"}}`,
			expectedCode:  "503",
			expectedType:  "UNAVAILABLE",
			expectMessage: "fixture upstream unavailable",
		},
		{
			name:          "symbolic proxy code",
			payload:       `{"error":{"message":"fixture credential unavailable","type":"pggo_error","code":"no_credential","param":null,"http_status":503}}`,
			expectedCode:  "no_credential",
			expectedType:  "pggo_error",
			expectedHTTP:  503,
			expectMessage: "fixture credential unavailable",
		},
		{
			name:          "null code",
			payload:       `{"error":{"code":null,"message":"fixture failed","status":"INTERNAL"}}`,
			expectedCode:  "",
			expectedType:  "INTERNAL",
			expectMessage: "fixture failed",
		},
		{
			name:          "missing code",
			payload:       `{"error":{"message":"fixture failed","type":"pggo_error"}}`,
			expectedCode:  "",
			expectedType:  "pggo_error",
			expectMessage: "fixture failed",
		},
		{
			name:          "future code shape",
			payload:       `{"error":{"code":{"detail":"fixture"},"message":"fixture failed","type":"pggo_error"}}`,
			expectedCode:  `{"detail":"fixture"}`,
			expectedType:  "pggo_error",
			expectMessage: "fixture failed",
		},
	}

	for _, testCase := range testCases {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			var response model.GeminiGenerateContentResponse
			if err := transformer.Unmarshal([]byte(testCase.payload), &response); err != nil {
				testContext.Fatalf("unmarshal fixture: %v", err)
			}
			if response.Error == nil {
				testContext.Fatal("error envelope was lost")
			}
			if response.Error.Code.String() != testCase.expectedCode {
				testContext.Fatalf("code = %q, want %q", response.Error.Code.String(), testCase.expectedCode)
			}
			if response.Error.Type != testCase.expectedType && response.Error.Status != testCase.expectedType {
				testContext.Fatalf("type/status = %q/%q, want %q", response.Error.Type, response.Error.Status, testCase.expectedType)
			}
			if response.Error.HTTPStatus != testCase.expectedHTTP {
				testContext.Fatalf("http_status = %d, want %d", response.Error.HTTPStatus, testCase.expectedHTTP)
			}
			if response.Error.Message != testCase.expectMessage {
				testContext.Fatalf("message = %q, want %q", response.Error.Message, testCase.expectMessage)
			}
		})
	}
}
