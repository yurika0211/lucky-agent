//go:build windows

package computer

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	winSMXVXScreen = 76
	winSMYVYScreen = 77
	winSMCXVScreen = 78
	winSMCYVScreen = 79

	winSRCCopy    = 0x00CC0020
	winCaptureBlt = 0x40000000
	winDIBRGB     = 0
	winBI_RGB     = 0

	winInputKeyboard   = 1
	winKeyEventUp      = 0x0002
	winKeyEventUnicode = 0x0004

	winMouseLeftDown   = 0x0002
	winMouseLeftUp     = 0x0004
	winMouseRightDown  = 0x0008
	winMouseRightUp    = 0x0010
	winMouseMiddleDown = 0x0020
	winMouseMiddleUp   = 0x0040
	winMouseWheel      = 0x0800
	winMouseHWheel     = 0x1000
)

var (
	winUser32 = windows.NewLazySystemDLL("user32.dll")
	winGDI32  = windows.NewLazySystemDLL("gdi32.dll")

	winGetSystemMetrics       = winUser32.NewProc("GetSystemMetrics")
	winGetDC                  = winUser32.NewProc("GetDC")
	winReleaseDC              = winUser32.NewProc("ReleaseDC")
	winSetCursorPos           = winUser32.NewProc("SetCursorPos")
	winMouseEvent             = winUser32.NewProc("mouse_event")
	winSendInput              = winUser32.NewProc("SendInput")
	winGetForegroundWindow    = winUser32.NewProc("GetForegroundWindow")
	winGetWindowTextW         = winUser32.NewProc("GetWindowTextW")
	winGetWindowRect          = winUser32.NewProc("GetWindowRect")
	winSetProcessDPIAware     = winUser32.NewProc("SetProcessDPIAware")
	winCreateCompatibleDC     = winGDI32.NewProc("CreateCompatibleDC")
	winDeleteDC               = winGDI32.NewProc("DeleteDC")
	winCreateCompatibleBitmap = winGDI32.NewProc("CreateCompatibleBitmap")
	winSelectObject           = winGDI32.NewProc("SelectObject")
	winDeleteObject           = winGDI32.NewProc("DeleteObject")
	winBitBlt                 = winGDI32.NewProc("BitBlt")
	winGetDIBits              = winGDI32.NewProc("GetDIBits")
	winEnableDPIAwarenessOnce sync.Once
)

// WindowsBackend captures the virtual desktop with GDI and injects input with
// SendInput. Both APIs operate in physical pixels after DPI awareness is set,
// matching the screenshot coordinate system exposed to the model.
type WindowsBackend struct{}

func NewWindowsBackend() (Backend, error) {
	winEnableDPIAwarenessOnce.Do(func() {
		// The process may already have a DPI mode selected by a host application.
		// In that case the call simply fails and the existing mode remains valid.
		_, _, _ = winSetProcessDPIAware.Call()
	})
	return &WindowsBackend{}, nil
}

func (b *WindowsBackend) Name() string { return "windows" }

func (b *WindowsBackend) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{
		Capture:       true,
		Click:         true,
		DoubleClick:   true,
		Move:          true,
		Drag:          true,
		TypeText:      true,
		Keypress:      true,
		Scroll:        true,
		MultiDisplay:  true,
		ScaleFactor:   1,
		BackendDetail: "Win32 GDI virtual desktop + SendInput",
	}, nil
}

func (b *WindowsBackend) Capture(ctx context.Context, target Target) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if strings.TrimSpace(target.Window) != "" {
		return Observation{}, fmt.Errorf("computer: windows window-scoped capture is not implemented: %q", target.Window)
	}
	if displayID := strings.TrimSpace(target.DisplayID); displayID != "" && !strings.EqualFold(displayID, "virtual") {
		return Observation{}, fmt.Errorf("computer: windows display-scoped capture is not implemented: %q", displayID)
	}

	x, y, width, height := windowsVirtualScreenBounds()
	if width <= 0 || height <= 0 {
		return Observation{}, fmt.Errorf("computer: windows virtual desktop has invalid size %dx%d", width, height)
	}
	imageData, err := windowsCapturePNG(x, y, width, height)
	if err != nil {
		return Observation{}, err
	}
	activeWindow, bounds := windowsForegroundWindow()
	return Observation{
		ImageData:     imageData,
		MimeType:      "image/png",
		Width:         width,
		Height:        height,
		ScaleFactor:   1,
		DisplayID:     "virtual",
		ActiveWindow:  activeWindow,
		WindowBounds:  bounds,
		CaptureBounds: Rect{X: x, Y: y, Width: width, Height: height},
	}, nil
}

