package ttym

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

var (
	MagicMotionFrame   = [4]byte{'M', 'O', 'T', '1'}
	MagicKeyframeCells = [4]byte{'K', 'F', 'R', '1'}
)

// MotionVector specifies 2D displacement in cell units.
type MotionVector struct {
	DX int8
	DY int8
}

// BlockResidual stores a single cell modification within a macroblock.
type BlockResidual struct {
	BlockIdx uint16
	RelX     uint8
	RelY     uint8
	Cell     CellState
}

// MotionFrame encapsulates 2D block motion vectors and sparse cell residuals.
type MotionFrame struct {
	BlockSize  uint8
	GridWidth  uint16
	GridHeight uint16
	Vectors    []MotionVector
	Residuals  []BlockResidual
}

// EstimateBlockMotion performs 2D block matching between current and previous frame cells.
func EstimateBlockMotion(current, prev []CellState, width, height, blockSize, searchRadius, threshold int) *MotionFrame {
	if blockSize <= 0 || blockSize > MaxBlockSize {
		blockSize = DefaultBlockSize
	}
	if searchRadius <= 0 || searchRadius > MaxSearchRadius {
		searchRadius = DefaultSearchRadius
	}

	gridW := (width + blockSize - 1) / blockSize
	gridH := (height + blockSize - 1) / blockSize
	totalBlocks := gridW * gridH

	vectors := make([]MotionVector, totalBlocks)
	var residuals []BlockResidual

	for by := 0; by < gridH; by++ {
		y0 := by * blockSize
		bh := blockSize
		if y0+bh > height {
			bh = height - y0
		}

		for bx := 0; bx < gridW; bx++ {
			x0 := bx * blockSize
			bw := blockSize
			if x0+bw > width {
				bw = width - x0
			}

			blockIdx := uint16(by*gridW + bx)

			// 1. Evaluate (0, 0) vector first
			bestDX, bestDY := int8(0), int8(0)
			bestSAD := calculateBlockSAD(current, prev, width, x0, y0, 0, 0, bw, bh)

			// 2. Search window [-searchRadius, +searchRadius] if zero vector has non-zero distortion
			if bestSAD > 0 && prev != nil {
				for dy := -searchRadius; dy <= searchRadius; dy++ {
					refY0 := y0 + dy
					if refY0 < 0 || refY0+bh > height {
						continue
					}

					for dx := -searchRadius; dx <= searchRadius; dx++ {
						if dx == 0 && dy == 0 {
							continue
						}
						refX0 := x0 + dx
						if refX0 < 0 || refX0+bw > width {
							continue
						}

						sad := calculateBlockSAD(current, prev, width, x0, y0, dx, dy, bw, bh)
						// Vector cost penalty (lambda = 8) to prefer (0, 0) on ambiguous/flat regions
						absDX := dx
						if absDX < 0 {
							absDX = -absDX
						}
						absDY := dy
						if absDY < 0 {
							absDY = -absDY
						}
						cost := sad + 8*(absDX+absDY)

						if cost < bestSAD {
							bestSAD = cost
							bestDX = int8(dx)
							bestDY = int8(dy)
						}
					}
				}
			}

			vectors[blockIdx] = MotionVector{DX: bestDX, DY: bestDY}

			// 3. Extract residuals for cells that deviate past threshold
			refX0 := x0 + int(bestDX)
			refY0 := y0 + int(bestDY)

			for ry := 0; ry < bh; ry++ {
				currY := y0 + ry
				refY := refY0 + ry
				for rx := 0; rx < bw; rx++ {
					currX := x0 + rx
					refX := refX0 + rx

					currCell := current[currY*width+currX]
					if prev != nil {
						refCell := prev[refY*width+refX]
						if threshold <= 0 {
							if currCell.FG == refCell.FG && currCell.BG == refCell.BG && bytes.Equal(currCell.Char, refCell.Char) {
								continue
							}
						} else {
							if CellDistance(currCell, refCell) <= threshold {
								continue
							}
						}
					}

					residuals = append(residuals, BlockResidual{
						BlockIdx: blockIdx,
						RelX:     uint8(rx),
						RelY:     uint8(ry),
						Cell:     currCell,
					})
				}
			}
		}
	}

	return &MotionFrame{
		BlockSize:  uint8(blockSize),
		GridWidth:  uint16(gridW),
		GridHeight: uint16(gridH),
		Vectors:    vectors,
		Residuals:  residuals,
	}
}

