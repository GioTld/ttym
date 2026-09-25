//go:build !amd64 && !arm64

package ttym

func rgbDistBatch4(px *[4]RGB, c0, c1 RGB) ([4]int32, [4]int32) {
	return rgbDistBatch4Scalar(px, c0, c1)
}

func clampDitherRGBFast(r, g, b uint8, offset int16) (uint8, uint8, uint8) {
	return clampDitherRGBScalar(r, g, b, offset)
}

func batchOklabDistancesFast(targetL, targetA, targetB float64, pL, pA, pB, out []float64) {
	batchOklabDistancesScalar(targetL, targetA, targetB, pL, pA, pB, out)
}
