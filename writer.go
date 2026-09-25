package ttym

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Write serializes the Header in Big-Endian binary format to w using the canonical MagicFile.
func (h *Header) Write(w io.Writer) error {
	if h.Width == 0 || h.Height == 0 || h.FPS == 0 {
		return ErrInvalidDimensions
	}
	if h.Width > MaxDimension || h.Height > MaxDimension || h.FPS > MaxFPS {
		return ErrInvalidDimensions
	}

	metaBytes := []byte(h.Metadata)
	if len(metaBytes) > MaxMetadataLen {
		return ErrMetadataTooLarge
	}

	audioCfg := AudioConfig{
		Codec:      h.AudioCodec,
		Channels:   h.AudioChannels,
		SampleRate: h.AudioSampleRate,
	}
	if err := audioCfg.Validate(); err != nil {
		return err
	}

	buf := make([]byte, FileHeaderFixedSize+len(metaBytes))
	copy(buf[0:4], MagicFile[:])
	buf[4] = CurrentVersion
	buf[5] = h.ColorMode
	binary.BigEndian.PutUint16(buf[6:8], h.Width)
	binary.BigEndian.PutUint16(buf[8:10], h.Height)
	binary.BigEndian.PutUint16(buf[10:12], h.FPS)
	binary.BigEndian.PutUint32(buf[12:16], h.TotalFrames)
	binary.BigEndian.PutUint32(buf[16:20], h.DurationMs)
	binary.BigEndian.PutUint32(buf[20:24], h.ChunkCount)
	buf[24] = h.AudioCodec
	buf[25] = h.AudioChannels
	binary.BigEndian.PutUint32(buf[26:30], h.AudioSampleRate)
	binary.BigEndian.PutUint16(buf[30:32], uint16(len(metaBytes)))
	copy(buf[32:], metaBytes)

	_, err := w.Write(buf)
	return err
}

// WriteChunk encodes and writes a Chunk with zstd compression to w.
func WriteChunk(w io.Writer, chunk *Chunk, enc *zstd.Encoder) error {
	var rawBuf bytes.Buffer
	for _, f := range chunk.Frames {
		var fhdr [FrameHeaderSize]byte
		binary.BigEndian.PutUint32(fhdr[0:4], f.TimestampMs)
		fhdr[4] = f.Type
		binary.BigEndian.PutUint32(fhdr[5:9], uint32(len(f.Data)))
		rawBuf.Write(fhdr[:])
		rawBuf.Write(f.Data)
	}

	uncompressed := rawBuf.Bytes()
	compressed := enc.EncodeAll(uncompressed, make([]byte, 0, len(uncompressed)))

	var chkHdr [ChunkHeaderSize]byte
	copy(chkHdr[0:4], MagicChunk[:])
	binary.BigEndian.PutUint32(chkHdr[4:8], chunk.StartTimestampMs)
	binary.BigEndian.PutUint32(chkHdr[8:12], chunk.EndTimestampMs)
	binary.BigEndian.PutUint16(chkHdr[12:14], uint16(len(chunk.Frames)))
	binary.BigEndian.PutUint32(chkHdr[14:18], uint32(len(uncompressed)))
	binary.BigEndian.PutUint32(chkHdr[18:22], uint32(len(compressed)))

	if _, err := w.Write(chkHdr[:]); err != nil {
		return err
	}
	_, err := w.Write(compressed)
	return err
}

// WriteTrailer writes the index table and the fixed 16-byte footer to w.
func WriteTrailer(w io.Writer, indexOffset uint64, entries []IndexEntry) error {
	entryBuf := make([]byte, 12)
	for _, entry := range entries {
		binary.BigEndian.PutUint64(entryBuf[0:8], entry.FileOffset)
		binary.BigEndian.PutUint32(entryBuf[8:12], entry.StartTimestampMs)
		if _, err := w.Write(entryBuf); err != nil {
			return err
		}
	}

	var footer [TrailerFooterSize]byte
	binary.BigEndian.PutUint64(footer[0:8], indexOffset)
	copy(footer[8:12], MagicTrailer[:])
	footer[12] = CurrentVersion
	// footer[13:16] zero reserved

	_, err := w.Write(footer[:])
	return err
}

type countWriter struct {
	w io.Writer
	n uint64
}

func (cw *countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += uint64(n)
	return n, err
}

