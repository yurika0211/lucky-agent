param([string]$RequestPath)

$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [System.Text.Encoding]::UTF8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Add-Type -AssemblyName System.Drawing

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;

public static class LuckyAgentWin32
{
    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool SetProcessDPIAware();

    [DllImport("user32.dll")]
    private static extern int GetSystemMetrics(int index);

    [DllImport("user32.dll")]
    private static extern IntPtr GetForegroundWindow();

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int GetWindowTextW(IntPtr window, StringBuilder text, int count);

    [DllImport("user32.dll")]
    private static extern bool GetWindowRect(IntPtr window, out RECT rect);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool SetCursorPos(int x, int y);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool GetCursorPos(out POINT point);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extraInfo);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern uint SendInput(uint count, INPUT[] inputs, int size);

    private const uint INPUT_KEYBOARD = 1;
    private const uint KEYEVENTF_KEYUP = 0x0002;
    private const uint KEYEVENTF_UNICODE = 0x0004;

    [StructLayout(LayoutKind.Sequential)]
    private struct RECT
    {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct POINT
    {
        public int X;
        public int Y;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct KEYBDINPUT
    {
        public ushort Vk;
        public ushort Scan;
        public uint Flags;
        public uint Time;
        public UIntPtr ExtraInfo;
    }

    [StructLayout(LayoutKind.Explicit)]
    private struct INPUTUNION
    {
        [FieldOffset(0)]
        public KEYBDINPUT Keyboard;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct INPUT
    {
        public uint Type;
        public INPUTUNION Data;
    }

    public static void MakeDpiAware()
    {
        SetProcessDPIAware();
    }

    public static int Metric(int index)
    {
        return GetSystemMetrics(index);
    }

    public static string ForegroundTitle()
    {
        IntPtr window = GetForegroundWindow();
        if (window == IntPtr.Zero)
        {
            return "";
        }
        StringBuilder text = new StringBuilder(512);
        GetWindowTextW(window, text, text.Capacity);
        return text.ToString();
    }

    public static int[] ForegroundBounds()
    {
        IntPtr window = GetForegroundWindow();
        RECT rect;
        if (window == IntPtr.Zero || !GetWindowRect(window, out rect))
        {
            return new int[] { 0, 0, 0, 0 };
        }
        return new int[] { rect.Left, rect.Top, rect.Right - rect.Left, rect.Bottom - rect.Top };
    }

    public static void SetCursor(int x, int y)
    {
        if (!SetCursorPos(x, y))
        {
            throw new InvalidOperationException("SetCursorPos failed");
        }
    }

    public static int[] CursorPosition()
    {
        POINT point;
        if (!GetCursorPos(out point))
        {
            return new int[] { 0, 0, 0 };
        }
        return new int[] { point.X, point.Y, 1 };
    }

    public static void Mouse(uint flags, int data)
    {
        mouse_event(flags, 0, 0, unchecked((uint)data), UIntPtr.Zero);
    }

    private static INPUT Keyboard(ushort virtualKey, ushort scan, uint flags)
    {
        INPUT input = new INPUT();
        input.Type = INPUT_KEYBOARD;
        input.Data.Keyboard = new KEYBDINPUT {
            Vk = virtualKey,
            Scan = scan,
            Flags = flags,
            Time = 0,
            ExtraInfo = UIntPtr.Zero
        };
        return input;
    }

    private static void Send(INPUT[] inputs)
    {
        if (inputs.Length == 0)
        {
            return;
        }
        uint sent = SendInput((uint)inputs.Length, inputs, Marshal.SizeOf(typeof(INPUT)));
        if (sent != inputs.Length)
        {
            throw new InvalidOperationException("SendInput sent only " + sent + " of " + inputs.Length + " events");
        }
    }

    public static void SendUnicode(string value)
    {
        List<INPUT> inputs = new List<INPUT>(value.Length * 2);
        foreach (char character in value)
        {
            inputs.Add(Keyboard(0, character, KEYEVENTF_UNICODE));
            inputs.Add(Keyboard(0, character, KEYEVENTF_UNICODE | KEYEVENTF_KEYUP));
        }
        Send(inputs.ToArray());
    }

    public static void SendVirtualKeys(int[] keys)
    {
        List<INPUT> inputs = new List<INPUT>(keys.Length * 2);
        foreach (int key in keys)
        {
            inputs.Add(Keyboard((ushort)key, 0, 0));
        }
        for (int i = keys.Length - 1; i >= 0; i--)
        {
            inputs.Add(Keyboard((ushort)keys[i], 0, KEYEVENTF_KEYUP));
        }
        Send(inputs.ToArray());
    }

    public static int VirtualKey(string raw)
    {
        string key = (raw ?? "").Trim().ToUpperInvariant().Replace(' ', '_');
        if (key.Length == 1 && ((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')))
        {
            return key[0];
        }
        switch (key)
        {
            case "CTRL":
            case "CONTROL": return 0x11;
            case "ALT":
            case "OPTION":
            case "OPT": return 0x12;
            case "SHIFT": return 0x10;
            case "CMD":
            case "COMMAND":
            case "META":
            case "WIN":
            case "WINDOWS":
            case "SUPER": return 0x5B;
            case "ESC":
            case "ESCAPE": return 0x1B;
            case "ENTER":
            case "RETURN": return 0x0D;
            case "BACKSPACE":
            case "BACK_SPACE": return 0x08;
            case "DELETE":
            case "DEL": return 0x2E;
            case "INSERT":
            case "INS": return 0x2D;
            case "TAB": return 0x09;
            case "SPACE":
            case "SPACEBAR": return 0x20;
            case "HOME": return 0x24;
            case "END": return 0x23;
            case "PAGEUP":
            case "PAGE_UP": return 0x21;
            case "PAGEDOWN":
            case "PAGE_DOWN": return 0x22;
            case "UP":
            case "ARROWUP": return 0x26;
            case "DOWN":
            case "ARROWDOWN": return 0x28;
            case "LEFT":
            case "ARROWLEFT": return 0x25;
            case "RIGHT":
            case "ARROWRIGHT": return 0x27;
        }
        if (key.Length >= 2 && key[0] == 'F')
        {
            int number;
            if (Int32.TryParse(key.Substring(1), out number) && number >= 1 && number <= 24)
            {
                return 0x70 + number - 1;
            }
        }
        return -1;
    }
}
'@

[LuckyAgentWin32]::MakeDpiAware()

function Get-VirtualScreen {
    $x = [LuckyAgentWin32]::Metric(76)
    $y = [LuckyAgentWin32]::Metric(77)
    $width = [LuckyAgentWin32]::Metric(78)
    $height = [LuckyAgentWin32]::Metric(79)
    if ($width -le 0 -or $height -le 0 -or $width -gt 16384 -or $height -gt 16384) {
        throw "Windows virtual desktop has invalid size ${width}x${height}"
    }
    return [ordered]@{ x = $x; y = $y; width = $width; height = $height }
}

function Get-WindowBounds {
    $values = [LuckyAgentWin32]::ForegroundBounds()
    return [ordered]@{ x = $values[0]; y = $values[1]; width = $values[2]; height = $values[3] }
}

function Invoke-Capture {
    Add-Type -AssemblyName System.Windows.Forms
    $screen = Get-VirtualScreen
    $bitmap = New-Object System.Drawing.Bitmap($screen.width, $screen.height, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $stream = New-Object System.IO.MemoryStream
    try {
        $source = New-Object System.Drawing.Point($screen.x, $screen.y)
        $destination = New-Object System.Drawing.Point(0, 0)
        $size = New-Object System.Drawing.Size($screen.width, $screen.height)
        $graphics.CopyFromScreen($source, $destination, $size, [System.Drawing.CopyPixelOperation]::SourceCopy)
		$cursor = [LuckyAgentWin32]::CursorPosition()
		$cursorX = [int]$cursor[0] - $screen.x
		$cursorY = [int]$cursor[1] - $screen.y
		$cursorVisible = ([int]$cursor[2] -eq 1 -and $cursorX -ge 0 -and $cursorY -ge 0 -and $cursorX -lt $screen.width -and $cursorY -lt $screen.height)
		if ($cursorVisible) {
			# CopyFromScreen does not include the native pointer. Draw a small,
			# high-contrast arrow into the frame so the model can see the exact
			# coordinate that will be used for the next action.
			$points = [System.Drawing.Point[]]@(
				(New-Object System.Drawing.Point($cursorX, $cursorY)),
				(New-Object System.Drawing.Point($cursorX + 2, $cursorY + 18)),
				(New-Object System.Drawing.Point($cursorX + 7, $cursorY + 13)),
				(New-Object System.Drawing.Point($cursorX + 14, $cursorY + 22)),
				(New-Object System.Drawing.Point($cursorX + 18, $cursorY + 19)),
				(New-Object System.Drawing.Point($cursorX + 11, $cursorY + 10)),
				(New-Object System.Drawing.Point($cursorX + 17, $cursorY + 8))
			)
			$graphics.FillPolygon([System.Drawing.Brushes]::White, $points)
			$graphics.DrawPolygon([System.Drawing.Pens]::Black, $points)
		}
        $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
        return [ordered]@{
            width = $screen.width
            height = $screen.height
            display_id = 'wslg:virtual'
            active_window = [LuckyAgentWin32]::ForegroundTitle()
            window_bounds = Get-WindowBounds
				capture_bounds = $screen
				cursor_x = [int]$cursor[0]
				cursor_y = [int]$cursor[1]
				cursor_visible = $cursorVisible
				png_base64 = [Convert]::ToBase64String($stream.ToArray())
        }
    }
    finally {
        $stream.Dispose()
        $graphics.Dispose()
        $bitmap.Dispose()
    }
}

function Invoke-Click([string]$button, [int]$count, [int]$duration) {
    if ([string]::IsNullOrWhiteSpace($button)) {
        $button = 'left'
    }
	if ($count -le 0) { $count = 1 }
	if ($count -gt 5) { throw "click count must be between 1 and 5" }
	for ($index = 0; $index -lt $count; $index++) {
		switch ($button.ToLowerInvariant()) {
			'left' { [LuckyAgentWin32]::Mouse(0x0002, 0); if ($duration -gt 0) { Start-Sleep -Milliseconds $duration }; [LuckyAgentWin32]::Mouse(0x0004, 0) }
			'middle' { [LuckyAgentWin32]::Mouse(0x0020, 0); if ($duration -gt 0) { Start-Sleep -Milliseconds $duration }; [LuckyAgentWin32]::Mouse(0x0040, 0) }
			'right' { [LuckyAgentWin32]::Mouse(0x0008, 0); if ($duration -gt 0) { Start-Sleep -Milliseconds $duration }; [LuckyAgentWin32]::Mouse(0x0010, 0) }
			default { throw "unsupported mouse button '$button'" }
		}
		if ($index + 1 -lt $count) { Start-Sleep -Milliseconds 80 }
	}
	return
}

function Invoke-Drag($action) {
    $button = [string]$action.button
    if ([string]::IsNullOrWhiteSpace($button)) {
        $button = 'left'
    }
    $button = $button.ToLowerInvariant()
    switch ($button) {
        'left' { $down = 0x0002; $up = 0x0004 }
        'middle' { $down = 0x0020; $up = 0x0040 }
        'right' { $down = 0x0008; $up = 0x0010 }
        default { throw "unsupported mouse button '$button'" }
    }
    [LuckyAgentWin32]::SetCursor([int]$action.x, [int]$action.y)
    [LuckyAgentWin32]::Mouse($down, 0)
    try {
        $duration = [Math]::Max(0, [Math]::Min(10000, [int]$action.duration_ms))
        $steps = [Math]::Max(1, [Math]::Min(60, [int]([Math]::Ceiling($duration / 1000.0 * 60))))
        for ($step = 1; $step -le $steps; $step++) {
            if ($duration -gt 0) {
                Start-Sleep -Milliseconds ([Math]::Max(1, [int]($duration / $steps)))
            }
            $ratio = $step / [double]$steps
            $x = [int][Math]::Round([int]$action.x + ([int]$action.end_x - [int]$action.x) * $ratio)
            $y = [int][Math]::Round([int]$action.y + ([int]$action.end_y - [int]$action.y) * $ratio)
            [LuckyAgentWin32]::SetCursor($x, $y)
        }
    }
    finally {
        [LuckyAgentWin32]::Mouse($up, 0)
    }
}

function Invoke-Action($action) {
    $kind = ([string]$action.kind).ToLowerInvariant()
    switch ($kind) {
        'move' {
            [LuckyAgentWin32]::SetCursor([int]$action.x, [int]$action.y)
            return
        }
		'click' {
			[LuckyAgentWin32]::SetCursor([int]$action.x, [int]$action.y)
			Invoke-Click ([string]$action.button) ([int]$action.click_count) ([Math]::Max(0, [Math]::Min(10000, [int]$action.duration_ms)))
			return
		}
        'double_click' {
            [LuckyAgentWin32]::SetCursor([int]$action.x, [int]$action.y)
			$count = [int]$action.click_count
			if ($count -le 0) { $count = 2 }
			Invoke-Click ([string]$action.button) $count ([Math]::Max(0, [Math]::Min(10000, [int]$action.duration_ms)))
            return
        }
        'drag' {
            Invoke-Drag $action
            return
        }
        'scroll' {
            if ([int]$action.delta_y -ne 0) {
                [LuckyAgentWin32]::Mouse(0x0800, [int]$action.delta_y * 120)
            }
            if ([int]$action.delta_x -ne 0) {
                [LuckyAgentWin32]::Mouse(0x1000, [int]$action.delta_x * 120)
            }
            return
        }
        'type' {
            [LuckyAgentWin32]::SendUnicode([string]$action.text)
            return
        }
        'keypress' {
            $codes = @()
            foreach ($rawKey in @($action.keys)) {
                $code = [LuckyAgentWin32]::VirtualKey([string]$rawKey)
                if ($code -lt 0) {
                    throw "unsupported Windows key '$rawKey'"
                }
                $codes += $code
            }
            [LuckyAgentWin32]::SendVirtualKeys([int[]]$codes)
            return
        }
        default { throw "unsupported WSLg action '$kind'" }
    }
}

function Invoke-Request($request) {
    switch ([string]$request.op) {
        'probe' {
            return Get-VirtualScreen
        }
        'capture' {
            return Invoke-Capture
        }
        'action' {
            Invoke-Action $request.action
            return [ordered]@{}
        }
        default { throw "unsupported WSLg helper operation '$($request.op)'" }
    }
}

function Write-RequestResponse($request) {
    try {
        $result = Invoke-Request $request
        [Console]::WriteLine((([ordered]@{ result = $result }) | ConvertTo-Json -Compress -Depth 8))
    }
    catch {
        [Console]::WriteLine((([ordered]@{ error = $_.Exception.Message }) | ConvertTo-Json -Compress -Depth 8))
    }
}

if (-not [string]::IsNullOrWhiteSpace($RequestPath)) {
    $request = Get-Content -Raw -LiteralPath $RequestPath | ConvertFrom-Json
    Write-RequestResponse $request
    exit 0
}

while (($line = [Console]::In.ReadLine()) -ne $null) {
    Write-RequestResponse ($line | ConvertFrom-Json)
}
