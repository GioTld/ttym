package ttym

import (
	"bytes"
	"encoding/binary"
	"errors"
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
		Version:         version,
		ColorMode:       fixed[5],
		Width:           binary.BigEndian.Uint16(fixed[6:8]),
		Height:          binary.BigEndian.Uint16(fixed[8:10]),
		FPS:             binary.BigEndian.Uint16(fixed[10:12]),
		TotalFrames:     binary.BigEndian.Uint32(fixed[12:16]),
		DurationMs:      binary.BigEndian.Uint32(fixed[16:20]),
		ChunkCount:      binary.BigEndian.Uint32(fixed[20:24]),
		AudioCodec:      fixed[24],
		AudioChannels:   fixed[25],
		AudioSampleRate: binary.BigEndian.Uint32(fixed[26:30]),
	}

	if h.Width == 0 || h.Height == 0 || h.FPS == 0 {
		return nil, ErrInvalidDimensions
	}
	if h.Width > MaxDimension || h.Height > MaxDimension || h.FPS > MaxFPS {
		return nil, ErrInvalidDimensions
	}

	audioCfg := AudioConfig{
		Codec:      h.AudioCodec,
		Channels:   h.AudioChannels,
		SampleRate: h.AudioSampleRate,
	}
	if err := audioCfg.Validate(); err != nil {
		return nil, err
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

		if ftype == PacketTypeAudio && dataLen > MaxAudioPacketBytes {
			return nil, fmt.Errorf("%w: audio packet size %d exceeds limit %d",
				ErrAudioPacketTooLarge, dataLen, MaxAudioPacketBytes)
		}
		if ftype == PacketTypeMotionDelta && dataLen < 13 {
			return nil, fmt.Errorf("%w: motion delta packet smaller than header (len %d)", ErrCorruptMotionData, dataLen)
		}

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
	maxIndexBytes := uint64(fileSize - int64(TrailerFooterSize))
	if indexOffset > maxIndexBytes {
		return nil, fmt.Errorf("%w: index table offset %d exceeds file boundary %d",
			ErrInvalidTrailer, indexOffset, maxIndexBytes)
	}

	availableBytes := maxIndexBytes - indexOffset
	if availableBytes%12 != 0 {
		return nil, fmt.Errorf("%w: trailer index size %d is not a multiple of 12",
			ErrInvalidTrailer, availableBytes)
	}

	entriesInTrailer := uint32(availableBytes / 12)
	if chunkCount == 0 {
		chunkCount = entriesInTrailer
	} else if chunkCount != entriesInTrailer {
		return nil, fmt.Errorf("%w: chunk count %d does not match index entries %d",
			ErrInvalidTrailer, chunkCount, entriesInTrailer)
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

// Reader provides high-level sequential demuxing and random seeking across a .ttym file or stream.
type Reader struct {
	r            io.Reader
	rs           io.ReadSeeker
	dec          *zstd.Decoder
	header       *Header
	trailer      []IndexEntry
	currentChunk *Chunk
	packetIdx    int
	chunkIdx     uint32
}

// NewReader opens a .ttym bitstream for demuxed reading.
// If r implements io.ReadSeeker, it also parses the trailer index for random seeking.
func NewReader(r io.Reader) (*Reader, error) {
	hdr, err := ReadHeader(r)
	if err != nil {
		return nil, err
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}

	var rs io.ReadSeeker
	var trailer []IndexEntry
	if s, ok := r.(io.ReadSeeker); ok {
		rs = s
		t, err := ReadTrailer(rs, hdr.ChunkCount)
		if err == nil {
			trailer = t
			hdr.ChunkCount = uint32(len(trailer))
		}
		firstChunkOffset := int64(FileHeaderFixedSize + len(hdr.Metadata))
		if len(trailer) > 0 {
			firstChunkOffset = int64(trailer[0].FileOffset)
		}
		if _, err := rs.Seek(firstChunkOffset, io.SeekStart); err != nil {
			dec.Close()
			return nil, err
		}
	}

	return &Reader{
		r:       r,
		rs:      rs,
		dec:     dec,
		header:  hdr,
		trailer: trailer,
	}, nil
}

// Header returns the file header metadata.
func (r *Reader) Header() *Header {
	return r.header
}

// Trailer returns the seek table index entries.
func (r *Reader) Trailer() []IndexEntry {
	return r.trailer
}

// NextPacket returns the next interleaved Packet (video or audio) in presentation order.
// Returns (nil, io.EOF) when all packets in all chunks have been consumed.
func (r *Reader) NextPacket() (*Packet, error) {
	for {
		if r.currentChunk != nil && r.packetIdx < len(r.currentChunk.Frames) {
			pkt := &r.currentChunk.Frames[r.packetIdx]
			r.packetIdx++
			return pkt, nil
		}

		if r.chunkIdx >= r.header.ChunkCount {
			return nil, io.EOF
		}

		chunk, err := ReadChunk(r.r, r.dec)
		if err != nil {
			return nil, err
		}

		r.currentChunk = chunk
		r.packetIdx = 0
		r.chunkIdx++
	}
}

// SeekTo locates the GOP chunk keyframe containing targetTimestampMs and repositions the stream.
func (r *Reader) SeekTo(targetTimestampMs uint32) error {
	if r.rs == nil || len(r.trailer) == 0 {
		return errors.New("ttym: cannot seek without seekable stream and trailer index")
	}

	idx := 0
	for i := len(r.trailer) - 1; i >= 0; i-- {
		if r.trailer[i].StartTimestampMs <= targetTimestampMs {
			idx = i
			break
		}
	}

	targetOffset := r.trailer[idx].FileOffset
	if _, err := r.rs.Seek(int64(targetOffset), io.SeekStart); err != nil {
		return err
	}

	r.chunkIdx = uint32(idx)
	r.currentChunk = nil
	r.packetIdx = 0
	return nil
}

// Close releases decoder resources.
func (r *Reader) Close() error {
	r.dec.Close()
	return nil
}

