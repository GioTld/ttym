package ttym

import (
	"math"
)

// RGB represents an 8-bit RGB color.
type RGB struct {
	R, G, B uint8
}

const (
	Palette64  = 64
	Palette125 = 125
	Palette216 = 216
)

var (
	quantLUT64  [256]uint8
	quantLUT125 [256]uint8

	// oklabQuantLUT is a 32x32x32 precomputed lookup table mapping (r>>3, g>>3, b>>3)
	// bins directly to the closest Palette216 color by perceptual Oklab distance.
	oklabQuantLUT [32][32][32]RGB

	bayer4x4 = [4][4]float64{
		{0.0 / 16.0, 8.0 / 16.0, 2.0 / 16.0, 10.0 / 16.0},
		{12.0 / 16.0, 4.0 / 16.0, 14.0 / 16.0, 6.0 / 16.0},
		{3.0 / 16.0, 11.0 / 16.0, 1.0 / 16.0, 9.0 / 16.0},
		{15.0 / 16.0, 7.0 / 16.0, 13.0 / 16.0, 5.0 / 16.0},
	}

	bayerOffsets216 [4][4]int16
)

func srgbToLinear(c uint8) float64 {
	v := float64(c) / 255.0
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func rgbToOklab(r, g, b uint8) (L, a, ob float64) {
	lr := srgbToLinear(r)
	lg := srgbToLinear(g)
	lb := srgbToLinear(b)

	l := 0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb
	m := 0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb
	s := 0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb

	l_ := math.Cbrt(l)
	m_ := math.Cbrt(m)
	s_ := math.Cbrt(s)

	L = 0.2104542553*l_ + 0.7936177850*m_ - 0.0040720468*s_
	a = 1.9779984951*l_ - 2.4285922050*m_ + 0.4505937099*s_
	ob = 0.0259040371*l_ + 0.7827717662*m_ - 0.8086757660*s_
	return
}

func init() {
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			bayerOffsets216[y][x] = int16(math.Round((bayer4x4[y][x] - 0.5) * 51.0 * 0.75))
		}
	}

	// 64 colors: 4 levels {0, 85, 170, 255}
	for i := 0; i < 256; i++ {
		quantLUT64[i] = uint8(math.Round(float64(i)/85.0) * 85.0)
	}

	// 125 colors: 5 levels {0, 64, 128, 192, 255}
	levels125 := [5]uint8{0, 64, 128, 192, 255}
	for i := 0; i < 256; i++ {
		idx := int(math.Round(float64(i) / 63.75))
		if idx > 4 {
			idx = 4
		}
		quantLUT125[i] = levels125[idx]
	}

	// Build Oklab LUT for Palette216: for each 8-wide RGB bin, find the nearest
	// palette color by Oklab perceptual distance.
	type oklabEntry struct{ L, a, b float64 }
	palette := make([]struct {
		rgb RGB
		lab oklabEntry
	}, 0, 216)
	for ri := 0; ri < 6; ri++ {
		for gi := 0; gi < 6; gi++ {
			for bi := 0; bi < 6; bi++ {
				rgb := RGB{uint8(ri * 51), uint8(gi * 51), uint8(bi * 51)}
				L, a, b := rgbToOklab(rgb.R, rgb.G, rgb.B)
				palette = append(palette, struct {
					rgb RGB
					lab oklabEntry
				}{rgb, oklabEntry{L, a, b}})
			}
		}
	}
	for ri := 0; ri < 32; ri++ {
		for gi := 0; gi < 32; gi++ {
			for bi := 0; bi < 32; bi++ {
				r := uint8(ri*8 + 4)
				g := uint8(gi*8 + 4)
				b := uint8(bi*8 + 4)
				L, a, bb := rgbToOklab(r, g, b)
				bestDist := math.MaxFloat64
				var best RGB
				for _, p := range palette {
					dL := L - p.lab.L
					da := a - p.lab.a
					db := bb - p.lab.b
					dist := dL*dL + da*da + db*db
					if dist < bestDist {
						bestDist = dist
						best = p.rgb
					}
				}
				oklabQuantLUT[ri][gi][bi] = best
			}
		}
	}
}

func quantizeChannel(val uint8, px, py int, palette int, dither bool) uint8 {
	var lut *[256]uint8
	var step float64

	switch palette {
	case Palette64:
		lut = &quantLUT64
		step = 85.0
	case Palette125:
		fallthrough
	default:
		lut = &quantLUT125
		step = 64.0
	}

	if !dither || val == 0 {
		return lut[val]
	}

	bayerVal := bayer4x4[py&3][px&3]
	offset := (bayerVal - 0.5) * step * 0.75
	v := float64(val) + offset
	if v < 0 {
		return lut[0]
	}
	if v > 255 {
		return lut[255]
	}
	return lut[uint8(v)]
}

// QuantizeRGBWithCoord quantizes an RGB color with optional Bayer dithering anchored at (px, py).
// For Palette216 it uses the perceptual Oklab LUT; for other palettes it quantizes per-channel in RGB.
func QuantizeRGBWithCoord(r, g, b uint8, px, py int, palette int, dither bool) RGB {
	if r == 0 && g == 0 && b == 0 {
		return RGB{0, 0, 0}
	}
	if palette == Palette216 {
		if dither {
			offset := bayerOffsets216[py&3][px&3]
			if r < 16 && g < 16 && b < 16 && offset > 0 {
				// Retain clean deep blacks without checkerboard noise
			} else {
				r, g, b = clampDitherRGBFast(r, g, b, offset)
			}
		}
		return oklabQuantLUT[r>>3][g>>3][b>>3]
	}
	return RGB{
		R: quantizeChannel(r, px, py, palette, dither),
		G: quantizeChannel(g, px, py, palette, dither),
		B: quantizeChannel(b, px, py, palette, dither),
	}
}

// rgbDist returns the squared Euclidean distance between two colors in RGB space.
func rgbDist(a, b RGB) int {
	dr := int(a.R) - int(b.R)
	dg := int(a.G) - int(b.G)
	db := int(a.B) - int(b.B)
	return dr*dr + dg*dg + db*db
}

// BatchOklabDistances computes squared Euclidean distance in Oklab perceptual color space
// between a target color and an array of palette colors.
func BatchOklabDistances(targetL, targetA, targetB float64, paletteL, paletteA, paletteB []float64, outDists []float64) {
	batchOklabDistancesFast(targetL, targetA, targetB, paletteL, paletteA, paletteB, outDists)
}
