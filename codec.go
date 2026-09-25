package ttym

import (
	"bytes"
)

// CellDistance calculates the perceptual distance between two cells.
// Any block character mismatch returns a very large penalty to force an immediate redraw.
func CellDistance(c1, c2 CellState) int {
	if !bytes.Equal(c1.Char, c2.Char) {
		return 1 << 30
	}
	dr1 := int(c1.FG.R) - int(c2.FG.R)
	if dr1 < 0 {
		dr1 = -dr1
	}
	dg1 := int(c1.FG.G) - int(c2.FG.G)
	if dg1 < 0 {
		dg1 = -dg1
	}
	db1 := int(c1.FG.B) - int(c2.FG.B)
	if db1 < 0 {
		db1 = -db1
	}

	dr2 := int(c1.BG.R) - int(c2.BG.R)
	if dr2 < 0 {
		dr2 = -dr2
	}
	dg2 := int(c1.BG.G) - int(c2.BG.G)
	if dg2 < 0 {
		dg2 = -dg2
	}
	db2 := int(c1.BG.B) - int(c2.BG.B)
	if db2 < 0 {
		db2 = -db2
	}

	return dr1 + dg1 + db1 + dr2 + dg2 + db2
}

// RenderKeyframe generates a full screen redraw ANSI sequence using pre-warmed ring buffers.
func RenderKeyframe(cells []CellState, width, height int) []byte {
	return DefaultRingBuffer.RenderKeyframe(cells, width, height)
}

// RenderDelta generates ANSI sequences updating only the cells that changed significantly since prev
// using pre-warmed ring buffers.
func RenderDelta(current, prev []CellState, width, height int, threshold int) ([]byte, int) {
	return DefaultRingBuffer.RenderDelta(current, prev, width, height, threshold)
}

// EncodeMotionDelta computes block motion estimation and returns packed motion delta bytes and residual count.
func EncodeMotionDelta(current, prev []CellState, width, height, blockSize, searchRadius, threshold int) ([]byte, int) {
	mf := EstimateBlockMotion(current, prev, width, height, blockSize, searchRadius, threshold)
	return mf.Encode(), len(mf.Residuals)
}

