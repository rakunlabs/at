package common

// EstimateEmbeddingInputTokens gives embedding calls the same conservative
// byte-based weight used for provider-budget reservation. It is deliberately
// an estimate: exact tokenization depends on the selected upstream model.
func EstimateEmbeddingInputTokens(input []string) int {
	total := 0
	for _, text := range input {
		total += max(1, len(text)/4)
	}
	return total
}