func (b *WindowsBackend) ValidateObservation(ctx context.Context, obs Observation, _ Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	x, y, width, height := windowsVirtualScreenBounds()
	if obs.CaptureBounds != (Rect{X: x, Y: y, Width: width, Height: height}) {
		return fmt.Errorf("%w: virtual desktop geometry changed", ErrStaleFrame)
	}
	return nil
}

func (b *WindowsBackend) Perform(ctx context.Context, action Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := action.Validate(); err != nil {
		return err
	}
	x, y, _, _ := windowsVirtualScreenBounds()
	toScreen := func(actionX, actionY int) (int, int) { return x + actionX, y + actionY }

	switch action.Kind {
	case ActionMove:
		moveX, moveY := toScreen(action.X, action.Y)
		return windowsSetCursor(moveX, moveY)
	case ActionClick:
		moveX, moveY := toScreen(action.X, action.Y)
		if err := windowsSetCursor(moveX, moveY); err != nil {
			return err
		}
		count := action.ClickCount
		if count <= 0 {
			count = 1
		}
		return windowsClick(action.buttonOrDefault(), count, action.DurationMS)
	case ActionDoubleClick:
		moveX, moveY := toScreen(action.X, action.Y)
		if err := windowsSetCursor(moveX, moveY); err != nil {
			return err
		}
		count := action.ClickCount
		if count <= 0 {
			count = 2
		}
		return windowsClick(action.buttonOrDefault(), count, action.DurationMS)
	case ActionDrag:
		startX, startY := toScreen(action.X, action.Y)
		endX, endY := toScreen(action.EndX, action.EndY)
		if err := windowsSetCursor(startX, startY); err != nil {
			return err
		}
		down, up, err := windowsMouseButtonFlags(action.buttonOrDefault())
		if err != nil {
			return err
		}
		winMouseEvent.Call(uintptr(down), 0, 0, 0, 0)
		if action.DurationMS > 0 {
			if err := waitContext(ctx, time.Duration(action.DurationMS)*time.Millisecond); err != nil {
				winMouseEvent.Call(uintptr(up), 0, 0, 0, 0)
				return err
			}
		}
		if err := windowsSetCursor(endX, endY); err != nil {
			winMouseEvent.Call(uintptr(up), 0, 0, 0, 0)
			return err
		}
		winMouseEvent.Call(uintptr(up), 0, 0, 0, 0)
		return nil
	case ActionTypeText:
		return windowsTypeText(action.Text)
	case ActionKeypress:
		return windowsKeypress(action.Keys)
	case ActionScroll:
		return windowsScroll(action.DeltaX, action.DeltaY)
	default:
		return fmt.Errorf("computer: unsupported windows action %q", action.Kind)
	}
}

func (b *WindowsBackend) Close() error { return nil }

func windowsVirtualScreenBounds() (x, y, width, height int) {
	metric := func(index int) int {
		value, _, _ := winGetSystemMetrics.Call(uintptr(index))
		return int(int32(value))
	}
	return metric(winSMXVXScreen), metric(winSMYVYScreen), metric(winSMCXVScreen), metric(winSMCYVScreen)
}

