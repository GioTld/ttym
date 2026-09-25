package ttym

import (
	"fmt"
)

// AudioConfig defines the audio track parameters in the container.
type AudioConfig struct {
	Codec      uint8
	Channels   uint8
	SampleRate uint32
}

// Validate checks if the audio parameters conform to ttym specifications.
func (c AudioConfig) Validate() error {
	if c.Codec == AudioCodecNone {
		if c.Channels != 0 || c.SampleRate != 0 {
			return fmt.Errorf("%w: channels and sample rate must be 0 when codec is None", ErrInvalidAudioConfig)
		}
		return nil
	}

	switch c.Codec {
	case AudioCodecPCM, AudioCodecOpus, AudioCodecADPCM:
	default:
		return fmt.Errorf("%w: unsupported codec ID %d", ErrInvalidAudioConfig, c.Codec)
	}

	if c.Channels == 0 || c.Channels > MaxChannels {
		return fmt.Errorf("%w: channels %d out of bounds (1-%d)", ErrInvalidAudioConfig, c.Channels, MaxChannels)
	}

	if c.SampleRate < 8000 || c.SampleRate > MaxSampleRate {
		return fmt.Errorf("%w: sample rate %d out of bounds (8000-%d)", ErrInvalidAudioConfig, c.SampleRate, MaxSampleRate)
	}

	return nil
}

// CodecName returns the display name for the audio codec.
func (c AudioConfig) CodecName() string {
	switch c.Codec {
	case AudioCodecNone:
		return "None"
	case AudioCodecPCM:
		return "PCM (s16le)"
	case AudioCodecOpus:
		return "Opus"
	case AudioCodecADPCM:
		return "ADPCM"
	default:
		return fmt.Sprintf("Unknown (%d)", c.Codec)
	}
}
