package adapter

import "strings"

const commandCodePricingURL = "https://commandcode.ai/docs/resources/pricing-limits#models"

type commandCodePrice struct {
	Input         float64
	Output        float64
	CacheRead     float64
	CacheWrite    float64
	HasCacheWrite bool
}

// Values are USD per 1M tokens and reflect the current prices published by
// Command Code. The live provider model ID is the catalog key.
var commandCodePricingCatalog = map[string]commandCodePrice{
	"claude-sonnet-5":                       {Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5, HasCacheWrite: true},
	"claude-sonnet-4-6":                     {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75, HasCacheWrite: true},
	"claude-fable-5-1":                      {Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.5, HasCacheWrite: true},
	"claude-fable-5":                        {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5, HasCacheWrite: true},
	"claude-opus-5":                         {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25, HasCacheWrite: true},
	"claude-opus-4-8":                       {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25, HasCacheWrite: true},
	"claude-opus-4-7":                       {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25, HasCacheWrite: true},
	"claude-haiku-4-5-20251001":             {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25, HasCacheWrite: true},
	"gpt-5.6-sol":                           {Input: 5, Output: 30, CacheRead: 0.5, CacheWrite: 6.25, HasCacheWrite: true},
	"gpt-5.6-terra":                         {Input: 2, Output: 12, CacheRead: 0.2, CacheWrite: 2.5, HasCacheWrite: true},
	"gpt-5.6-luna":                          {Input: 0.2, Output: 1.2, CacheRead: 0.02, CacheWrite: 0.25, HasCacheWrite: true},
	"gpt-5.5":                               {Input: 5, Output: 30, CacheRead: 0.5},
	"gpt-5.4":                               {Input: 2.5, Output: 15, CacheRead: 0.25},
	"gpt-5.3-codex":                         {Input: 2, Output: 8, CacheRead: 0.5},
	"gpt-5.4-mini":                          {Input: 0.75, Output: 4.5, CacheRead: 0.075},
	"deepseek/deepseek-v4-pro":              {Input: 0.66, Output: 1.98, CacheRead: 0.022},
	"deepseek/deepseek-v4-flash":            {Input: 0.15, Output: 0.6, CacheRead: 0.003},
	"deepseek/deepseek-v4-flash-vision-exp": {Input: 0.15, Output: 0.6, CacheRead: 0.003},
	"deepseek/deepseek-v4-flash-fast":       {Input: 0.28, Output: 0.56, CacheRead: 0.07},
	"deepseek/deepseek-v4.1-flash":          {Input: 0.15, Output: 0.6, CacheRead: 0.003},
	"moonshotai/Kimi-K3":                    {Input: 3, Output: 15, CacheRead: 0.3},
	"moonshotai/Kimi-K2.7-Code":             {Input: 0.95, Output: 4, CacheRead: 0.19},
	"moonshotai/Kimi-K2.7-Code-Highspeed":   {Input: 1.9, Output: 8, CacheRead: 0.38},
	"moonshotai/Kimi-K2.6":                  {Input: 0.95, Output: 4, CacheRead: 0.16},
	"moonshotai/Kimi-K2.5":                  {Input: 0.6, Output: 3, CacheRead: 0.1},
	"z-ai/glm-5.3-flash":                    {Input: 0.15, Output: 0.5, CacheRead: 0.03},
	"z-ai/glm-5.3-flashx":                   {Input: 0.37, Output: 1.25, CacheRead: 0.075},
	"zai-org/GLM-5.3":                       {Input: 1.4, Output: 4.4, CacheRead: 0.26},
	"zai-org/GLM-5.2":                       {Input: 1.4, Output: 4.4, CacheRead: 0.26},
	"zai-org/GLM-5.2-Fast":                  {Input: 3, Output: 10.25, CacheRead: 0.5},
	"zai-org/GLM-5.1":                       {Input: 1.4, Output: 4.4, CacheRead: 0.26},
	"zai-org/GLM-5":                         {Input: 1, Output: 3.2, CacheRead: 0.2},
	"MiniMaxAI/MiniMax-M3":                  {Input: 0.3, Output: 1.2, CacheRead: 0.06},
	"MiniMaxAI/MiniMax-M2.7":                {Input: 0.3, Output: 1.2, CacheRead: 0.06},
	"MiniMaxAI/MiniMax-M2.5":                {Input: 0.3, Output: 1.2, CacheRead: 0.03},
	"xiaomi/mimo-v2.5-pro":                  {Input: 0.435, Output: 0.87, CacheRead: 0.0036},
	"xiaomi/mimo-v2.5":                      {Input: 0.14, Output: 0.28, CacheRead: 0.0028},
	"Qwen/Qwen3.8-Omni-Flash":               {Input: 0.15, Output: 0.47, CacheRead: 0.016},
	"Qwen/Qwen3.8-Max-0902":                 {Input: 2, Output: 6, CacheRead: 0.25},
	"Qwen/Qwen3.8-Max":                      {Input: 2, Output: 6, CacheRead: 0.25, CacheWrite: 2.5, HasCacheWrite: true},
	"Qwen/Qwen3.8-27B":                      {Input: 0.4, Output: 3, CacheRead: 0.04},
	"Qwen/Qwen3.8-Flash":                    {Input: 0.16, Output: 0.47, CacheRead: 0.016},
	"Qwen/Qwen3.7-Max":                      {Input: 2.5, Output: 7.5, CacheRead: 0.5, CacheWrite: 3.13, HasCacheWrite: true},
	"Qwen/Qwen3.7-Plus":                     {Input: 0.4, Output: 1.6, CacheRead: 0.08, CacheWrite: 0.5, HasCacheWrite: true},
	"Qwen/Qwen3.7-Flash":                    {Input: 0.03, Output: 0.13, CacheRead: 0.006, CacheWrite: 0.038, HasCacheWrite: true},
	"Qwen/Qwen3.6-Max-Preview":              {Input: 1.3, Output: 7.8, CacheRead: 0.26, CacheWrite: 1.63, HasCacheWrite: true},
	"Qwen/Qwen3.6-Plus":                     {Input: 0.5, Output: 3, CacheRead: 0.1},
	"meituan/LongCat-2.0":                   {Input: 0.3, Output: 1.2, CacheRead: 0.006},
	"stepfun/Step-3.7-Flash":                {Input: 0.2, Output: 1.15, CacheRead: 0.04},
	"stepfun/Step-3.5-Flash":                {Input: 0.1, Output: 0.3, CacheRead: 0.02},
	"tencent/hy3-paid":                      {Input: 0.14, Output: 0.58, CacheRead: 0.035},
	"tencent/hy4-preview":                   {Input: 0.834, Output: 2.501, CacheRead: 0.042},
	"google/gemini-3.8-flash":               {Input: 1.5, Output: 7.5, CacheRead: 0.15},
	"google/gemini-3.7-flash":               {Input: 1.5, Output: 7.5, CacheRead: 0.15, CacheWrite: 0.08334, HasCacheWrite: true},
	"google/gemini-3.6-flash":               {Input: 1.5, Output: 7.5, CacheRead: 0.15},
	"google/gemini-3.5-flash":               {Input: 1.5, Output: 9, CacheRead: 0.15},
	"google/gemini-3.5-flash-lite":          {Input: 0.3, Output: 2.5, CacheRead: 0.03},
	"google/gemini-3.1-flash-lite":          {Input: 0.25, Output: 1.5, CacheRead: 0.03},
	"sakana/fugu-ultra":                     {Input: 5, Output: 30, CacheRead: 0.5},
	"nvidia/nemotron-3-ultra-550b-a55b":     {Input: 0.6, Output: 2.4, CacheRead: 0.12},
	"thinkingmachines/inkling":              {Input: 1, Output: 4.05, CacheRead: 0.17},
	"thinkingmachines/inkling-small":        {Input: 0.5, Output: 1.2, CacheRead: 0.1},
	"poolside/laguna-s-2.1-free":            {Input: 0, Output: 0, CacheRead: 0},
	"inclusionai/ling-3.0-flash-sante:free": {Input: 0, Output: 0, CacheRead: 0},
	"meta/muse-spark-1.1":                   {Input: 1.25, Output: 4.25, CacheRead: 0.15},
	"meta/muse-spark-1.2":                   {Input: 1.25, Output: 4.25, CacheRead: 0.15},
	"meta/muse-spark-1.2-contributor":       {Input: 0.1, Output: 0.2, CacheRead: 0.002},
	"meta/muse-spark-1.3":                   {Input: 1.25, Output: 4.25, CacheRead: 0.15},
	"meta/muse-spark-1.3-contributor":       {Input: 0.1, Output: 0.2, CacheRead: 0.002},
	"xai/grok-4.5":                          {Input: 2, Output: 6, CacheRead: 0.5},
	"xai/grok-4.6":                          {Input: 2, Output: 6, CacheRead: 0.5},
}