func calculateBlockSAD(current, prev []CellState, width, x0, y0, dx, dy, bw, bh int) int {
	if prev == nil {
		return 1 << 30
	}
	sad := 0
	refX0 := x0 + dx
	refY0 := y0 + dy

	for ry := 0; ry < bh; ry++ {
		currRow := (y0 + ry) * width
		refRow := (refY0 + ry) * width
		for rx := 0; rx < bw; rx++ {
			c1 := current[currRow+(x0+rx)]
			c2 := prev[refRow+(refX0+rx)]
			sad += CellDistance(c1, c2)
		}
	}
	return sad
}

// Encode serializes the MotionFrame into a compact binary format.
func (mf *MotionFrame) Encode() []byte {
	var buf bytes.Buffer
	buf.Write(MagicMotionFrame[:])
	buf.WriteByte(mf.BlockSize)

	var hdr [8]byte
	binary.BigEndian.PutUint16(hdr[0:2], mf.GridWidth)
	binary.BigEndian.PutUint16(hdr[2:4], mf.GridHeight)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(mf.Residuals)))
	buf.Write(hdr[:])

	// Packed motion vectors (2 bytes per block: dx, dy)
	for _, v := range mf.Vectors {
		buf.WriteByte(byte(v.DX))
		buf.WriteByte(byte(v.DY))
	}

	// Residuals
	var rHdr [4]byte
	for _, r := range mf.Residuals {
		binary.BigEndian.PutUint16(rHdr[0:2], r.BlockIdx)
		rHdr[2] = r.RelX
		rHdr[3] = r.RelY
		buf.Write(rHdr[:])

		buf.WriteByte(r.Cell.FG.R)
		buf.WriteByte(r.Cell.FG.G)
		buf.WriteByte(r.Cell.FG.B)
		buf.WriteByte(r.Cell.BG.R)
		buf.WriteByte(r.Cell.BG.G)
		buf.WriteByte(r.Cell.BG.B)

		charLen := uint8(len(r.Cell.Char))
		buf.WriteByte(charLen)
		if charLen > 0 {
			buf.Write(r.Cell.Char)
		}
	}

	return buf.Bytes()
}

// DecodeMotionFrame deserializes a MotionFrame with strict boundary and integrity validation.
func DecodeMotionFrame(data []byte, width, height int) (*MotionFrame, error) {
	if len(data) < 13 {
		return nil, ErrCorruptMotionData
	}

	if !bytes.Equal(data[0:4], MagicMotionFrame[:]) {
		return nil, ErrInvalidMagic
	}

	bs := data[4]
	if bs == 0 || bs > MaxBlockSize {
		return nil, ErrInvalidBlockSize
	}

	gw := binary.BigEndian.Uint16(data[5:7])
	gh := binary.BigEndian.Uint16(data[7:9])
	resCount := binary.BigEndian.Uint32(data[9:13])

	expectedGW := uint16((width + int(bs) - 1) / int(bs))
	expectedGH := uint16((height + int(bs) - 1) / int(bs))
	if gw != expectedGW || gh != expectedGH {
		return nil, fmt.Errorf("%w: grid mismatch (%dx%d != %dx%d)", ErrCorruptMotionData, gw, gh, expectedGW, expectedGH)
	}

	totalBlocks := int(gw) * int(gh)
	vecBytes := totalBlocks * 2
	if len(data) < 13+vecBytes {
		return nil, ErrCorruptMotionData
	}

	vectors := make([]MotionVector, totalBlocks)
	offset := 13
	for i := 0; i < totalBlocks; i++ {
		dx := int8(data[offset])
		dy := int8(data[offset+1])
		offset += 2

		bx := i % int(gw)
		by := i / int(gw)
		x0 := bx * int(bs)
		y0 := by * int(bs)
		bw := int(bs)
		if x0+bw > width {
			bw = width - x0
		}
		bh := int(bs)
		if y0+bh > height {
			bh = height - y0
		}

		refX0 := x0 + int(dx)
		refY0 := y0 + int(dy)
		if refX0 < 0 || refX0+bw > width || refY0 < 0 || refY0+bh > height {
			return nil, fmt.Errorf("%w: vector (%d, %d) out of canvas bounds at block %d", ErrCorruptMotionData, dx, dy, i)
		}

		vectors[i] = MotionVector{DX: dx, DY: dy}
	}

	residuals := make([]BlockResidual, 0, resCount)
	reader := bytes.NewReader(data[offset:])

	for i := uint32(0); i < resCount; i++ {
		var rHdr [4]byte
		if _, err := reader.Read(rHdr[:]); err != nil {
			return nil, ErrCorruptMotionData
		}
		bIdx := binary.BigEndian.Uint16(rHdr[0:2])
		relX := rHdr[2]
		relY := rHdr[3]

		if int(bIdx) >= totalBlocks || relX >= bs || relY >= bs {
			return nil, ErrCorruptMotionData
		}

		var colorBuf [6]byte
		if _, err := reader.Read(colorBuf[:]); err != nil {
			return nil, ErrCorruptMotionData
		}

		charLenByte, err := reader.ReadByte()
		if err != nil || charLenByte > 4 {
			return nil, ErrCorruptMotionData
		}

		charBuf := make([]byte, charLenByte)
		if charLenByte > 0 {
			if _, err := reader.Read(charBuf); err != nil {
				return nil, ErrCorruptMotionData
			}
		}

		residuals = append(residuals, BlockResidual{
			BlockIdx: bIdx,
			RelX:     relX,
			RelY:     relY,
			Cell: CellState{
				FG:   RGB{R: colorBuf[0], G: colorBuf[1], B: colorBuf[2]},
				BG:   RGB{R: colorBuf[3], G: colorBuf[4], B: colorBuf[5]},
				Char: charBuf,
			},
		})
	}

	return &MotionFrame{
		BlockSize:  bs,
		GridWidth:  gw,
		GridHeight: gh,
		Vectors:    vectors,
		Residuals:  residuals,
	}, nil
}

