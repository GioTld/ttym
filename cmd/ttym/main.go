package main

import (
	"fmt"
	"os"
	"time"

	"github.com/GioTld/ttym"
	"github.com/klauspost/compress/zstd"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "inspect":
		handleInspect(os.Args[2:])
	case "validate":
		handleValidate(os.Args[2:])
	case "version":
		fmt.Printf("ttym specification v%d (magic: %s, ext: %s)\n",
			ttym.CurrentVersion, string(ttym.MagicFile[:]), ttym.DefaultExtension)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`ttym - Terminal Media Container & Codec Utility

Usage:
  ttym <command> [arguments]

Commands:
  inspect   Display header metadata, chunks, and seek index of a .ttym file
  validate  Verify bitstream integrity and chunk boundaries of a .ttym file
  version   Show format specification and version info`)
}

func handleInspect(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: ttym inspect <file.ttym>")
		os.Exit(1)
	}

	path := args[0]
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	hdr, err := ttym.ReadHeader(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading header: %v\n", err)
		os.Exit(1)
	}

	fi, _ := f.Stat()
	fileSize := int64(0)
	if fi != nil {
		fileSize = fi.Size()
	}

	colorModeStr := "Truecolor RGB24"
	if hdr.ColorMode == ttym.ColorModeANSI256 {
		colorModeStr = "ANSI 256 Palette"
	}

	duration := time.Duration(hdr.DurationMs) * time.Millisecond

	fmt.Printf("File: %s (%.1f KB)\n", path, float64(fileSize)/1024.0)
	fmt.Println("Header Metadata:")
	fmt.Printf("  • Version:      %d\n", hdr.Version)
	fmt.Printf("  • Color Mode:   %s (%d)\n", colorModeStr, hdr.ColorMode)
	fmt.Printf("  • Resolution:   %dx%d cells\n", hdr.Width, hdr.Height)
	fmt.Printf("  • Framerate:    %d fps\n", hdr.FPS)
	fmt.Printf("  • Duration:     %v (%d ms)\n", duration, hdr.DurationMs)
	fmt.Printf("  • Total Frames: %d\n", hdr.TotalFrames)
	fmt.Printf("  • GOP Chunks:   %d\n", hdr.ChunkCount)
	if hdr.Metadata != "" {
		fmt.Printf("  • Title / Meta: %s\n", hdr.Metadata)
	}

	if hdr.ChunkCount > 0 {
		entries, err := ttym.ReadTrailer(f, hdr.ChunkCount)
		if err != nil {
			fmt.Printf("\nTrailer Index: error reading (%v)\n", err)
			return
		}
		fmt.Printf("\nTrailer Index (%d chunks):\n", len(entries))
		limit := len(entries)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			fmt.Printf("  [%03d] Timestamp: %d ms | Offset: %d bytes\n",
				i, entries[i].StartTimestampMs, entries[i].FileOffset)
		}
		if len(entries) > limit {
			fmt.Printf("  ... and %d more chunks\n", len(entries)-limit)
		}
	}
}

func handleValidate(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: ttym validate <file.ttym>")
		os.Exit(1)
	}

	path := args[0]
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	hdr, err := ttym.ReadHeader(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validation failed: invalid header: %v\n", err)
		os.Exit(1)
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validation failed: zstd init error: %v\n", err)
		os.Exit(1)
	}
	defer dec.Close()

	frameCount := 0
	for i := uint32(0); i < hdr.ChunkCount; i++ {
		chunk, err := ttym.ReadChunk(f, dec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "validation failed: error reading chunk %d: %v\n", i, err)
			os.Exit(1)
		}
		if len(chunk.Frames) == 0 {
			fmt.Fprintf(os.Stderr, "validation failed: chunk %d has zero frames\n", i)
			os.Exit(1)
		}
		if chunk.Frames[0].Type != ttym.FrameTypeKeyframe {
			fmt.Fprintf(os.Stderr, "validation failed: chunk %d first frame is not a Keyframe\n", i)
			os.Exit(1)
		}
		frameCount += len(chunk.Frames)
	}

	entries, err := ttym.ReadTrailer(f, hdr.ChunkCount)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validation failed: trailer index error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ File %s is valid: %d chunks, %d frames verified.\n", path, len(entries), frameCount)
}
