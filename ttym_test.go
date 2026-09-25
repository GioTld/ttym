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


