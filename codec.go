package ttym

import (
	"bytes"
	"fmt"
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

// RenderKeyframe generates a full screen redraw ANSI sequence.
func RenderKeyframe(cells []CellState, width, height int) []byte {
	var buf bytes.Buffer
	var currFG, currBG *RGB

	for y := 0; y < height; y++ {
		fmt.Fprintf(&buf, "\033[%d;1H", y+1)
		for x := 0; x < width; x++ {
			c := cells[y*width+x]
			if currFG == nil || *currFG != c.FG {
				fmt.Fprintf(&buf, "\033[38;2;%d;%d;%dm", c.FG.R, c.FG.G, c.FG.B)
				fg := c.FG
				currFG = &fg
			}
			if currBG == nil || *currBG != c.BG {
				fmt.Fprintf(&buf, "\033[48;2;%d;%d;%dm", c.BG.R, c.BG.G, c.BG.B)
				bg := c.BG
				currBG = &bg
			}
			buf.Write(c.Char)
		}
	}
	return buf.Bytes()
}

// RenderDelta generates ANSI sequences updating only the cells that changed significantly since prev.
func RenderDelta(current, prev []CellState, width, height int, threshold int) ([]byte, int) {
	var buf bytes.Buffer
	dirtyCount := 0

	lastX, lastY := -999, -999
	var currFG, currBG *RGB

	for y := 0; y < height; y++ {
		rowOffset := y * width
		for x := 0; x < width; x++ {
			idx := rowOffset + x
			curr := current[idx]
			if prev != nil {
				if threshold <= 0 {
					p := prev[idx]
					if curr.FG == p.FG && curr.BG == p.BG && bytes.Equal(curr.Char, p.Char) {
						continue
					}
				} else {
					if CellDistance(curr, prev[idx]) <= threshold {
						current[idx] = prev[idx]
						continue
					}
				}
			}

			dirtyCount++

			if y != lastY || x != lastX+1 {
				fmt.Fprintf(&buf, "\033[%d;%dH", y+1, x+1)
			}
			if currFG == nil || *currFG != curr.FG {
				fmt.Fprintf(&buf, "\033[38;2;%d;%d;%dm", curr.FG.R, curr.FG.G, curr.FG.B)
				fg := curr.FG
				currFG = &fg
			}
			if currBG == nil || *currBG != curr.BG {
				fmt.Fprintf(&buf, "\033[48;2;%d;%d;%dm", curr.BG.R, curr.BG.G, curr.BG.B)
				bg := curr.BG
				currBG = &bg
			}
			buf.Write(curr.Char)
			lastX, lastY = x, y
		}
	}

	return buf.Bytes(), dirtyCount
}