func commandCodeModelPricing(modelID string) (*ModelPricing, bool) {
	modelID = strings.TrimSpace(modelID)
	price, ok := commandCodePricingCatalog[modelID]
	if !ok {
		return nil, false
	}

	raw := map[string]any{
		"source":           "command_code",
		"pricing_url":      commandCodePricingURL,
		"unit":             "USD per 1M tokens",
		"input_value":      price.Input,
		"output_value":     price.Output,
		"cache_read_value": price.CacheRead,
	}
	pricing := &ModelPricing{
		ModelName:      modelID,
		GroupName:      "default",
		QuotaType:      0,
		BillingType:    "tokens",
		Currency:       "USD",
		GroupRatio:     1,
		InputValue:     price.Input,
		HasInputValue:  true,
		OutputValue:    price.Output,
		HasOutputValue: true,
		CacheRatio:     0,
		HasCacheRatio:  true,
		Raw:            raw,
	}
	if price.Input > 0 {
		pricing.CacheRatio = price.CacheRead / price.Input
	}
	if price.HasCacheWrite && price.Input > 0 {
		pricing.CreateCacheRatio = price.CacheWrite / price.Input
		pricing.HasCreateCacheRatio = true
		raw["cache_write_value"] = price.CacheWrite
	}
	return pricing, true
}
