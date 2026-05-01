package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const openRouterURL = "https://openrouter.ai/api/v1/models"

type openRouterResponse struct {
	Data []openRouterModel `json:"data"`
}

type openRouterModel struct {
	ID      string         `json:"id"`
	Pricing orModelPricing `json:"pricing"`
}

type orModelPricing struct {
	Prompt         string `json:"prompt"`
	Completion     string `json:"completion"`
	InputCacheRead string `json:"input_cache_read"`
}

// FetchOpenRouterPricing downloads the OpenRouter model list
// and returns pricing entries. Per-token string costs are
// converted to per-million-token float64s.
func FetchOpenRouterPricing() ([]ModelPricing, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(openRouterURL)
	if err != nil {
		return nil, fmt.Errorf("fetching openrouter pricing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"fetching openrouter pricing: status %d", resp.StatusCode,
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading openrouter response: %w", err)
	}

	return ParseOpenRouterPricing(data)
}

// ParseOpenRouterPricing parses the OpenRouter JSON response
// into ModelPricing entries. Entries where both prompt and
// completion are zero or missing are skipped.
func ParseOpenRouterPricing(
	data []byte,
) ([]ModelPricing, error) {
	var raw openRouterResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing openrouter JSON: %w", err)
	}

	var prices []ModelPricing
	for _, m := range raw.Data {
		prompt := parseORCost(m.Pricing.Prompt)
		completion := parseORCost(m.Pricing.Completion)
		if prompt == 0 && completion == 0 {
			continue
		}
		p := ModelPricing{
			ModelPattern:  m.ID,
			InputPerMTok:  prompt * perMTok,
			OutputPerMTok: completion * perMTok,
		}
		if cr := parseORCost(m.Pricing.InputCacheRead); cr > 0 {
			p.CacheReadPerMTok = cr * perMTok
		}
		prices = append(prices, p)
	}
	return prices, nil
}

func parseORCost(s string) float64 {
	if s == "" || s == "0" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
