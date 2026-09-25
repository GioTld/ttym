package ttym

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
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

func TestHeaderAudioRoundTripAndValidation(t *testing.T) {
	// 1. Valid Opus audio header
	opusHdr := &Header{
		Version:         CurrentVersion,
		ColorMode:       ColorModeTruecolor,
		Width:           160,
		Height:          80,
		FPS:             30,
		TotalFrames:     300,
		DurationMs:      10000,
		ChunkCount:      2,
		AudioCodec:      AudioCodecOpus,
		AudioChannels:   2,
		AudioSampleRate: 48000,
		Metadata:        "Movie With Audio",
	}

	var buf bytes.Buffer
	if err := opusHdr.Write(&buf); err != nil {
		t.Fatalf("opusHdr.Write failed: %v", err)
	}

	readHdr, err := ReadHeader(&buf)
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}

	if *readHdr != *opusHdr {
		t.Errorf("header mismatch: got %+v, want %+v", readHdr, opusHdr)
	}

	// 2. AudioConfig validation errors
	invalidConfigs := []Header{
		{Width: 100, Height: 50, FPS: 24, AudioCodec: AudioCodecNone, AudioChannels: 1},
		{Width: 100, Height: 50, FPS: 24, AudioCodec: AudioCodecNone, AudioSampleRate: 44100},
		{Width: 100, Height: 50, FPS: 24, AudioCodec: 99, AudioChannels: 2, AudioSampleRate: 44100},
		{Width: 100, Height: 50, FPS: 24, AudioCodec: AudioCodecPCM, AudioChannels: 0, AudioSampleRate: 44100},
		{Width: 100, Height: 50, FPS: 24, AudioCodec: AudioCodecPCM, AudioChannels: 9, AudioSampleRate: 44100},
		{Width: 100, Height: 50, FPS: 24, AudioCodec: AudioCodecPCM, AudioChannels: 2, AudioSampleRate: 4000},
		{Width: 100, Height: 50, FPS: 24, AudioCodec: AudioCodecPCM, AudioChannels: 2, AudioSampleRate: 200000},
	}

	for i, cfg := range invalidConfigs {
		var b bytes.Buffer
		if err := cfg.Write(&b); !errors.Is(err, ErrInvalidAudioConfig) {
			t.Errorf("case %d: expected ErrInvalidAudioConfig, got %v", i, err)
		}
	}
}