// Writer multiplexes video and audio packets into a sequential .ttym bitstream with GOP chunks.
type Writer struct {
	cw            *countWriter
	enc           *zstd.Encoder
	header        Header
	gopDurationMs uint32
	currentChunk  Chunk
	indexEntries  []IndexEntry
	closed        bool
}

// NewWriter creates a streaming Writer and serializes the container Header.
func NewWriter(w io.Writer, h Header, gopDurationMs uint32) (*Writer, error) {
	cw := &countWriter{w: w}
	if err := h.Write(cw); err != nil {
		return nil, err
	}

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}

	if gopDurationMs == 0 {
		gopDurationMs = 5000
	}

	return &Writer{
		cw:            cw,
		enc:           enc,
		header:        h,
		gopDurationMs: gopDurationMs,
	}, nil
}

// WritePacket writes a discrete media packet into the active GOP chunk.
func (w *Writer) WritePacket(pkt Packet) error {
	if w.closed {
		return errors.New("ttym: writer closed")
	}

	if pkt.IsAudio() && len(pkt.Data) > MaxAudioPacketBytes {
		return ErrAudioPacketTooLarge
	}

	// Auto-flush chunk when a new keyframe arrives and GOP target duration is reached
	if pkt.IsKeyframe() && len(w.currentChunk.Frames) > 0 {
		if pkt.TimestampMs >= w.currentChunk.StartTimestampMs+w.gopDurationMs {
			if err := w.FlushChunk(); err != nil {
				return err
			}
		}
	}

	if len(w.currentChunk.Frames) == 0 {
		w.currentChunk.StartTimestampMs = pkt.TimestampMs
	}
	w.currentChunk.EndTimestampMs = pkt.TimestampMs
	w.currentChunk.Frames = append(w.currentChunk.Frames, pkt)

	if pkt.IsVideo() {
		w.header.TotalFrames++
	}
	if pkt.TimestampMs > w.header.DurationMs {
		w.header.DurationMs = pkt.TimestampMs
	}

	return nil
}

// WriteVideoKeyframe writes a video keyframe packet.
func (w *Writer) WriteVideoKeyframe(timestampMs uint32, data []byte) error {
	return w.WritePacket(Packet{
		TimestampMs: timestampMs,
		Type:        PacketTypeVideoKeyframe,
		Data:        data,
	})
}

// WriteVideoDelta writes a video delta packet.
func (w *Writer) WriteVideoDelta(timestampMs uint32, data []byte) error {
	return w.WritePacket(Packet{
		TimestampMs: timestampMs,
		Type:        PacketTypeVideoDelta,
		Data:        data,
	})
}

// WriteAudioPacket writes an interleaved audio packet.
func (w *Writer) WriteAudioPacket(timestampMs uint32, data []byte) error {
	return w.WritePacket(Packet{
		TimestampMs: timestampMs,
		Type:        PacketTypeAudio,
		Data:        data,
	})
}

// FlushChunk serializes and compresses the current GOP chunk.
func (w *Writer) FlushChunk() error {
	if len(w.currentChunk.Frames) == 0 {
		return nil
	}

	offset := w.cw.n
	if err := WriteChunk(w.cw, &w.currentChunk, w.enc); err != nil {
		return err
	}

	w.indexEntries = append(w.indexEntries, IndexEntry{
		FileOffset:       offset,
		StartTimestampMs: w.currentChunk.StartTimestampMs,
	})

	w.currentChunk = Chunk{}
	return nil
}

// Close flushes any pending chunk, writes the index trailer, and releases encoder resources.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.enc.Close()

	if err := w.FlushChunk(); err != nil {
		return err
	}

	w.header.ChunkCount = uint32(len(w.indexEntries))

	// If underlying writer supports Seek, update header metrics (TotalFrames, DurationMs, ChunkCount)
	if ws, ok := w.cw.w.(io.WriteSeeker); ok {
		currentPos, seekErr := ws.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			if _, seekErr = ws.Seek(12, io.SeekStart); seekErr == nil {
				var countBuf [12]byte
				binary.BigEndian.PutUint32(countBuf[0:4], w.header.TotalFrames)
				binary.BigEndian.PutUint32(countBuf[4:8], w.header.DurationMs)
				binary.BigEndian.PutUint32(countBuf[8:12], w.header.ChunkCount)
				_, _ = ws.Write(countBuf[:])
				_, _ = ws.Seek(currentPos, io.SeekStart)
			}
		}
	}

	indexOffset := w.cw.n
	return WriteTrailer(w.cw, indexOffset, w.indexEntries)
}

