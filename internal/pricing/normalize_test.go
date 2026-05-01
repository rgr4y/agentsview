package pricing

import "testing"

func TestNormalizeModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"Qwen/Qwen3-Coder-Next", "qwen/qwen3-coder-next"},
		{"deepseek/deepseek-v3.2-20251201",
			"deepseek/deepseek-v3.2"},
		{"google/gemma-4-26b-a4b-it-20260403",
			"google/gemma-4-26b-a4b-it"},
		{"GOOGLE/Gemini-2.5-Pro", "google/gemini-2.5-pro"},
		{"gpt-5.4", "gpt-5.4"},
		{"", ""},
	}
	for _, tt := range tests {
		got := NormalizeModel(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeModel(%q) = %q, want %q",
				tt.input, got, tt.want)
		}
	}
}
