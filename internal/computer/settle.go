package computer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
	"time"
)

func discardCapture(obs Observation) {
	if obs.CleanupFile && obs.FilePath != "" {
		_ = os.Remove(obs.FilePath)
	}
}

// Fingerprint PNG image chunks only: capture timestamps and text metadata do
// not represent a visual change. This avoids decoding millions of pixels merely
// to decide whether another wait is necessary.
func screenshotFingerprint(obs Observation) ([32]byte, error) {
	var reader io.Reader = bytes.NewReader(obs.ImageData)
	if len(obs.ImageData) == 0 {
		f, err := os.Open(obs.FilePath)
		if err != nil {
			return [32]byte{}, err
		}
		defer f.Close()
		reader = f
	}
	h := sha256.New()
	var header [8]byte
	n, err := io.ReadFull(reader, header[:])
	if err != nil || string(header[:]) != "\x89PNG\r\n\x1a\n" {
		_, _ = h.Write(header[:n])
		if _, err := io.Copy(h, reader); err != nil {
			return [32]byte{}, err
		}
	} else {
		for {
			if _, err := io.ReadFull(reader, header[:]); err != nil {
				return [32]byte{}, err
			}
			length, kind := int64(binary.BigEndian.Uint32(header[:4])), string(header[4:])
			var dst io.Writer = io.Discard
			if kind == "IHDR" || kind == "PLTE" || kind == "tRNS" || kind == "IDAT" {
				dst = h
				_, _ = h.Write(header[:])
			}
			if _, err := io.CopyN(dst, reader, length); err != nil {
				return [32]byte{}, err
			}
			if _, err := io.CopyN(io.Discard, reader, 4); err != nil {
				return [32]byte{}, err
			}
			if kind == "IEND" {
				break
			}
		}
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

func (m *Manager) captureSettled(ctx context.Context, target Target, settle bool) (Observation, error) {
	if !settle || m.config.Settle <= 0 {
		return m.backend.Capture(ctx, target)
	}
	if m.config.SettleMode == "fixed" {
		if err := waitContext(ctx, m.config.Settle); err != nil {
			return Observation{}, err
		}
		return m.backend.Capture(ctx, target)
	}
	deadline := time.Now().Add(m.config.Settle)
	current, err := m.backend.Capture(ctx, target)
	if err != nil {
		return Observation{}, err
	}
	previous, err := screenshotFingerprint(current)
	if err != nil {
		discardCapture(current)
		return Observation{}, err
	}
	interval := min(80*time.Millisecond, m.config.Settle/3)
	interval = max(time.Millisecond, interval)
	stable := 0
	for time.Now().Before(deadline) {
		if err := waitContext(ctx, min(interval, time.Until(deadline))); err != nil {
			discardCapture(current)
			return Observation{}, err
		}
		if !time.Now().Before(deadline) {
			break
		}
		next, err := m.backend.Capture(ctx, target)
		if err != nil {
			discardCapture(current)
			return Observation{}, err
		}
		nextHash, err := screenshotFingerprint(next)
		if err != nil {
			discardCapture(current)
			discardCapture(next)
			return Observation{}, err
		}
		unchanged := previous == nextHash && current.OriginX == next.OriginX && current.OriginY == next.OriginY && current.WindowID == next.WindowID && current.ActiveWindow == next.ActiveWindow
		discardCapture(current)
		current, previous = next, nextHash
		if unchanged {
			stable++
		} else {
			stable = 0
		}
		if stable >= 2 {
			current.Stable = true
			return current, nil
		}
	}
	return current, nil
}
