package ttym

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestHeaderRoundTrip(t *testing.T) {
	orig := &Header{
		Version:     CurrentVersion,
		ColorMode:   ColorModeTruecolor,
		Width:       160,
		Height:      80,
		FPS:         24,
		TotalFrames: 240,
		DurationMs:  10000,
		ChunkCount:  2,
		Metadata:    "Test Movie Title",
	}

	var buf bytes.Buffer
	if err := orig.Write(&buf); err != nil {
		t.Fatalf("Header.Write failed: %v", err)
	}

	readHdr, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}

	if *readHdr != *orig {
		t.Errorf("header mismatch: got %+v, want %+v", readHdr, orig)
	}
}

func TestHeaderLegacyMagicCompatibility(t *testing.T) {
	// Write a header with legacy 'GIOV' magic bytes
	var buf bytes.Buffer
	var raw [FileHeaderFixedSize]byte
	copy(raw[0:4], LegacyMagicFile[:])
	raw[4] = CurrentVersion
	raw[5] = ColorModeTruecolor
	binary.BigEndian.PutUint16(raw[6:8], 120)
	binary.BigEndian.PutUint16(raw[8:10], 60)
	binary.BigEndian.PutUint16(raw[10:12], 24)
	buf.Write(raw[:])

	hdr, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("expected legacy GIOV magic to be accepted, got error: %v", err)
	}
	if hdr.Width != 120 || hdr.Height != 60 {
		t.Errorf("unexpected header values: %+v", hdr)
	}
}

func TestHeaderValidationErrors(t *testing.T) {
	// Invalid magic
	badMagic := []byte{'B', 'A', 'A', 'D', 1, 1, 0, 80, 0, 40, 0, 24, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := ReadHeader(bytes.NewReader(badMagic)); !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}

	// Zero dimensions
	zeroDim := &Header{Width: 0, Height: 80, FPS: 24}
	var buf bytes.Buffer
	if err := zeroDim.Write(&buf); !errors.Is(err, ErrInvalidDimensions) {
		t.Errorf("expected ErrInvalidDimensions on zero width write, got %v", err)
	}

	// Exceeding maximum dimensions
	hugeDim := &Header{Width: 4000, Height: 80, FPS: 24}
	if err := hugeDim.Write(&buf); !errors.Is(err, ErrInvalidDimensions) {
		t.Errorf("expected ErrInvalidDimensions on huge width write, got %v", err)
	}
}

func TestChunkRoundTripAndBombProtection(t *testing.T) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter failed: %v", err)
	}
	defer enc.Close()

	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd.NewReader failed: %v", err)
	}
	defer dec.Close()

	origChunk := &Chunk{
		StartTimestampMs: 0,
		EndTimestampMs:   1000,
		Frames: []Frame{
			{TimestampMs: 0, Type: FrameTypeKeyframe, Data: []byte("frame0-data")},
			{TimestampMs: 500, Type: FrameTypeDelta, Data: []byte("frame1-data")},
		},
	}

	var buf bytes.Buffer
	if err := WriteChunk(&buf, origChunk, enc); err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	readChunk, err := ReadChunk(&buf, dec)
	if err != nil {
		t.Fatalf("ReadChunk failed: %v", err)
	}

	if len(readChunk.Frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(readChunk.Frames))
	}
	if string(readChunk.Frames[0].Data) != "frame0-data" {
		t.Errorf("frame data mismatch: got %s", readChunk.Frames[0].Data)
	}

	// Test decompression bomb rejection
	var fakeBomb bytes.Buffer
	var hdr [ChunkHeaderSize]byte
	copy(hdr[0:4], MagicChunk[:])
	binary.BigEndian.PutUint32(hdr[14:18], MaxDecompressedChunkBytes+1) // declare 65MB
	binary.BigEndian.PutUint32(hdr[18:22], 10)
	fakeBomb.Write(hdr[:])
	fakeBomb.Write(make([]byte, 10))

	if _, err := ReadChunk(&fakeBomb, dec); !errors.Is(err, ErrDecompressionBomb) {
		t.Errorf("expected ErrDecompressionBomb, got %v", err)
	}
}

