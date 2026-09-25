package ttym

import (
	"bytes"
	"encoding/binary"
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
	// buf[24:30] reserved zeroes
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
