package ttym

import (
	"bytes"
	"fmt"
)

// Decoder maintains the virtual character-cell canvas and translates video packets into terminal ANSI sequences.
type Decoder struct {
	width         int
	height        int
	canvas        []CellState
	prevCanvas    []CellState
	scratchCanvas []CellState
}

// NewDecoder initializes a stateful Decoder with pre-allocated steady-state canvas buffers.
func NewDecoder(width, height int) *Decoder {
	totalCells := width * height
	return &Decoder{
		width:         width,
		height:        height,
		canvas:        make([]CellState, totalCells),
		prevCanvas:    make([]CellState, totalCells),
		scratchCanvas: make([]CellState, totalCells),
	}
}

// Canvas returns the current decoded cell grid.
func (d *Decoder) Canvas() []CellState {
	return d.canvas
}

// ApplyKeyframe updates the internal canvas state with a full grid of cells.
func (d *Decoder) ApplyKeyframe(cells []CellState) error {
	if len(cells) != d.width*d.height {
		return fmt.Errorf("%w: cell count %d does not match %dx%d", ErrInvalidDimensions, len(cells), d.width, d.height)
	}
	copy(d.canvas, cells)
	copy(d.prevCanvas, cells)
	return nil
}

// ApplyMotionDelta parses motion vectors and residuals, updating the canvas with 2D block displacement.
func (d *Decoder) ApplyMotionDelta(data []byte) error {
	mf, err := DecodeMotionFrame(data, d.width, d.height)
	if err != nil {
		return err
	}

	gridW := int(mf.GridWidth)
	gridH := int(mf.GridHeight)
	bs := int(mf.BlockSize)

	// Displace reference blocks from prevCanvas into canvas
	for by := 0; by < gridH; by++ {
		y0 := by * bs
		bh := bs
		if y0+bh > d.height {
			bh = d.height - y0
		}

		for bx := 0; bx < gridW; bx++ {
			x0 := bx * bs
			bw := bs
			if x0+bw > d.width {
				bw = d.width - x0
			}

			bIdx := by*gridW + bx
			v := mf.Vectors[bIdx]
			refX0 := x0 + int(v.DX)
			refY0 := y0 + int(v.DY)

			for ry := 0; ry < bh; ry++ {
				currY := y0 + ry
				refY := refY0 + ry
				for rx := 0; rx < bw; rx++ {
					currX := x0 + rx
					refX := refX0 + rx
					d.canvas[currY*d.width+currX] = d.prevCanvas[refY*d.width+refX]
				}
			}
		}
	}

	// Apply sparse cell residuals
	for _, r := range mf.Residuals {
		bx := int(r.BlockIdx % mf.GridWidth)
		by := int(r.BlockIdx / mf.GridWidth)
		cx := bx*bs + int(r.RelX)
		cy := by*bs + int(r.RelY)
		if cx < d.width && cy < d.height {
			d.canvas[cy*d.width+cx] = r.Cell
		}
	}

	// Snapshot to prevCanvas for subsequent delta frames
	copy(d.prevCanvas, d.canvas)
	return nil
}

// Decode processes a video packet (Keyframe, Classic Delta, or Motion Delta) and returns ready-to-write ANSI sequences.
func (d *Decoder) Decode(pkt *Packet) ([]byte, error) {
	if pkt.IsKeyframe() {
		if bytes.HasPrefix(pkt.Data, MagicKeyframeCells[:]) {
			cells, err := UnpackKeyframeCells(pkt.Data, d.width, d.height)
			if err != nil {
				return nil, err
			}
			if err := d.ApplyKeyframe(cells); err != nil {
				return nil, err
			}
			return RenderKeyframe(d.canvas, d.width, d.height), nil
		}
		// Raw ANSI keyframe pass-through
		return pkt.Data, nil
	}

	if pkt.IsMotionDelta() {
		copy(d.scratchCanvas, d.prevCanvas)
		if err := d.ApplyMotionDelta(pkt.Data); err != nil {
			return nil, err
		}
		ansi, _ := RenderDelta(d.canvas, d.scratchCanvas, d.width, d.height, 0)
		return ansi, nil
	}

	if pkt.Type == PacketTypeVideoDelta {
		// Raw ANSI delta pass-through
		return pkt.Data, nil
	}

	return nil, fmt.Errorf("ttym: unexpected non-video packet type %d", pkt.Type)
}

// DecodeCells processes a video packet and returns the reconstructed []CellState grid.
func (d *Decoder) DecodeCells(pkt *Packet) ([]CellState, error) {
	if pkt.IsKeyframe() {
		if bytes.HasPrefix(pkt.Data, MagicKeyframeCells[:]) {
			cells, err := UnpackKeyframeCells(pkt.Data, d.width, d.height)
			if err != nil {
				return nil, err
			}
			if err := d.ApplyKeyframe(cells); err != nil {
				return nil, err
			}
			return d.canvas, nil
		}
		return nil, fmt.Errorf("ttym: cannot decode raw ANSI keyframe to CellState without parsing")
	}

	if pkt.IsMotionDelta() {
		if err := d.ApplyMotionDelta(pkt.Data); err != nil {
			return nil, err
		}
		return d.canvas, nil
	}

	return nil, fmt.Errorf("ttym: packet type %d does not support direct CellState extraction", pkt.Type)
}
