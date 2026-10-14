package tokenizer

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestEncodingFamilyForModel(t *testing.T) {
	cases := []struct {
		model string
		want  encodingFamily
	}{
		{"gpt-4o", familyO200k},
		{"gpt-4o-2024-08-06", familyO200k},
		{"o1-preview", familyO200k},
		{"o3-mini", familyO200k},
		{"chatgpt-4o-latest", familyO200k},
		{"GPT-4", familyCl100k},
		{"gpt-3.5-turbo", familyCl100k},
		{"text-embedding-3-small", familyCl100k},
		{"text-davinci-edit-001", familyP50kEdit},
		{"code-davinci-edit-001", familyP50kEdit},
		{"davinci", familyR50k},
		{"text-search-davinci-doc-001", familyR50k},
		{"code-search-ada-code-001", familyR50k},
		{"code-davinci-002", familyP50k},
		{"  GPT-4o  ", familyO200k},
		{"", familyO200k},
		{"gemini-3.1-pro", familyO200k},
		{"claude-sonnet-4", familyO200k},
	}
	for _, tc := range cases {
		if got := encodingFamilyForModel(tc.model); got != tc.want {
			t.Fatalf("encodingFamilyForModel(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

func TestCodecCacheReusesInstancesPerFamily(t *testing.T) {
	if codecForModel("gpt-4o") != codecForModel("gpt-4o-mini") {
		t.Fatal("o200k models must share one cached codec")
	}
	if codecForModel("gpt-4o") != codecForModel("unlisted-model-family") {
		t.Fatal("unknown models must reuse the default o200k codec")
	}
	if codecForModel("gpt-4") == codecForModel("gpt-4o") {
		t.Fatal("cl100k and o200k must not share a codec")
	}
	if codecForModel("gpt-4") != codecForModel("gpt-3.5-turbo") {
		t.Fatal("cl100k models must share one cached codec")
	}
}

// TestCountTokensIsStableUnderConcurrency 既是行为回归，也是 codec 复用的
// 数据竞争用例（CI 的 race 步骤覆盖本包）。
func TestCountTokensIsStableUnderConcurrency(t *testing.T) {
	texts := []string{
		"hello world",
		strings.Repeat("hello world, 你好世界 ", 40),
		"",
	}
	models := []string{"gpt-4o", "gpt-4", "gemini-3.1-pro"}
	want := make([][]int, len(models))
	for mi, model := range models {
		want[mi] = make([]int, len(texts))
		for ti, text := range texts {
			want[mi][ti] = CountTokens(text, model)
		}
	}
	if want[0][0] <= 0 || want[0][1] <= 0 {
		t.Fatalf("unexpected baseline counts: %v", want)
	}
	if want[0][2] != 0 {
		t.Fatalf("empty text should count 0 tokens, got %d", want[0][2])
	}

	var wg sync.WaitGroup
	failures := make(chan string, 64)
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			mi := worker % len(models)
			for ti, text := range texts {
				if got := CountTokens(text, models[mi]); got != want[mi][ti] {
					failures <- fmt.Sprintf("worker %d model %s text %d: got %d, want %d", worker, models[mi], ti, got, want[mi][ti])
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
