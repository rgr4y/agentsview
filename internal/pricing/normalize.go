package pricing

import (
	"regexp"
	"strings"
)

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// NormalizeModel lowercases a model string and strips a
// trailing -YYYYMMDD date suffix. Used to reconcile
// provider-specific model IDs (e.g. "Qwen/Qwen3-Coder-Next",
// "deepseek/deepseek-v3.2-20251201") against canonical pricing
// keys (e.g. "qwen/qwen3-coder-next", "deepseek/deepseek-v3.2").
func NormalizeModel(model string) string {
	model = strings.ToLower(model)
	model = dateSuffix.ReplaceAllString(model, "")
	return model
}