// PackKeyframeCells serializes a full grid of CellStates into a compact binary keyframe.
func PackKeyframeCells(cells []CellState, width, height int) []byte {
	var buf bytes.Buffer
	buf.Write(MagicKeyframeCells[:])

	var dim [4]byte
	binary.BigEndian.PutUint16(dim[0:2], uint16(width))
	binary.BigEndian.PutUint16(dim[2:4], uint16(height))
	buf.Write(dim[:])

	for _, c := range cells {
		buf.WriteByte(c.FG.R)
		buf.WriteByte(c.FG.G)
		buf.WriteByte(c.FG.B)
		buf.WriteByte(c.BG.R)
		buf.WriteByte(c.BG.G)
		buf.WriteByte(c.BG.B)

		charLen := uint8(len(c.Char))
		buf.WriteByte(charLen)
		if charLen > 0 {
			buf.Write(c.Char)
		}
	}

	return buf.Bytes()
}

// UnpackKeyframeCells deserializes a packed keyframe into a slice of CellStates.
func UnpackKeyframeCells(data []byte, width, height int) ([]CellState, error) {
	if len(data) < 8 {
		return nil, ErrCorruptData
	}

	if !bytes.Equal(data[0:4], MagicKeyframeCells[:]) {
		return nil, ErrInvalidMagic
	}

	w := int(binary.BigEndian.Uint16(data[4:6]))
	h := int(binary.BigEndian.Uint16(data[6:8]))
	if w != width || h != height {
		return nil, fmt.Errorf("%w: dimensions %dx%d do not match expected %dx%d", ErrInvalidDimensions, w, h, width, height)
	}

	expectedCells := width * height
	cells := make([]CellState, 0, expectedCells)
	reader := bytes.NewReader(data[8:])

	for i := 0; i < expectedCells; i++ {
		var colors [6]byte
		if _, err := reader.Read(colors[:]); err != nil {
			return nil, ErrCorruptData
		}

		charLen, err := reader.ReadByte()
		if err != nil || charLen > 4 {
			return nil, ErrCorruptData
		}

		charBuf := make([]byte, charLen)
		if charLen > 0 {
			if _, err := reader.Read(charBuf); err != nil {
				return nil, ErrCorruptData
			}
		}

		cells = append(cells, CellState{
			FG:   RGB{R: colors[0], G: colors[1], B: colors[2]},
			BG:   RGB{R: colors[3], G: colors[4], B: colors[5]},
			Char: charBuf,
		})
	}

	return cells, nil
}