func TestMultiplexedChunkRoundTrip(t *testing.T) {
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
		EndTimestampMs:   100,
		Frames: []Packet{
			{TimestampMs: 0, Type: PacketTypeVideoKeyframe, Data: []byte("keyframe-data")},
			{TimestampMs: 20, Type: PacketTypeAudio, Data: []byte("audio-chunk-1")},
			{TimestampMs: 40, Type: PacketTypeAudio, Data: []byte("audio-chunk-2")},
			{TimestampMs: 41, Type: PacketTypeVideoDelta, Data: []byte("delta-frame-1")},
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

	if len(readChunk.Packets()) != 4 {
		t.Fatalf("expected 4 packets, got %d", len(readChunk.Packets()))
	}

	pkts := readChunk.Packets()
	if !pkts[0].IsKeyframe() || !pkts[0].IsVideo() || pkts[0].IsAudio() {
		t.Errorf("packet 0 flags incorrect: %+v", pkts[0])
	}
	if pkts[1].IsVideo() || !pkts[1].IsAudio() {
		t.Errorf("packet 1 flags incorrect: %+v", pkts[1])
	}
	if pkts[3].IsKeyframe() || !pkts[3].IsVideo() {
		t.Errorf("packet 3 flags incorrect: %+v", pkts[3])
	}

	// Oversized audio packet test
	var oversizedChunk bytes.Buffer
	oversizedData := make([]byte, MaxAudioPacketBytes+1)
	var fhdr [FrameHeaderSize]byte
	binary.BigEndian.PutUint32(fhdr[0:4], 0)
	fhdr[4] = PacketTypeAudio
	binary.BigEndian.PutUint32(fhdr[5:9], uint32(len(oversizedData)))

	var rawPayload bytes.Buffer
	rawPayload.Write(fhdr[:])
	rawPayload.Write(oversizedData)

	compressed := enc.EncodeAll(rawPayload.Bytes(), nil)
	var chkHdr [ChunkHeaderSize]byte
	copy(chkHdr[0:4], MagicChunk[:])
	binary.BigEndian.PutUint16(chkHdr[12:14], 1)
	binary.BigEndian.PutUint32(chkHdr[14:18], uint32(rawPayload.Len()))
	binary.BigEndian.PutUint32(chkHdr[18:22], uint32(len(compressed)))
	oversizedChunk.Write(chkHdr[:])
	oversizedChunk.Write(compressed)

	if _, err := ReadChunk(&oversizedChunk, dec); !errors.Is(err, ErrAudioPacketTooLarge) {
		t.Errorf("expected ErrAudioPacketTooLarge, got %v", err)
	}
}

func TestStreamingWriterAndReader(t *testing.T) {
	var buf bytes.Buffer
	hdr := Header{
		Version:         CurrentVersion,
		ColorMode:       ColorModeTruecolor,
		Width:           80,
		Height:          40,
		FPS:             24,
		AudioCodec:      AudioCodecOpus,
		AudioChannels:   2,
		AudioSampleRate: 48000,
		Metadata:        "Streaming Test",
	}

	writer, err := NewWriter(&buf, hdr, 100) // 100ms GOP duration for test
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// GOP 1: 0ms to 99ms
	if err := writer.WriteVideoKeyframe(0, []byte("kf0")); err != nil {
		t.Fatalf("WriteVideoKeyframe failed: %v", err)
	}
	if err := writer.WriteAudioPacket(20, []byte("aud0")); err != nil {
		t.Fatalf("WriteAudioPacket failed: %v", err)
	}
	if err := writer.WriteVideoDelta(42, []byte("delta1")); err != nil {
		t.Fatalf("WriteVideoDelta failed: %v", err)
	}

	// GOP 2: starts at 100ms
	if err := writer.WriteVideoKeyframe(100, []byte("kf1")); err != nil {
		t.Fatalf("WriteVideoKeyframe failed: %v", err)
	}
	if err := writer.WriteAudioPacket(120, []byte("aud1")); err != nil {
		t.Fatalf("WriteAudioPacket failed: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close failed: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	r, err := NewReader(reader)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}
	defer r.Close()

	if len(r.Trailer()) != 2 {
		t.Fatalf("expected 2 trailer chunks, got %d", len(r.Trailer()))
	}

	// Verify sequential iteration through NextPacket
	var received []Packet
	for {
		pkt, err := r.NextPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextPacket failed: %v", err)
		}
		received = append(received, *pkt)
	}

	if len(received) != 5 {
		t.Fatalf("expected 5 packets total, got %d", len(received))
	}
	if string(received[0].Data) != "kf0" || string(received[3].Data) != "kf1" {
		t.Errorf("unexpected packet content in stream")
	}

	// Test Seeking to GOP 2
	if err := r.SeekTo(110); err != nil {
		t.Fatalf("SeekTo failed: %v", err)
	}
	firstAfterSeek, err := r.NextPacket()
	if err != nil {
		t.Fatalf("NextPacket after seek failed: %v", err)
	}
	if !firstAfterSeek.IsKeyframe() || firstAfterSeek.TimestampMs != 100 {
		t.Errorf("SeekTo did not land on GOP 2 keyframe: %+v", firstAfterSeek)
	}
}

func BenchmarkDemuxerNextPacket(b *testing.B) {
	var buf bytes.Buffer
	hdr := Header{
		Version:         CurrentVersion,
		ColorMode:       ColorModeTruecolor,
		Width:           80,
		Height:          40,
		FPS:             30,
		AudioCodec:      AudioCodecPCM,
		AudioChannels:   2,
		AudioSampleRate: 48000,
	}

	writer, _ := NewWriter(&buf, hdr, 1000)
	frameData := []byte("\033[1;1H\033[38;2;255;255;255m▀")
	audioData := make([]byte, 256)

	for i := 0; i < 60; i++ {
		ts := uint32(i * 33)
		if i%30 == 0 {
			_ = writer.WriteVideoKeyframe(ts, frameData)
		} else {
			_ = writer.WriteVideoDelta(ts, frameData)
		}
		_ = writer.WriteAudioPacket(ts, audioData)
	}
	_ = writer.Close()

	data := buf.Bytes()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		for {
			_, err := r.NextPacket()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		_ = r.Close()
	}
}

func TestMultiplexedFileLifecycleAndSeek(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.ttym")

	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	hdr := Header{
		Version:         CurrentVersion,
		ColorMode:       ColorModeTruecolor,
		Width:           120,
		Height:          60,
		FPS:             30,
		AudioCodec:      AudioCodecPCM,
		AudioChannels:   2,
		AudioSampleRate: 44100,
		Metadata:        "Disk File Test",
	}

	w, err := NewWriter(f, hdr, 200) // 200ms chunks
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Chunk 1: 0ms..199ms
	_ = w.WriteVideoKeyframe(0, []byte("chunk0-kf"))
	_ = w.WriteAudioPacket(50, []byte("chunk0-pcm1"))
	_ = w.WriteAudioPacket(100, []byte("chunk0-pcm2"))
	_ = w.WriteVideoDelta(150, []byte("chunk0-delta1"))

	// Chunk 2: 200ms..399ms
	_ = w.WriteVideoKeyframe(200, []byte("chunk1-kf"))
	_ = w.WriteAudioPacket(250, []byte("chunk1-pcm1"))
	_ = w.WriteVideoDelta(300, []byte("chunk1-delta1"))

	if err := w.Close(); err != nil {
		t.Fatalf("w.Close failed: %v", err)
	}
	f.Close()

	// Reopen file with os.Open
	readFile, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("os.Open failed: %v", err)
	}
	defer readFile.Close()

	reader, err := NewReader(readFile)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}
	defer reader.Close()

	// Verify seekable writer updated header metrics!
	readHdr := reader.Header()
	if readHdr.ChunkCount != 2 {
		t.Errorf("expected ChunkCount 2, got %d", readHdr.ChunkCount)
	}
	if readHdr.TotalFrames != 4 {
		t.Errorf("expected TotalFrames 4 (2 keyframes + 2 deltas), got %d", readHdr.TotalFrames)
	}
	if readHdr.DurationMs != 300 {
		t.Errorf("expected DurationMs 300, got %d", readHdr.DurationMs)
	}

	// Seek to 250ms (should land on chunk 1 keyframe at 200ms)
	if err := reader.SeekTo(250); err != nil {
		t.Fatalf("SeekTo failed: %v", err)
	}
	pkt, err := reader.NextPacket()
	if err != nil {
		t.Fatalf("NextPacket after seek failed: %v", err)
	}
	if !pkt.IsKeyframe() || pkt.TimestampMs != 200 {
		t.Errorf("expected chunk 1 keyframe at 200ms, got %+v", pkt)
	}
}

