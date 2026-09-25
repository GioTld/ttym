package ttym

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// ReadHeader deserializes and validates the Header from r.
// Accepts both the canonical MagicFile ('TTYM') and the legacy dev placeholder ('GIOV').
func ReadHeader(r io.Reader) (*Header, error) {
	var fixed [FileHeaderFixedSize]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return nil, err
	}

	magic := fixed[0:4]
	if !bytes.Equal(magic, MagicFile[:]) && !bytes.Equal(magic, LegacyMagicFile[:]) {
		return nil, ErrInvalidMagic
	}

	version := fixed[4]
	if version != CurrentVersion {
		return nil, fmt.Errorf("%w: got %d, expected %d", ErrUnsupportedVersion, version, CurrentVersion)
	}

	h := &Header{
		Version:     version,
		ColorMode:   fixed[5],
		Width:       binary.BigEndian.Uint16(fixed[6:8]),
		Height:      binary.BigEndian.Uint16(fixed[8:10]),
		FPS:         binary.BigEndian.Uint16(fixed[10:12]),
		TotalFrames: binary.BigEndian.Uint32(fixed[12:16]),
		DurationMs:  binary.BigEndian.Uint32(fixed[16:20]),
		ChunkCount:  binary.BigEndian.Uint32(fixed[20:24]),
	}

	if h.Width == 0 || h.Height == 0 || h.FPS == 0 {
		return nil, ErrInvalidDimensions
	}
	if h.Width > MaxDimension || h.Height > MaxDimension || h.FPS > MaxFPS {
		return nil, ErrInvalidDimensions
	}

	metaLen := binary.BigEndian.Uint16(fixed[30:32])
	if metaLen > 0 {
		metaBuf := make([]byte, metaLen)
		if _, err := io.ReadFull(r, metaBuf); err != nil {
			return nil, err
		}
		h.Metadata = string(metaBuf)
	}

	return h, nil
}

// ReadChunk deserializes and decompresses a Chunk from r using dec.
// Enforces decompression bomb limits and per-frame bounds checks.
func ReadChunk(r io.Reader, dec *zstd.Decoder) (*Chunk, error) {
	var chkHdr [ChunkHeaderSize]byte
	if _, err := io.ReadFull(r, chkHdr[:]); err != nil {
		return nil, err
	}

	if !bytes.Equal(chkHdr[0:4], MagicChunk[:]) {
		return nil, ErrInvalidMagic
	}

	startMs := binary.BigEndian.Uint32(chkHdr[4:8])
	endMs := binary.BigEndian.Uint32(chkHdr[8:12])
	frameCount := binary.BigEndian.Uint16(chkHdr[12:14])
	uncompressedSize := binary.BigEndian.Uint32(chkHdr[14:18])
	compressedSize := binary.BigEndian.Uint32(chkHdr[18:22])

	if uncompressedSize > MaxDecompressedChunkBytes {
		return nil, fmt.Errorf("%w: declared uncompressed size %d > cap %d",
			ErrDecompressionBomb, uncompressedSize, MaxDecompressedChunkBytes)
	}

	compressed := make([]byte, compressedSize)
	if _, err := io.ReadFull(r, compressed); err != nil {
		return nil, err
	}

	uncompressed, err := dec.DecodeAll(compressed, make([]byte, 0, uncompressedSize))
	if err != nil {
		return nil, fmt.Errorf("ttym: zstd decompress error: %w", err)
	}

	if uint32(len(uncompressed)) != uncompressedSize {
		return nil, fmt.Errorf("%w: uncompressed size mismatch (expected %d, got %d)",
			ErrCorruptData, uncompressedSize, len(uncompressed))
	}

	frames := make([]Frame, 0, frameCount)
	reader := bytes.NewReader(uncompressed)
	for i := 0; i < int(frameCount); i++ {
		var fhdr [FrameHeaderSize]byte
		if _, err := io.ReadFull(reader, fhdr[:]); err != nil {
			return nil, ErrCorruptData
		}
		ts := binary.BigEndian.Uint32(fhdr[0:4])
		ftype := fhdr[4]
		dataLen := binary.BigEndian.Uint32(fhdr[5:9])

		if int64(dataLen) > reader.Size()-int64(reader.Len()) && int64(dataLen) > int64(reader.Len()) {
			return nil, fmt.Errorf("%w: frame data length %d exceeds remaining chunk buffer", ErrCorruptData, dataLen)
		}

		data := make([]byte, dataLen)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, ErrCorruptData
		}

		frames = append(frames, Frame{
			TimestampMs: ts,
			Type:        ftype,
			Data:        data,
		})
	}

	return &Chunk{
		StartTimestampMs: startMs,
		EndTimestampMs:   endMs,
		Frames:           frames,
	}, nil
}

// ReadTrailer reads the trailer footer from the last 16 bytes and deserializes the index table with offset validation.
func ReadTrailer(r io.ReadSeeker, chunkCount uint32) ([]IndexEntry, error) {
	fileSize, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}

	if fileSize < int64(FileHeaderFixedSize+TrailerFooterSize) {
		return nil, ErrCorruptData
	}

	if _, err := r.Seek(-int64(TrailerFooterSize), io.SeekEnd); err != nil {
		return nil, err
	}

	var footer [TrailerFooterSize]byte
	if _, err := io.ReadFull(r, footer[:]); err != nil {
		return nil, err
	}

	if !bytes.Equal(footer[8:12], MagicTrailer[:]) {
		return nil, ErrInvalidMagic
	}
	if footer[12] != CurrentVersion {
		return nil, ErrUnsupportedVersion
	}

	indexOffset := binary.BigEndian.Uint64(footer[0:8])
	expectedIndexSize := uint64(chunkCount) * 12
	if indexOffset+expectedIndexSize > uint64(fileSize-int64(TrailerFooterSize)) {
		return nil, fmt.Errorf("%w: index table offset %d + size %d exceeds file boundary",
			ErrInvalidTrailer, indexOffset, expectedIndexSize)
	}

	if _, err := r.Seek(int64(indexOffset), io.SeekStart); err != nil {
		return nil, err
	}

	entries := make([]IndexEntry, chunkCount)
	entryBuf := make([]byte, 12)
	for i := 0; i < int(chunkCount); i++ {
		if _, err := io.ReadFull(r, entryBuf); err != nil {
			return nil, ErrCorruptData
		}
		entries[i] = IndexEntry{
			FileOffset:       binary.BigEndian.Uint64(entryBuf[0:8]),
			StartTimestampMs: binary.BigEndian.Uint32(entryBuf[8:12]),
		}
	}

	return entries, nil
}