func TestTrailerRoundTripAndOffsetValidation(t *testing.T) {
	entries := []IndexEntry{
		{FileOffset: 32, StartTimestampMs: 0},
		{FileOffset: 512, StartTimestampMs: 5000},
	}

	var buf bytes.Buffer
	// Prepend dummy header to satisfy minimum file size check
	dummyHdr := make([]byte, FileHeaderFixedSize)
	buf.Write(dummyHdr)

	indexOffset := uint64(buf.Len())
	if err := WriteTrailer(&buf, indexOffset, entries); err != nil {
		t.Fatalf("WriteTrailer failed: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	readEntries, err := ReadTrailer(reader, uint32(len(entries)))
	if err != nil {
		t.Fatalf("ReadTrailer failed: %v", err)
	}

	if len(readEntries) != len(entries) {
		t.Fatalf("entry count mismatch: got %d, want %d", len(readEntries), len(entries))
	}
	for i := range entries {
		if readEntries[i] != entries[i] {
			t.Errorf("entry %d mismatch: got %+v, want %+v", i, readEntries[i], entries[i])
		}
	}

	// Test out-of-bounds indexOffset
	var corruptBuf bytes.Buffer
	corruptBuf.Write(dummyHdr)
	// Write a trailer claiming index is at offset 999999 (far beyond file)
	if err := WriteTrailer(&corruptBuf, 999999, entries); err != nil {
		t.Fatalf("WriteTrailer failed: %v", err)
	}
	corruptReader := bytes.NewReader(corruptBuf.Bytes())
	if _, err := ReadTrailer(corruptReader, uint32(len(entries))); !errors.Is(err, ErrInvalidTrailer) {
		t.Errorf("expected ErrInvalidTrailer, got %v", err)
	}
}

func TestQuantizeAndDithering(t *testing.T) {
	// 125 palette
	got125 := QuantizeRGBWithCoord(100, 100, 100, 0, 0, Palette125, false)
	if got125.R != 128 {
		t.Errorf("Palette125 100 -> want 128, got %d", got125.R)
	}

	// 216 palette (Oklab LUT)
	got216 := QuantizeRGBWithCoord(100, 100, 100, 0, 0, Palette216, false)
	if got216.R != 102 {
		t.Errorf("Palette216 100 -> want 102, got %d", got216.R)
	}

	// Bayer dithering determinism
	c1 := QuantizeRGBWithCoord(110, 110, 110, 2, 3, Palette125, true)
	c2 := QuantizeRGBWithCoord(110, 110, 110, 2, 3, Palette125, true)
	if c1 != c2 {
		t.Fatalf("Bayer dithering must be deterministic: %v vs %v", c1, c2)
	}
}

func TestCellExtractionHalfAndQuarter(t *testing.T) {
	// Half-block 2x1 cell grid -> 2x2 pixels
	rawHB := []byte{
		255, 0, 0, 0, 255, 0,
		0, 0, 255, 255, 255, 0,
	}
	cellsHB := ExtractCells(rawHB, 2, 1, Palette125, false, false, false)
	if len(cellsHB) != 2 {
		t.Fatalf("expected 2 half-block cells, got %d", len(cellsHB))
	}
	if !bytes.Equal(cellsHB[0].Char, GlyphHalfBlock) {
		t.Errorf("expected ▀ character for half-block, got %q", cellsHB[0].Char)
	}

	// Quarter-block 1x1 cell grid -> 2x2 pixels
	rawQB := []byte{
		255, 0, 0, 255, 0, 0, // top half red
		0, 0, 255, 0, 0, 255, // bot half blue
	}
	cellsQB := ExtractCells(rawQB, 1, 1, Palette216, false, false, true)
	if len(cellsQB) != 1 {
		t.Fatalf("expected 1 quarter-block cell, got %d", len(cellsQB))
	}
	char := string(cellsQB[0].Char)
	if char != "▀" && char != "▄" {
		t.Errorf("expected ▀ or ▄ for half-split quarter block, got %q", char)
	}
}

func TestCodecDistanceAndDelta(t *testing.T) {
	c1 := CellState{FG: RGB{100, 100, 100}, BG: RGB{50, 50, 50}, Char: GlyphHalfBlock}
	c2 := CellState{FG: RGB{102, 100, 100}, BG: RGB{50, 50, 50}, Char: GlyphHalfBlock}
	diff := CellDistance(c1, c2)
	if diff != 2 {
		t.Errorf("expected distance 2, got %d", diff)
	}

	// Character change should produce massive distance
	cDiffChar := CellState{FG: RGB{100, 100, 100}, BG: RGB{50, 50, 50}, Char: []byte("▄")}
	if CellDistance(c1, cDiffChar) < 1000000 {
		t.Errorf("expected massive penalty on char mismatch")
	}

	// RenderDelta with threshold
	prev := []CellState{c1, c1}
	curr := []CellState{c2, {FG: RGB{255, 0, 0}, BG: RGB{0, 0, 0}, Char: GlyphHalfBlock}}
	out, dirty := RenderDelta(curr, prev, 2, 1, 10)
	if dirty != 1 {
		t.Fatalf("expected 1 dirty cell, got %d", dirty)
	}
	if !strings.Contains(string(out), "\033[1;2H") {
		t.Errorf("expected cursor jump to 1;2, got %s", out)
	}
}
