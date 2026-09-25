# ttym

> High-performance binary media container and temporal delta codec for terminal streaming.

`ttym` is a standalone Go library and bitstream specification for encoding, compressing, and streaming video and audio directly to modern ANSI terminal emulators. It provides subpixel spatial sampling (half-blocks, quarter-blocks), perceptual Oklab color quantization, temporal delta compression, and streaming zstd container packaging.

---

## Features

- **Format Specification**: Clean binary format with fixed headers, zstd-compressed GOP chunks, and trailer seek indexes.
- **Subpixel Block Rendering**:
  - **Half-Blocks (1×2)**: Canonical upper half-block (`▀`) sampling.
  - **Quarter-Blocks (2×2)**: 16 Unicode quadrant glyphs with 2-color k-means clustering for double horizontal resolution.
- **Color Science**:
  - Precomputed 32×32×32 Oklab color space lookup table for the 216-color palette.
  - Bayer 4×4 spatial dithering matrix.
  - Lossless 24-bit Truecolor RGB bypass.
- **Temporal Delta Engine**: Sparse ANSI diffing emitting only dirty cells with absolute cursor positioning (`\033[y;xH`).
- **Bitstream Safety**: Strict bounds checking, metadata length limits, and decompression bomb caps.
- **Zero Heavy Dependencies**: Pure Go with zero CGO or graphics dependencies.

---

## Installation

```bash
go get github.com/GioTld/ttym
```

---

## CLI Utility

Install the standalone inspection and validation tool:

```bash
go install github.com/GioTld/ttym/cmd/ttym@latest
```

### Inspect a .ttym file

```bash
ttym inspect movie.ttym
```

Outputs:
- Resolution, framerate, duration, and color mode
- GOP chunk counts and uncompressed/compressed ratios
- Trailer seek index table

### Validate a bitstream

```bash
ttym validate movie.ttym
```

Performs full frame-by-frame integrity and decompression checks.

---

## Usage in Go

### Reading and Streaming

```go
package main

import (
	"os"

	"github.com/GioTld/ttym"
	"github.com/klauspost/compress/zstd"
)

func main() {
	f, _ := os.Open("movie.ttym")
	defer f.Close()

	hdr, _ := ttym.ReadHeader(f)
	dec, _ := zstd.NewReader(nil)
	defer dec.Close()

	for i := uint32(0); i < hdr.ChunkCount; i++ {
		chunk, _ := ttym.ReadChunk(f, dec)
		for _, frame := range chunk.Frames {
			// frame.Data contains ready-to-write ANSI sequences
			os.Stdout.Write(frame.Data)
		}
	}
}
```

---

## Testing & Benchmarks

```bash
go test -v -race ./...
go test -bench=. -benchmem ./...
```

---

## License

MIT License.
