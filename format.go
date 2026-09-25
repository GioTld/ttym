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
	ErrInvalidAudioConfig = errors.New("ttym: invalid audio configuration")
	ErrAudioPacketTooLarge = errors.New("ttym: audio packet exceeds safety limit")
)

const (
	CurrentVersion = 1

	ColorModeTruecolor = 1
	ColorModeANSI256   = 2

	PacketTypeVideoKeyframe = 1
	PacketTypeVideoDelta    = 2
	PacketTypeAudio         = 3

	FrameTypeKeyframe = PacketTypeVideoKeyframe
	FrameTypeDelta    = PacketTypeVideoDelta

	AudioCodecNone  = 0
	AudioCodecPCM   = 1
	AudioCodecOpus  = 2
	AudioCodecADPCM = 3

	FileHeaderFixedSize = 32
	ChunkHeaderSize     = 22
	FrameHeaderSize     = 9
	TrailerFooterSize   = 16

	// Safety thresholds to prevent resource exhaustion from corrupt/malicious inputs.
	MaxDimension              = 2048
	MaxFPS                    = 120
	MaxMetadataLen            = 65535
	MaxDecompressedChunkBytes = 64 * 1024 * 1024 // 64 MB maximum uncompressed buffer per chunk
	MaxSampleRate             = 192000
	MaxChannels               = 8
	MaxAudioPacketBytes       = 1024 * 1024 // 1 MB per audio packet
)

// Header represents the file-level metadata of a .ttym media file.
type Header struct {
	Version         uint8
	ColorMode       uint8
	Width           uint16
	Height          uint16
	FPS             uint16
	TotalFrames     uint32
	DurationMs      uint32
	ChunkCount      uint32
	AudioCodec      uint8
	AudioChannels   uint8
	AudioSampleRate uint32
	Metadata        string
}

// Packet represents a discrete media unit (video frame or audio packet) within a chunk.
type Packet struct {
	TimestampMs uint32
	Type        uint8 // PacketTypeVideoKeyframe, PacketTypeVideoDelta, or PacketTypeAudio
	Data        []byte
}

func (p Packet) IsVideo() bool {
	return p.Type == PacketTypeVideoKeyframe || p.Type == PacketTypeVideoDelta
}

func (p Packet) IsKeyframe() bool {
	return p.Type == PacketTypeVideoKeyframe
}

func (p Packet) IsAudio() bool {
	return p.Type == PacketTypeAudio
}

// Frame is a type alias for Packet maintaining full backward compatibility.
type Frame = Packet

// Chunk represents a GOP (Group of Pictures) block containing interleaved frames and packets.
type Chunk struct {
	StartTimestampMs uint32
	EndTimestampMs   uint32
	Frames           []Frame
}

func (c *Chunk) Packets() []Packet {
	return c.Frames
}

// IndexEntry maps a chunk's start timestamp to its absolute file offset.
type IndexEntry struct {
	FileOffset       uint64
	StartTimestampMs uint32
}