func TestMotionEstimationAccuracyAndDirtyReduction(t *testing.T) {
	width, height := 80, 40
	totalCells := width * height

	frame0 := make([]CellState, totalCells)
	frame1 := make([]CellState, totalCells)

	// Create vertical stripes on frame0 with period 4
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var fg RGB
			if (x/2)%2 == 0 {
				fg = RGB{255, 0, 0}
			} else {
				fg = RGB{0, 255, 0}
			}
			frame0[y*width+x] = CellState{
				FG:   fg,
				BG:   RGB{0, 0, 0},
				Char: GlyphHalfBlock,
			}
		}
	}

	// Frame 1: shifted to the right by +2 cells
	panX := 2
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x < panX {
				// Newly revealed column entering the left edge
				frame1[y*width+x] = CellState{
					FG:   RGB{0, 0, 255},
					BG:   RGB{0, 0, 0},
					Char: GlyphHalfBlock,
				}
			} else {
				// Shifted from frame 0
				frame1[y*width+x] = frame0[y*width+(x-panX)]
			}
		}
	}

	// 1. Classic delta evaluation
	_, classicDirty := RenderDelta(frame1, frame0, width, height, 0)

	// 2. Motion estimation evaluation
	mf := EstimateBlockMotion(frame1, frame0, width, height, 4, 4, 0)
	motionResiduals := len(mf.Residuals)

	if classicDirty == 0 {
		t.Fatalf("expected non-zero classic dirty count")
	}

	// Verify dirty cell reduction target (at least 50% reduction)
	reduction := float64(classicDirty-motionResiduals) / float64(classicDirty)
	if reduction < 0.50 {
		t.Errorf("dirty cell reduction %.1f%% below 50%% target (classic %d vs motion %d)",
			reduction*100, classicDirty, motionResiduals)
	}

	// Reconstruct frame1 using Decoder
	dec := NewDecoder(width, height)
	if err := dec.ApplyKeyframe(frame0); err != nil {
		t.Fatalf("ApplyKeyframe failed: %v", err)
	}

	encodedMotion := mf.Encode()
	if err := dec.ApplyMotionDelta(encodedMotion); err != nil {
		t.Fatalf("ApplyMotionDelta failed: %v", err)
	}

	// Canvas must match frame1 exactly cell-by-cell!
	canvas := dec.Canvas()
	for i := 0; i < totalCells; i++ {
		if canvas[i].FG != frame1[i].FG || !bytes.Equal(canvas[i].Char, frame1[i].Char) {
			t.Fatalf("reconstructed cell %d mismatch: got %+v, want %+v", i, canvas[i], frame1[i])
		}
	}
}

