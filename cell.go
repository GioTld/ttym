package ttym

// CellState represents the colors and block character for a single terminal cell.
// In half-block mode Char is always ▀ and FG/BG are the top/bottom pixel colors.
// In quarter-block mode Char is one of the 16 Unicode block characters and FG/BG
// are the two representative colors chosen by k-means over the 4 subpixels.
type CellState struct {
	FG   RGB
	BG   RGB
	Char []byte
}

var GlyphHalfBlock = []byte("▀") // U+2580

// quarterChars maps a 4-bit subpixel pattern to its Unicode block character.
// Bit layout: bit3=TL, bit2=TR, bit1=BL, bit0=BR (1=FG, 0=BG).
var quarterChars = [16]string{
	" ", "▗", "▖", "▄", "▝", "▐", "▞", "▟",
	"▘", "▚", "▌", "▙", "▀", "▜", "▛", "█",
}

// CellFromQuarter builds a CellState for a 2×2 subpixel group using 2-color k-means.
func CellFromQuarter(tl, tr, bl, br RGB) CellState {
	px := [4]RGB{tl, tr, bl, br}

	// k-means with k=2: initialize centroids as the two most distant pixels.
	c0, c1 := px[0], px[0]
	maxDist := 0
	for _, p := range px[1:] {
		if d := rgbDist(px[0], p); d > maxDist {
			maxDist = d
			c1 = p
		}
	}

	var assign [4]int
	for iter := 0; iter < 3; iter++ {
		var sum0, sum1 [3]int
		var cnt0, cnt1 int
		for i, p := range px {
			if rgbDist(p, c0) <= rgbDist(p, c1) {
				assign[i] = 0
				sum0[0] += int(p.R)
				sum0[1] += int(p.G)
				sum0[2] += int(p.B)
				cnt0++
			} else {
				assign[i] = 1
				sum1[0] += int(p.R)
				sum1[1] += int(p.G)
				sum1[2] += int(p.B)
				cnt1++
			}
		}
		if cnt0 > 0 {
			c0 = RGB{uint8(sum0[0] / cnt0), uint8(sum0[1] / cnt0), uint8(sum0[2] / cnt0)}
		}
		if cnt1 > 0 {
			c1 = RGB{uint8(sum1[0] / cnt1), uint8(sum1[1] / cnt1), uint8(sum1[2] / cnt1)}
		}
	}

	// Build 4-bit pattern: TL=bit3, TR=bit2, BL=bit1, BR=bit0.
	pattern := assign[0]<<3 | assign[1]<<2 | assign[2]<<1 | assign[3]
	return CellState{FG: c1, BG: c0, Char: []byte(quarterChars[pattern])}
}

// ExtractCells processes a raw RGB24 frame into a grid of CellState.
// Half-block mode (quarterBlocks=false): frame is W×2H pixels; each cell gets one top and
// one bottom pixel, producing a ▀ character.
// Quarter-block mode (quarterBlocks=true): frame is 2W×2H pixels; each cell gets four 2×2
// subpixels, color is taken raw or quantized, and the best block character is chosen
// by 2-color k-means.
func ExtractCells(raw []byte, width, height int, palette int, dither bool, truecolor bool, quarterBlocks bool) []CellState {
	cells := make([]CellState, width*height)

	if quarterBlocks {
		// Frame stride is 2*width pixels wide (6 bytes per cell horizontally).
		stride := width * 2 * 3
		for y := 0; y < height; y++ {
			topRow := y * 2 * stride
			botRow := topRow + stride
			for x := 0; x < width; x++ {
				tl := RGB{raw[topRow+x*6], raw[topRow+x*6+1], raw[topRow+x*6+2]}
				tr := RGB{raw[topRow+x*6+3], raw[topRow+x*6+4], raw[topRow+x*6+5]}
				bl := RGB{raw[botRow+x*6], raw[botRow+x*6+1], raw[botRow+x*6+2]}
				br := RGB{raw[botRow+x*6+3], raw[botRow+x*6+4], raw[botRow+x*6+5]}
				cells[y*width+x] = CellFromQuarter(tl, tr, bl, br)
			}
		}
		return cells
	}

	// Half-block mode: frame is width × (height*2) pixels.
	pixelStride := width * 3
	for y := 0; y < height; y++ {
		topPixelY := y * 2
		botPixelY := y*2 + 1
		topRowOffset := topPixelY * pixelStride
		botRowOffset := botPixelY * pixelStride

		for x := 0; x < width; x++ {
			tx := topRowOffset + x*3
			bx := botRowOffset + x*3

			var fg, bg RGB
			if truecolor {
				fg = RGB{raw[tx], raw[tx+1], raw[tx+2]}
				bg = RGB{raw[bx], raw[bx+1], raw[bx+2]}
			} else {
				fg = QuantizeRGBWithCoord(raw[tx], raw[tx+1], raw[tx+2], x, topPixelY, palette, dither)
				bg = QuantizeRGBWithCoord(raw[bx], raw[bx+1], raw[bx+2], x, botPixelY, palette, dither)
			}
			cells[y*width+x] = CellState{FG: fg, BG: bg, Char: GlyphHalfBlock}
		}
	}
	return cells
}