func windowsCapturePNG(x, y, width, height int) ([]byte, error) {
	screenDC, _, callErr := winGetDC.Call(0)
	if screenDC == 0 {
		return nil, windowsCallError("GetDC", callErr)
	}
	defer winReleaseDC.Call(0, screenDC)

	memDC, _, callErr := winCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, windowsCallError("CreateCompatibleDC", callErr)
	}
	defer winDeleteDC.Call(memDC)

	bitmap, _, callErr := winCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, windowsCallError("CreateCompatibleBitmap", callErr)
	}
	defer winDeleteObject.Call(bitmap)

	oldObject, _, callErr := winSelectObject.Call(memDC, bitmap)
	if oldObject == 0 || oldObject == ^uintptr(0) {
		return nil, windowsCallError("SelectObject", callErr)
	}
	defer winSelectObject.Call(memDC, oldObject)

	ret, _, callErr := winBitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height), screenDC, uintptr(int32(x)), uintptr(int32(y)), winSRCCopy|winCaptureBlt)
	if ret == 0 {
		return nil, windowsCallError("BitBlt", callErr)
	}

	pixels := make([]byte, width*height*4)
	info := winBitmapInfo{Header: winBitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(winBitmapInfoHeader{})),
		Width:       int32(width),
		Height:      -int32(height),
		Planes:      1,
		BitCount:    32,
		Compression: winBI_RGB,
	}}
	ret, _, callErr = winGetDIBits.Call(memDC, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), winDIBRGB)
	if int(ret) != height {
		return nil, windowsCallError("GetDIBits", callErr)
	}

	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for offset := 0; offset < len(pixels); offset += 4 {
		rgba.Pix[offset] = pixels[offset+2]
		rgba.Pix[offset+1] = pixels[offset+1]
		rgba.Pix[offset+2] = pixels[offset]
		rgba.Pix[offset+3] = 0xff
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, rgba); err != nil {
		return nil, fmt.Errorf("computer: encode windows screenshot: %w", err)
	}
	return encoded.Bytes(), nil
}

func windowsForegroundWindow() (string, Rect) {
	hwnd, _, _ := winGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", Rect{}
	}
	buffer := make([]uint16, 512)
	n, _, _ := winGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	name := ""
	if n > 0 {
		name = windows.UTF16ToString(buffer[:n])
	}
	var bounds winRect
	if ret, _, _ := winGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ret != 0 {
		return name, Rect{X: int(bounds.Left), Y: int(bounds.Top), Width: int(bounds.Right - bounds.Left), Height: int(bounds.Bottom - bounds.Top)}
	}
	return name, Rect{}
}

func windowsSetCursor(x, y int) error {
	ret, _, callErr := winSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y)))
	if ret == 0 {
		return windowsCallError("SetCursorPos", callErr)
	}
	return nil
}

func windowsClick(button string, count, durationMS int) error {
	down, up, err := windowsMouseButtonFlags(button)
	if err != nil {
		return err
	}
	if count <= 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		winMouseEvent.Call(uintptr(down), 0, 0, 0, 0)
		if durationMS > 0 {
			if err := waitContext(context.Background(), time.Duration(durationMS)*time.Millisecond); err != nil {
				return err
			}
		}
		winMouseEvent.Call(uintptr(up), 0, 0, 0, 0)
		if i+1 < count {
			if err := waitContext(context.Background(), 80*time.Millisecond); err != nil {
				return err
			}
		}
	}
	return nil
}

func windowsMouseButtonFlags(button string) (down, up uint32, err error) {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "", "left":
		return winMouseLeftDown, winMouseLeftUp, nil
	case "middle":
		return winMouseMiddleDown, winMouseMiddleUp, nil
	case "right":
		return winMouseRightDown, winMouseRightUp, nil
	default:
		return 0, 0, fmt.Errorf("computer: unsupported mouse button %q", button)
	}
}

func windowsScroll(deltaX, deltaY int) error {
	if deltaY != 0 {
		winMouseEvent.Call(winMouseWheel, 0, 0, uintptr(uint32(int32(deltaY*120))), 0)
	}
	if deltaX != 0 {
		winMouseEvent.Call(winMouseHWheel, 0, 0, uintptr(uint32(int32(deltaX*120))), 0)
	}
	return nil
}

func windowsTypeText(value string) error {
	units := utf16.Encode([]rune(value))
	inputs := make([]winInput, 0, len(units)*2)
	for _, unit := range units {
		inputs = append(inputs, windowsKeyboardInput(0, unit, winKeyEventUnicode), windowsKeyboardInput(0, unit, winKeyEventUnicode|winKeyEventUp))
	}
	return windowsSendInputs(inputs)
}