func TestMotionFrameEncodeDecodeRoundTrip(t *testing.T) {
	mf := &MotionFrame{
		BlockSize:  4,
		GridWidth:  2,
		GridHeight: 2,
		Vectors: []MotionVector{
			{DX: 0, DY: 0},
			{DX: -2, DY: 1},
			{DX: 1, DY: -2},
			{DX: 0, DY: 0},
		},
		Residuals: []BlockResidual{
			{
				BlockIdx: 1,
				RelX:     0,
				RelY:     1,
				Cell: CellState{
					FG:   RGB{10, 20, 30},
					BG:   RGB{40, 50, 60},
					Char: GlyphHalfBlock,
				},
			},
		},
	}

	encoded := mf.Encode()
	decoded, err := DecodeMotionFrame(encoded, 8, 8)
	if err != nil {
		t.Fatalf("DecodeMotionFrame failed: %v", err)
	}

	if decoded.BlockSize != mf.BlockSize || decoded.GridWidth != mf.GridWidth || decoded.GridHeight != mf.GridHeight {
		t.Errorf("header mismatch: %+v vs %+v", decoded, mf)
	}
	if len(decoded.Vectors) != len(mf.Vectors) {
		t.Fatalf("vector count mismatch: %d vs %d", len(decoded.Vectors), len(mf.Vectors))
	}
	for i := range mf.Vectors {
		if decoded.Vectors[i] != mf.Vectors[i] {
			t.Errorf("vector %d mismatch: got %+v, want %+v", i, decoded.Vectors[i], mf.Vectors[i])
		}
	}
	if len(decoded.Residuals) != len(mf.Residuals) {
		t.Fatalf("residuals count mismatch: %d vs %d", len(decoded.Residuals), len(mf.Residuals))
	}
	if decoded.Residuals[0].Cell.FG != mf.Residuals[0].Cell.FG ||
		!bytes.Equal(decoded.Residuals[0].Cell.Char, mf.Residuals[0].Cell.Char) {
		t.Errorf("residual mismatch: got %+v, want %+v", decoded.Residuals[0], mf.Residuals[0])
	}
}

func TestMotionCorruptionResilience(t *testing.T) {
	// 1. Truncated header
	if _, err := DecodeMotionFrame([]byte("MOT1"), 10, 10); !errors.Is(err, ErrCorruptMotionData) {
		t.Errorf("expected ErrCorruptMotionData on truncated header, got %v", err)
	}

	// 2. Invalid magic
	badMagic := make([]byte, 20)
	copy(badMagic[0:4], "BAD!")
	badMagic[4] = 4
	if _, err := DecodeMotionFrame(badMagic, 8, 8); !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}

	// 3. Out-of-bounds motion vector
	mf := &MotionFrame{
		BlockSize:  4,
		GridWidth:  2,
		GridHeight: 2,
		Vectors: []MotionVector{
			{DX: 50, DY: 0}, // Points far outside width 8!
			{DX: 0, DY: 0},
			{DX: 0, DY: 0},
			{DX: 0, DY: 0},
		},
	}
	encoded := mf.Encode()
	if _, err := DecodeMotionFrame(encoded, 8, 8); !errors.Is(err, ErrCorruptMotionData) {
		t.Errorf("expected ErrCorruptMotionData on out-of-bounds vector, got %v", err)
	}
}

