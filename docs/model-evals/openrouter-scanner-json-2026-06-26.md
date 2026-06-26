# OpenRouter Scanner JSON Eval - 2026-06-26

## Command

```sh
go run ./cmd/modelbench -mode=json -repeat=1 -timeout=45s -models 'z-ai/glm-5.2,moonshotai/kimi-k2.7-code,minimax/minimax-m3,deepseek/deepseek-v4-pro,qwen/qwen3.7-max'
```

## Suite

Four labelled prediction-market scanner cases:

- reject no-edge wide-spread market
- buy yes when liquid proxy implies higher probability
- buy no when yes is overpriced versus fresh evidence
- reject illiquid wide-spread market

Scoring checks structured JSON, trade/no-trade decision, side, category, risk cap, latency, and token use.

## Results

| Model | Structured | Decision | Side | Category | Risk | Avg Score | Avg Latency | Input Tokens | Output Tokens |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| z-ai/glm-5.2 | 1.000 | 1.000 | 1.000 | 0.500 | 1.000 | 95.0 | 4.415s | 1,080 | 1,060 |
| minimax/minimax-m3 | 1.000 | 1.000 | 1.000 | 0.500 | 1.000 | 95.0 | 12.014s | 1,652 | 1,139 |
| qwen/qwen3.7-max | 1.000 | 1.000 | 1.000 | 0.250 | 1.000 | 92.5 | 23.792s | 1,175 | 4,842 |
| deepseek/deepseek-v4-pro | 0.750 | 0.750 | 0.750 | 0.250 | 0.750 | 70.0 | 6.922s | 1,162 | 1,766 |
| moonshotai/kimi-k2.7-code | 0.500 | 0.500 | 0.500 | 0.000 | 0.500 | 45.0 | 6.731s | 1,084 | 1,765 |

## Interpretation

For the current structured scanner path, `z-ai/glm-5.2` is still the best default from this run: it tied MiniMax on score, beat Qwen Max on latency and output tokens, and preserved fully structured, decision-correct JSON.

`qwen/qwen3.7-max` should remain in the stack for diversity or judge fallback, but not as the first scanner model. It was accurate on this suite, but averaged 23.8s and 4,842 output tokens across four cases.

`minimax/minimax-m3` improved materially on this rerun and tied GLM on score, but it was materially slower than GLM in this sample. It is a plausible cheap fallback and worth tracking with more cases before promotion.

`moonshotai/kimi-k2.7-code` remains a strong critical/research candidate, but it had strict scanner-contract failures from verbosity/truncation in this JSON path.

This is a small labelled eval, not a final model-selection benchmark. Next expansion should replay real accepted/rejected signals and score downstream thesis quality, fill outcomes, and anti-portfolio counterfactuals.