func windowsKeypress(keys []string) error {
	inputs := make([]winInput, 0, len(keys)*2)
	virtualKeys := make([]uint16, 0, len(keys))
	for _, key := range keys {
		virtualKey, ok := windowsVirtualKey(key)
		if !ok {
			return fmt.Errorf("computer: unsupported Windows key %q", key)
		}
		virtualKeys = append(virtualKeys, virtualKey)
		inputs = append(inputs, windowsKeyboardInput(virtualKey, 0, 0))
	}
	for index := len(virtualKeys) - 1; index >= 0; index-- {
		inputs = append(inputs, windowsKeyboardInput(virtualKeys[index], 0, winKeyEventUp))
	}
	return windowsSendInputs(inputs)
}

func windowsSendInputs(inputs []winInput) error {
	if len(inputs) == 0 {
		return nil
	}
	ret, _, callErr := winSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(winInput{}))
	if int(ret) != len(inputs) {
		return windowsCallError("SendInput", callErr)
	}
	return nil
}

func windowsKeyboardInput(virtualKey, scan uint16, flags uint32) winInput {
	var input winInput
	input.Type = winInputKeyboard
	binary.LittleEndian.PutUint16(input.Data[0:2], virtualKey)
	binary.LittleEndian.PutUint16(input.Data[2:4], scan)
	binary.LittleEndian.PutUint32(input.Data[4:8], flags)
	return input
}

func windowsVirtualKey(raw string) (uint16, bool) {
	key := strings.ToUpper(strings.TrimSpace(raw))
	if len(key) == 1 {
		if key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9' {
			return uint16(key[0]), true
		}
	}
	switch strings.ReplaceAll(key, " ", "_") {
	case "CTRL", "CONTROL":
		return 0x11, true
	case "ALT", "OPTION", "OPT":
		return 0x12, true
	case "SHIFT":
		return 0x10, true
	case "CMD", "COMMAND", "META", "WIN", "WINDOWS", "SUPER":
		return 0x5B, true
	case "ESC", "ESCAPE":
		return 0x1B, true
	case "ENTER", "RETURN":
		return 0x0D, true
	case "BACKSPACE", "BACK_SPACE":
		return 0x08, true
	case "DELETE", "DEL":
		return 0x2E, true
	case "INSERT", "INS":
		return 0x2D, true
	case "TAB":
		return 0x09, true
	case "SPACE", "SPACEBAR":
		return 0x20, true
	case "HOME":
		return 0x24, true
	case "END":
		return 0x23, true
	case "PAGEUP", "PAGE_UP":
		return 0x21, true
	case "PAGEDOWN", "PAGE_DOWN":
		return 0x22, true
	case "UP", "ARROWUP":
		return 0x26, true
	case "DOWN", "ARROWDOWN":
		return 0x28, true
	case "LEFT", "ARROWLEFT":
		return 0x25, true
	case "RIGHT", "ARROWRIGHT":
		return 0x27, true
	}
	if len(key) >= 2 && key[0] == 'F' {
		var number int
		if _, err := fmt.Sscanf(key[1:], "%d", &number); err == nil && number >= 1 && number <= 24 {
			return uint16(0x70 + number - 1), true
		}
	}
	return 0, false
}

func windowsCallError(name string, callErr error) error {
	if callErr != nil && callErr != windows.ERROR_SUCCESS {
		return fmt.Errorf("computer: windows %s: %w", name, callErr)
	}
	return fmt.Errorf("computer: windows %s failed", name)
}

type winBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type winBitmapInfo struct {
	Header winBitmapInfoHeader
	Colors [1]uint32
}

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// INPUT is 40 bytes on 64-bit Windows because its union is sized for a
// MOUSEINPUT. Keeping the union as raw bytes avoids architecture-dependent Go
// padding in the keyboard-only path used by SendInput.
type winInput struct {
	Type uint32
	_    uint32
	Data [32]byte
}