func TestKeyframePackingRoundTrip(t *testing.T) {
	cells := []CellState{
		{FG: RGB{1, 2, 3}, BG: RGB{4, 5, 6}, Char: GlyphHalfBlock},
		{FG: RGB{10, 20, 30}, BG: RGB{40, 50, 60}, Char: GlyphHalfBlock},
	}

	packed := PackKeyframeCells(cells, 2, 1)
	unpacked, err := UnpackKeyframeCells(packed, 2, 1)
	if err != nil {
		t.Fatalf("UnpackKeyframeCells failed: %v", err)
	}

	if len(unpacked) != len(cells) {
		t.Fatalf("cell count mismatch: %d vs %d", len(unpacked), len(cells))
	}
	if unpacked[0].FG != cells[0].FG || unpacked[1].BG != cells[1].BG {
		t.Errorf("unpacked cells mismatch: %+v vs %+v", unpacked, cells)
	}
}

func TestDecoderPlaybackWorkflow(t *testing.T) {
	dec := NewDecoder(10, 5)

	cells := make([]CellState, 50)
	for i := range cells {
		cells[i] = CellState{FG: RGB{255, 0, 0}, BG: RGB{0, 0, 0}, Char: GlyphHalfBlock}
	}

	// 1. Packed keyframe packet
	kfPacket := &Packet{
		TimestampMs: 0,
		Type:        PacketTypeVideoKeyframe,
		Data:        PackKeyframeCells(cells, 10, 5),
	}

	ansi, err := dec.Decode(kfPacket)
	if err != nil {
		t.Fatalf("Decode keyframe failed: %v", err)
	}
	if len(ansi) == 0 {
		t.Errorf("expected non-empty ANSI from keyframe decode")
	}

	// 2. Motion delta packet
	mf := EstimateBlockMotion(cells, cells, 10, 5, 4, 4, 0)
	motionPkt := &Packet{
		TimestampMs: 33,
		Type:        PacketTypeMotionDelta,
		Data:        mf.Encode(),
	}

	deltaAnsi, err := dec.Decode(motionPkt)
	if err != nil {
		t.Fatalf("Decode motion delta failed: %v", err)
	}
	// Identical frame should produce 0 dirty delta bytes
	if len(deltaAnsi) != 0 {
		t.Errorf("expected 0 ANSI delta bytes for identical frame, got %d", len(deltaAnsi))
	}
}

func BenchmarkMotionEstimation(b *testing.B) {
	width, height := 80, 40
	totalCells := width * height
	f0 := make([]CellState, totalCells)
	f1 := make([]CellState, totalCells)

	for i := 0; i < totalCells; i++ {
		f0[i] = CellState{FG: RGB{uint8(i % 256), 100, 50}, BG: RGB{0, 0, 0}, Char: GlyphHalfBlock}
		f1[i] = CellState{FG: RGB{uint8((i + 2) % 256), 100, 50}, BG: RGB{0, 0, 0}, Char: GlyphHalfBlock}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EstimateBlockMotion(f1, f0, width, height, 4, 4, 10)
	}
}

func BenchmarkMotionDeltaDecoder(b *testing.B) {
	width, height := 80, 40
	totalCells := width * height
	f0 := make([]CellState, totalCells)
	f1 := make([]CellState, totalCells)

	for i := 0; i < totalCells; i++ {
		f0[i] = CellState{FG: RGB{uint8(i % 256), 100, 50}, BG: RGB{0, 0, 0}, Char: GlyphHalfBlock}
		f1[i] = CellState{FG: RGB{uint8((i + 2) % 256), 100, 50}, BG: RGB{0, 0, 0}, Char: GlyphHalfBlock}
	}

	mf := EstimateBlockMotion(f1, f0, width, height, 4, 4, 10)
	data := mf.Encode()

	dec := NewDecoder(width, height)
	_ = dec.ApplyKeyframe(f0)

	pkt := &Packet{
		TimestampMs: 33,
		Type:        PacketTypeMotionDelta,
		Data:        data,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = dec.Decode(pkt)
	}
}



