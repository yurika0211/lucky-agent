package computer

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
)

// fitScreenshot preserves the whole capture, reducing its resolution only when
// needed to fit the configured width or encoded byte budget. Ordinary frames
// pass through without decoding or re-encoding.
func fitScreenshot(ctx context.Context, obs Observation, maxWidth, maxBytes int) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return obs, err
	}
	size := int64(len(obs.ImageData))
	if size == 0 && obs.FilePath != "" {
		info, err := os.Stat(obs.FilePath)
		if err != nil {
			return obs, fmt.Errorf("computer: stat screenshot: %w", err)
		}
		size = info.Size()
	}
	if (maxWidth <= 0 || obs.Width <= maxWidth) && (maxBytes <= 0 || size <= int64(maxBytes)) {
		return obs, nil
	}
	var reader io.Reader = bytes.NewReader(obs.ImageData)
	if len(obs.ImageData) == 0 {
		f, err := os.Open(obs.FilePath)
		if err != nil {
			return obs, fmt.Errorf("computer: open screenshot for resizing: %w", err)
		}
		defer f.Close()
		reader = f
	}
	src, _, err := image.Decode(reader)
	if err != nil {
		return obs, fmt.Errorf("computer: decode screenshot for resizing: %w", err)
	}
	sourceWidth, sourceHeight := src.Bounds().Dx(), src.Bounds().Dy()
	width, height := sourceWidth, sourceHeight
	if maxWidth > 0 && width > maxWidth {
		width = maxWidth
		height = max(1, int(math.Round(float64(sourceHeight)*float64(width)/float64(sourceWidth))))
	}
	for {
		if err := ctx.Err(); err != nil {
			return obs, err
		}
		img := src
		if width != sourceWidth || height != sourceHeight {
			img, err = shrinkScreenshot(ctx, src, width, height)
			if err != nil {
				return obs, err
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			return obs, fmt.Errorf("computer: encode resized screenshot: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return obs, err
		}
		if maxBytes <= 0 || encoded.Len() <= maxBytes {
			obs.ImageData = encoded.Bytes()
			obs.MimeType = "image/png"
			obs.Width, obs.Height = width, height
			obs.sourceWidth, obs.sourceHeight = sourceWidth, sourceHeight
			if obs.ScaleFactor <= 0 {
				obs.ScaleFactor = 1
			}
			obs.ScaleFactor *= float64(width) / float64(sourceWidth)
			return obs, nil
		}
		if width == 1 && height == 1 {
			return obs, fmt.Errorf("computer: screenshot cannot fit configured maximum %d bytes even at 1x1; increase max_observation_bytes", maxBytes)
		}
		// Estimate the needed area reduction, with headroom for PNG overhead.
		// Always resample the original image to avoid cumulative quality loss.
		ratio := math.Min(0.9, math.Sqrt(float64(maxBytes)/float64(encoded.Len()))*0.9)
		scale := math.Min(float64(width)/float64(sourceWidth), float64(height)/float64(sourceHeight)) * ratio
		width = max(1, int(math.Floor(float64(sourceWidth)*scale)))
		height = max(1, int(math.Floor(float64(sourceHeight)*scale)))
	}
}

// shrinkScreenshot averages source pixel areas instead of discarding pixels.
// Each source pixel contributes to one output pixel, including all four edges.
// Only downscaling is used here; integer partitions keep the work linear in the
// capture's pixel count and avoid an additional image-processing dependency.
func shrinkScreenshot(ctx context.Context, src image.Image, width, height int) (*image.RGBA, error) {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	bounds := src.Bounds()
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		y0, y1 := bounds.Min.Y+y*bounds.Dy()/height, bounds.Min.Y+(y+1)*bounds.Dy()/height
		for x := 0; x < width; x++ {
			x0, x1 := bounds.Min.X+x*bounds.Dx()/width, bounds.Min.X+(x+1)*bounds.Dx()/width
			var r, g, b, a uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(sx, sy).RGBA()
					r, g, b, a = r+uint64(pr), g+uint64(pg), b+uint64(pb), a+uint64(pa)
				}
			}
			n := uint64((x1 - x0) * (y1 - y0))
			dst.SetRGBA(x, y, color.RGBA{R: uint8((r / n) >> 8), G: uint8((g / n) >> 8), B: uint8((b / n) >> 8), A: uint8((a / n) >> 8)})
		}
	}
	return dst, nil
}

// desktopAction converts only pointer positions from the returned image into
// the backend's original capture coordinates. Separate axis ratios account for
// rounding the scaled image dimensions; wheel deltas and keys are unchanged.
func desktopAction(action Action, obs Observation) Action {
	mapPoint := func(x, y int) (int, int) {
		if obs.sourceWidth > 0 && obs.sourceHeight > 0 && obs.Width > 0 && obs.Height > 0 {
			x = min(obs.sourceWidth-1, int(math.Round(float64(x)*float64(obs.sourceWidth)/float64(obs.Width))))
			y = min(obs.sourceHeight-1, int(math.Round(float64(y)*float64(obs.sourceHeight)/float64(obs.Height))))
		}
		return x + obs.OriginX, y + obs.OriginY
	}
	switch action.Kind {
	case ActionClick, ActionDoubleClick, ActionMove:
		action.X, action.Y = mapPoint(action.X, action.Y)
	case ActionDrag:
		action.X, action.Y = mapPoint(action.X, action.Y)
		action.EndX, action.EndY = mapPoint(action.EndX, action.EndY)
	}
	return action
}

func cropScreenshot(ctx context.Context, obs Observation, region *Rect) (Observation, error) {
	if region == nil {
		return obs, nil
	}
	if err := ctx.Err(); err != nil {
		return obs, err
	}
	if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 || region.X > obs.Width-region.Width || region.Y > obs.Height-region.Height {
		return obs, fmt.Errorf("computer: region %+v is outside capture %dx%d", *region, obs.Width, obs.Height)
	}
	var reader io.Reader = bytes.NewReader(obs.ImageData)
	if len(obs.ImageData) == 0 {
		f, err := os.Open(obs.FilePath)
		if err != nil {
			return obs, err
		}
		defer f.Close()
		reader = f
	}
	src, _, err := image.Decode(reader)
	if err != nil {
		return obs, fmt.Errorf("computer: decode region: %w", err)
	}
	area := image.Rect(region.X, region.Y, region.X+region.Width, region.Y+region.Height)
	if !area.In(src.Bounds()) {
		return obs, fmt.Errorf("computer: region is outside image data")
	}
	dst := image.NewRGBA(image.Rect(0, 0, region.Width, region.Height))
	draw.Draw(dst, dst.Bounds(), src, area.Min, draw.Src)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return obs, err
	}
	obs.ImageData, obs.MimeType = out.Bytes(), "image/png"
	obs.Width, obs.Height = region.Width, region.Height
	obs.OriginX, obs.OriginY = obs.OriginX+region.X, obs.OriginY+region.Y
	return obs, nil
}
