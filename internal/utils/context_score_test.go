package utils

import "testing"

func TestScoreChunkWeighted(t *testing.T) {
	f := ChunkFeature{
		Relevance:        0.8,
		Recency:          0.6,
		RoleWeight:       1.0,
		Importance:       0.9,
		DuplicatePenalty: 0.3,
	}

	got := ScoreChunk(f, DefaultScoreWeights())
	want := 0.75

	if !almostEqual(got, want, 1e-9) {
		t.Fatalf("expected %.6f, got %.6f", want, got)
	}
}

func TestScoreChunkDuplicatePenaltyLowersScore(t *testing.T) {
	base := ChunkFeature{
		Relevance:  0.9,
		Recency:    0.7,
		RoleWeight: 1,
		Importance: 0.8,
	}
	withDup := base
	withDup.DuplicatePenalty = 0.9

	scoreBase := ScoreChunk(base, DefaultScoreWeights())
	scoreDup := ScoreChunk(withDup, DefaultScoreWeights())

	if scoreDup >= scoreBase {
		t.Fatalf("expected duplicate penalty to lower score: base=%.6f dup=%.6f", scoreBase, scoreDup)
	}
}

func TestScorePerTokenPrefersHigherDensity(t *testing.T) {
	w := DefaultScoreWeights()
	highDensity := ChunkFeature{
		Relevance:  0.75,
		Recency:    0.7,
		RoleWeight: 1.0,
		Importance: 0.6,
		Tokens:     80,
	}
	lowDensity := ChunkFeature{
		Relevance:  0.9,
		Recency:    0.9,
		RoleWeight: 1.0,
		Importance: 0.9,
		Tokens:     320,
	}

	if ScorePerToken(highDensity, w) <= ScorePerToken(lowDensity, w) {
		t.Fatalf("expected high-density chunk to have higher per-token score")
	}
}

func TestScoreChunkClampsFeatureRange(t *testing.T) {
	f := ChunkFeature{
		Relevance:        1.8,  // clamp to 1
		Recency:          -0.6, // clamp to 0
		RoleWeight:       2.3,  // clamp to 1
		Importance:       0.5,
		DuplicatePenalty: -1.2, // clamp to 0
	}

	got := ScoreChunk(f, DefaultScoreWeights())
	want := 0.70

	if !almostEqual(got, want, 1e-9) {
		t.Fatalf("expected %.6f, got %.6f", want, got)
	}
}

func TestScoreChunkFloorAtZero(t *testing.T) {
	f := ChunkFeature{
		Relevance:        0,
		Recency:          0,
		RoleWeight:       0,
		Importance:       0,
		DuplicatePenalty: 1,
	}
	w := ScoreWeights{
		DuplicatePenalty: 2,
	}

	if got := ScoreChunk(f, w); got != 0 {
		t.Fatalf("expected score floor at 0, got %.6f", got)
	}
}

func TestScorePerTokenTokensAtMostOne(t *testing.T) {
	f := ChunkFeature{
		Relevance:  1,
		RoleWeight: 1,
		Tokens:     0,
	}

	got := ScorePerToken(f, DefaultScoreWeights())
	want := ScoreChunk(f, DefaultScoreWeights())
	if !almostEqual(got, want, 1e-9) {
		t.Fatalf("expected per-token score to use divisor 1 when tokens<=0")
	}
}

/*
almostEqual 判断两个浮点数在给定误差范围内是否近似相等。
*/
func almostEqual(a, b, eps float64) bool {
	if a > b {
		return a-b <= eps
	}
	return b-a <= eps
}
