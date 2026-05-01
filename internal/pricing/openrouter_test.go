package pricing

import (
	"math"
	"testing"
)

func TestParseOpenRouterPricing(t *testing.T) {
	data := []byte(`{
		"data": [
			{
				"id": "deepseek/deepseek-v3.2",
				"pricing": {
					"prompt": "0.00000014",
					"completion": "0.00000028",
					"input_cache_read": "0.000000014"
				}
			},
			{
				"id": "google/gemini-2.5-pro",
				"pricing": {
					"prompt": "0.00000125",
					"completion": "0.00001"
				}
			}
		]
	}`)

	prices, err := ParseOpenRouterPricing(data)
	if err != nil {
		t.Fatalf("ParseOpenRouterPricing: %v", err)
	}

	if len(prices) != 2 {
		t.Fatalf("expected 2 models, got %d", len(prices))
	}

	byID := make(map[string]ModelPricing)
	for _, p := range prices {
		byID[p.ModelPattern] = p
	}

	ds := byID["deepseek/deepseek-v3.2"]
	assertClose(t, "deepseek input", ds.InputPerMTok, 0.14)
	assertClose(t, "deepseek output", ds.OutputPerMTok, 0.28)
	assertClose(t, "deepseek cache_read",
		ds.CacheReadPerMTok, 0.014)

	gem := byID["google/gemini-2.5-pro"]
	assertClose(t, "gemini input", gem.InputPerMTok, 1.25)
	assertClose(t, "gemini output", gem.OutputPerMTok, 10.0)
	if gem.CacheReadPerMTok != 0 {
		t.Errorf("expected zero cache_read, got %f",
			gem.CacheReadPerMTok)
	}
}

func TestParseOpenRouterPricingSkipsZero(t *testing.T) {
	data := []byte(`{
		"data": [
			{
				"id": "free-model",
				"pricing": {
					"prompt": "0",
					"completion": "0"
				}
			},
			{
				"id": "paid-model",
				"pricing": {
					"prompt": "0.000001",
					"completion": "0.000002"
				}
			}
		]
	}`)

	prices, err := ParseOpenRouterPricing(data)
	if err != nil {
		t.Fatalf("ParseOpenRouterPricing: %v", err)
	}

	if len(prices) != 1 {
		t.Fatalf("expected 1 model, got %d", len(prices))
	}
	if prices[0].ModelPattern != "paid-model" {
		t.Errorf("unexpected model: %s", prices[0].ModelPattern)
	}
}

func TestParseOpenRouterPricingEmpty(t *testing.T) {
	data := []byte(`{"data": []}`)

	prices, err := ParseOpenRouterPricing(data)
	if err != nil {
		t.Fatalf("ParseOpenRouterPricing: %v", err)
	}
	if len(prices) != 0 {
		t.Fatalf("expected 0 models, got %d", len(prices))
	}
}

func TestParseORCost(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"", 0},
		{"0", 0},
		{"0.000001", 0.000001},
		{"0.00000014", 0.00000014},
		{"garbage", 0},
	}
	for _, tt := range tests {
		got := parseORCost(tt.input)
		if math.Abs(got-tt.want) > 1e-15 {
			t.Errorf("parseORCost(%q) = %v, want %v",
				tt.input, got, tt.want)
		}
	}
}
