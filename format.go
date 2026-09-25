package ttym

import (
	"errors"
)

var (
	// MagicFile is the official 4-byte container identifier for .ttym files.
	MagicFile = [4]byte{'T', 'T', 'Y', 'M'}

	// LegacyMagicFile is the development placeholder identifier ('GIOV'), accepted for backward compatibility.
	LegacyMagicFile = [4]byte{'G', 'I', 'O', 'V'}

	// MagicChunk is the 4-byte header identifier for a GOP chunk.
	MagicChunk = [4]byte{'C', 'H', 'N', 'K'}

	// MagicTrailer is the 4-byte trailer identifier for the index table footer.
	MagicTrailer = [4]byte{'T', 'T', 'Y', 'I'}

	// DefaultExtension is the canonical file extension for movies encoded with ttym.
	DefaultExtension = ".ttym"

	// Standard error definitions.
	ErrInvalidMagic       = errors.New("ttym: invalid magic bytes")
	ErrUnsupportedVersion = errors.New("ttym: unsupported version")
	ErrCorruptData        = errors.New("ttym: corrupt data")
	ErrDecompressionBomb  = errors.New("ttym: uncompressed chunk exceeds safety limit")
	ErrInvalidDimensions  = errors.New("ttym: invalid width, height, or framerate")
	ErrMetadataTooLarge   = errors.New("ttym: metadata exceeds 64KB")
	ErrInvalidTrailer     = errors.New("ttym: invalid trailer index offset or bounds")
)

const (
	CurrentVersion = 1

	ColorModeTruecolor = 1
	ColorModeANSI256   = 2

	FrameTypeKeyframe = 1
	FrameTypeDelta    = 2

	FileHeaderFixedSize = 32
	ChunkHeaderSize     = 22
	FrameHeaderSize     = 9
	TrailerFooterSize   = 16

	// Safety thresholds to prevent resource exhaustion from corrupt/malicious inputs.
	MaxDimension              = 2048
	MaxFPS                    = 120
	MaxMetadataLen            = 65535
	MaxDecompressedChunkBytes = 64 * 1024 * 1024 // 64 MB maximum uncompressed buffer per chunk
)

// Header represents the file-level metadata of a .ttym media file.
type Header struct {
	Version     uint8
	ColorMode   uint8
	Width       uint16
	Height      uint16
	FPS         uint16
	TotalFrames uint32
	DurationMs  uint32
	ChunkCount  uint32
	Metadata    string
}

// Frame represents a single frame of terminal ANSI escape sequence data.
type Frame struct {
	TimestampMs uint32
	Type        uint8 // FrameTypeKeyframe or FrameTypeDelta
	Data        []byte
}

// Chunk represents a GOP (Group of Pictures) block containing multiple contiguous frames.
type Chunk struct {
	StartTimestampMs uint32
	EndTimestampMs   uint32
	Frames           []Frame
}

// IndexEntry maps a chunk's start timestamp to its absolute file offset.
type IndexEntry struct {
	FileOffset       uint64
	StartTimestampMs uint32
}
