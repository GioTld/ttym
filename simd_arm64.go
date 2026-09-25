//go:build arm64

package ttym

//go:noescape
func rgbDistBatch4Asm(px *[4]RGB, c0R, c0G, c0B, c1R, c1G, c1B uint8, d0, d1 *[4]int32)

//go:noescape
func clampDitherRGBAsm(r, g, b uint8, offset int16) (uint8, uint8, uint8)

//go:noescape
func batchOklabDistancesAsm(targetL, targetA, targetB float64, pL, pA, pB, out *float64, n int)

func rgbDistBatch4(px *[4]RGB, c0, c1 RGB) (d0, d1 [4]int32) {
	rgbDistBatch4Asm(px, c0.R, c0.G, c0.B, c1.R, c1.G, c1.B, &d0, &d1)
	return
}

func clampDitherRGBFast(r, g, b uint8, offset int16) (uint8, uint8, uint8) {
	return clampDitherRGBAsm(r, g, b, offset)
}

func batchOklabDistancesFast(targetL, targetA, targetB float64, pL, pA, pB, out []float64) {
	n := len(out)
	if n == 0 {
		return
	}
	batchOklabDistancesAsm(targetL, targetA, targetB, &pL[0], &pA[0], &pB[0], &out[0], n)
}
