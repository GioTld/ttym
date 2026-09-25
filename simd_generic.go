package ttym

// rgbDistBatch4Scalar computes squared RGB distance from 4 pixels in px to c0 and c1.
func rgbDistBatch4Scalar(px *[4]RGB, c0, c1 RGB) (d0, d1 [4]int32) {
	dr0_0 := int32(px[0].R) - int32(c0.R)
	dg0_0 := int32(px[0].G) - int32(c0.G)
	db0_0 := int32(px[0].B) - int32(c0.B)
	d0[0] = dr0_0*dr0_0 + dg0_0*dg0_0 + db0_0*db0_0

	dr0_1 := int32(px[1].R) - int32(c0.R)
	dg0_1 := int32(px[1].G) - int32(c0.G)
	db0_1 := int32(px[1].B) - int32(c0.B)
	d0[1] = dr0_1*dr0_1 + dg0_1*dg0_1 + db0_1*db0_1

	dr0_2 := int32(px[2].R) - int32(c0.R)
	dg0_2 := int32(px[2].G) - int32(c0.G)
	db0_2 := int32(px[2].B) - int32(c0.B)
	d0[2] = dr0_2*dr0_2 + dg0_2*dg0_2 + db0_2*db0_2

	dr0_3 := int32(px[3].R) - int32(c0.R)
	dg0_3 := int32(px[3].G) - int32(c0.G)
	db0_3 := int32(px[3].B) - int32(c0.B)
	d0[3] = dr0_3*dr0_3 + dg0_3*dg0_3 + db0_3*db0_3

	dr1_0 := int32(px[0].R) - int32(c1.R)
	dg1_0 := int32(px[0].G) - int32(c1.G)
	db1_0 := int32(px[0].B) - int32(c1.B)
	d1[0] = dr1_0*dr1_0 + dg1_0*dg1_0 + db1_0*db1_0

	dr1_1 := int32(px[1].R) - int32(c1.R)
	dg1_1 := int32(px[1].G) - int32(c1.G)
	db1_1 := int32(px[1].B) - int32(c1.B)
	d1[1] = dr1_1*dr1_1 + dg1_1*dg1_1 + db1_1*db1_1

	dr1_2 := int32(px[2].R) - int32(c1.R)
	dg1_2 := int32(px[2].G) - int32(c1.G)
	db1_2 := int32(px[2].B) - int32(c1.B)
	d1[2] = dr1_2*dr1_2 + dg1_2*dg1_2 + db1_2*db1_2

	dr1_3 := int32(px[3].R) - int32(c1.R)
	dg1_3 := int32(px[3].G) - int32(c1.G)
	db1_3 := int32(px[3].B) - int32(c1.B)
	d1[3] = dr1_3*dr1_3 + dg1_3*dg1_3 + db1_3*db1_3

	return
}

// clampDitherRGBScalar adds signed offset to r, g, b and clamps each channel to [0, 255].
func clampDitherRGBScalar(r, g, b uint8, offset int16) (uint8, uint8, uint8) {
	clamp := func(v int16) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return clamp(int16(r) + offset), clamp(int16(g) + offset), clamp(int16(b) + offset)
}

// batchOklabDistancesScalar calculates squared Euclidean distance in Oklab space for a slice of palette colors.
func batchOklabDistancesScalar(targetL, targetA, targetB float64, paletteL, paletteA, paletteB []float64, outDists []float64) {
	n := len(outDists)
	for i := 0; i < n; i++ {
		dL := targetL - paletteL[i]
		da := targetA - paletteA[i]
		db := targetB - paletteB[i]
		outDists[i] = dL*dL + da*da + db*db
	}
}
