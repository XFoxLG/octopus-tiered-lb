package model

import "testing"

func TestRequestFilterSettingsValidation(testContext *testing.T) {
	testCases := []struct {
		key       SettingKey
		value     string
		wantError bool
	}{
		{SettingKeyRequestFilterEnabled, "true", false},
		{SettingKeyRequestFilterEnabled, "false", false},
		{SettingKeyRequestFilterEnabled, "yes", true},
		{SettingKeyRequestFilterKeywords, `[]`, false},
		{SettingKeyRequestFilterKeywords, `["health probe"]`, false},
		{SettingKeyRequestFilterKeywords, `null`, true},
		{SettingKeyRequestFilterKeywords, `[null]`, true},
		{SettingKeyRequestFilterKeywords, `[42]`, true},
		{SettingKeyRequestFilterKeywords, `[""]`, true},
		{SettingKeyRequestFilterKeywords, `["  "]`, true},
		{SettingKeyRequestFilterErrorMessage, "", false},
	}
	for _, testCase := range testCases {
		testContext.Run(string(testCase.key)+"/"+testCase.value, func(testContext *testing.T) {
			setting := Setting{Key: testCase.key, Value: testCase.value}
			if err := setting.Validate(); (err != nil) != testCase.wantError {
				testContext.Fatalf("validation error = %v, wantError = %t", err, testCase.wantError)
			}
		})
	}
}
