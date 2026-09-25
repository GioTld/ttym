package ttym

import (
	"bytes"
	"sync"
)

// appendUint formats non-negative integer v as decimal digits and appends to b.
// Highly optimized for small integers common in coordinates and RGB values.
func appendUint(b []byte, v int) []byte {
	if v == 0 {
		return append(b, '0')
	}
	if v < 10 {
		return append(b, byte('0'+v))
	}
	if v < 100 {
		return append(b, byte('0'+(v/10)), byte('0'+(v%10)))
	}
	if v < 1000 {
		return append(b, byte('0'+(v/100)), byte('0'+((v/10)%10)), byte('0'+(v%10)))
	}

	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	return append(b, buf[i:]...)
}

// appendCursorPos appends ANSI cursor repositioning escape sequence "\033[row;colH".
func appendCursorPos(b []byte, row, col int) []byte {
	b = append(b, '\033', '[')
	b = appendUint(b, row)
	b = append(b, ';')
	b = appendUint(b, col)
	return append(b, 'H')
}

// appendFGColor appends ANSI 24-bit truecolor foreground escape sequence "\033[38;2;r;g;bm".
func appendFGColor(b []byte, c RGB) []byte {
	b = append(b, "\033[38;2;"...)
	b = appendUint(b, int(c.R))
	b = append(b, ';')
	b = appendUint(b, int(c.G))
	b = append(b, ';')
	b = appendUint(b, int(c.B))
	return append(b, 'm')
}

// appendBGColor appends ANSI 24-bit truecolor background escape sequence "\033[48;2;r;g;bm".
func appendBGColor(b []byte, c RGB) []byte {
	b = append(b, "\033[48;2;"...)
	b = appendUint(b, int(c.R))
	b = append(b, ';')
	b = appendUint(b, int(c.G))
	b = append(b, ';')
	b = appendUint(b, int(c.B))
	return append(b, 'm')
}

// AppendRenderKeyframe generates a full screen redraw ANSI sequence directly into dst.
// Reuses dst's underlying memory when capacity permits, achieving zero allocations.
func AppendRenderKeyframe(dst []byte, cells []CellState, width, height int) []byte {
	var currFG, currBG RGB
	hasFG, hasBG := false, false

	for y := 0; y < height; y++ {
		dst = appendCursorPos(dst, y+1, 1)
		rowOffset := y * width
		for x := 0; x < width; x++ {
			c := cells[rowOffset+x]
			if !hasFG || currFG != c.FG {
				dst = appendFGColor(dst, c.FG)
				currFG = c.FG
				hasFG = true
			}
			if !hasBG || currBG != c.BG {
				dst = appendBGColor(dst, c.BG)
				currBG = c.BG
				hasBG = true
			}
			dst = append(dst, c.Char...)
		}
	}
	return dst
}

// AppendRenderDelta generates ANSI sequences updating only cells that changed significantly.
// Writes directly into dst, returning the modified slice and count of updated dirty cells.
func AppendRenderDelta(dst []byte, current, prev []CellState, width, height int, threshold int) ([]byte, int) {
	dirtyCount := 0
	lastX, lastY := -999, -999
	var currFG, currBG RGB
	hasFG, hasBG := false, false

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
				dst = appendCursorPos(dst, y+1, x+1)
			}
			if !hasFG || currFG != curr.FG {
				dst = appendFGColor(dst, curr.FG)
				currFG = curr.FG
				hasFG = true
			}
			if !hasBG || currBG != curr.BG {
				dst = appendBGColor(dst, curr.BG)
				currBG = curr.BG
				hasBG = true
			}
			dst = append(dst, curr.Char...)
			lastX, lastY = x, y
		}
	}

	return dst, dirtyCount
}

// RingBuffer provides rotating pre-allocated byte slices to achieve zero-allocation
// frame diffing and encoding across successive frames.
type RingBuffer struct {
	mu      sync.Mutex
	buffers [][]byte
	idx     int
}

// NewRingBuffer creates a ring buffer with the specified slot count and initial slot capacity.
func NewRingBuffer(slots int, initialCap int) *RingBuffer {
	if slots <= 0 {
		slots = 8
	}
	if initialCap <= 0 {
		initialCap = 64 * 1024
	}
	bufs := make([][]byte, slots)
	for i := range bufs {
		bufs[i] = make([]byte, 0, initialCap)
	}
	return &RingBuffer{
		buffers: bufs,
	}
}

// Next retrieves the next rotating byte buffer slice with length 0.
func (rb *RingBuffer) Next() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	b := rb.buffers[rb.idx][:0]
	rb.idx = (rb.idx + 1) % len(rb.buffers)
	return b
}

// RenderKeyframe renders a full screen redraw using memory from the ring buffer.
func (rb *RingBuffer) RenderKeyframe(cells []CellState, width, height int) []byte {
	buf := rb.Next()
	return AppendRenderKeyframe(buf, cells, width, height)
}

// RenderDelta renders frame differences using memory from the ring buffer.
func (rb *RingBuffer) RenderDelta(current, prev []CellState, width, height int, threshold int) ([]byte, int) {
	buf := rb.Next()
	return AppendRenderDelta(buf, current, prev, width, height, threshold)
}

// DefaultRingBuffer is the package-level pre-warmed ring buffer used by RenderKeyframe and RenderDelta.
var DefaultRingBuffer = NewRingBuffer(8, 128*1024)
