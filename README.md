# ttym

> High-performance binary media container and temporal delta codec for terminal streaming.

`ttym` is a standalone Go library and bitstream specification for encoding, compressing, and streaming video and audio directly to modern ANSI terminal emulators. It provides subpixel spatial sampling (half-blocks, quarter-blocks), perceptual Oklab color quantization, temporal delta compression, and streaming zstd container packaging.

---

## Features

- **Format Specification**: Clean binary format with fixed headers, zstd-compressed GOP chunks, and trailer seek indexes.
- **Multiplexed Audio Tracks**: Interleaved audio packets (Raw PCM s16le, Opus, ADPCM) synchronized within GOP chunks with zero container overhead.
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
- Resolution, framerate, duration, color mode, and audio track parameters
- GOP chunk counts and uncompressed/compressed ratios
- Trailer seek index table

### Validate a bitstream

```bash
ttym validate movie.ttym
```

Performs full frame-by-frame and packet-by-packet integrity and decompression checks.

---

## Usage in Go

### Streaming Demuxer & Iteration

```go
package main

import (
	"errors"
	"io"
	"os"

	"github.com/GioTld/ttym"
)

func main() {
	f, _ := os.Open("movie.ttym")
	defer f.Close()

	reader, err := ttym.NewReader(f)
	if err != nil {
		panic(err)
	}
	defer reader.Close()

	for {
		packet, err := reader.NextPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			panic(err)
		}

		if packet.IsVideo() {
			os.Stdout.Write(packet.Data)
		} else if packet.IsAudio() {
			// dispatch audio packet (PCM, Opus, ADPCM) to sound driver
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
