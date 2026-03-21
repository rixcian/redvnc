//go:build windows

package input

import (
	"syscall"
	"unsafe"
)

var (
	user32Input          = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32Input.NewProc("SendInput")
	procMapVirtualKeyW   = user32Input.NewProc("MapVirtualKeyW")
	procGetSystemMetricsI = user32Input.NewProc("GetSystemMetrics")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfScanCode    = 0x0008

	mouseeventfMove       = 0x0001
	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfAbsolute   = 0x8000

	wheelDelta = 120

	mapvkVKToVSC = 0

	smCXScreenI = 0
	smCYScreenI = 1
)

// INPUT structure for SendInput.
// We use a fixed-size struct large enough for both KEYBDINPUT and MOUSEINPUT.
type inputStruct struct {
	Type uint32
	_    [4]byte // padding on 64-bit
	// Union: MOUSEINPUT is the largest member (32 bytes on 64-bit)
	Data [24]byte
}

type mouseInput struct {
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// WindowsInput injects input using the Windows SendInput API.
type WindowsInput struct {
	lastButtonMask uint8
}

func NewInputInjector() (InputInjector, error) {
	return &WindowsInput{}, nil
}

func (w *WindowsInput) Init() error {
	return nil
}

func (w *WindowsInput) KeyEvent(down bool, key uint32) error {
	vk := xkeysymToVK(key)
	if vk == 0 {
		return nil // unknown keysym, ignore
	}

	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), mapvkVKToVSC)

	var flags uint32
	if !down {
		flags |= keyeventfKeyUp
	}
	if isExtendedKey(vk) {
		flags |= keyeventfExtendedKey
	}

	ki := keybdInput{
		Vk:    uint16(vk),
		Scan:  uint16(scan),
		Flags: flags,
	}

	var inp inputStruct
	inp.Type = inputKeyboard
	*(*keybdInput)(unsafe.Pointer(&inp.Data[0])) = ki

	procSendInput.Call(1, uintptr(unsafe.Pointer(&inp)), unsafe.Sizeof(inp))
	return nil
}

func (w *WindowsInput) PointerEvent(buttonMask uint8, xPos, yPos uint16) error {
	screenW, _, _ := procGetSystemMetricsI.Call(smCXScreenI)
	screenH, _, _ := procGetSystemMetricsI.Call(smCYScreenI)
	if screenW == 0 || screenH == 0 {
		return nil
	}

	// Move pointer (absolute coordinates normalized to 0–65535)
	absX := (int32(xPos)*65535 + int32(screenW)/2) / int32(screenW)
	absY := (int32(yPos)*65535 + int32(screenH)/2) / int32(screenH)

	mi := mouseInput{
		Dx:    absX,
		Dy:    absY,
		Flags: mouseeventfAbsolute | mouseeventfMove,
	}
	var inp inputStruct
	inp.Type = inputMouse
	*(*mouseInput)(unsafe.Pointer(&inp.Data[0])) = mi
	procSendInput.Call(1, uintptr(unsafe.Pointer(&inp)), unsafe.Sizeof(inp))

	changed := w.lastButtonMask ^ buttonMask

	// Left button (bit 0)
	if changed&0x01 != 0 {
		flag := uint32(mouseeventfLeftUp)
		if buttonMask&0x01 != 0 {
			flag = mouseeventfLeftDown
		}
		w.sendButton(flag, 0)
	}

	// Middle button (bit 1)
	if changed&0x02 != 0 {
		flag := uint32(mouseeventfMiddleUp)
		if buttonMask&0x02 != 0 {
			flag = mouseeventfMiddleDown
		}
		w.sendButton(flag, 0)
	}

	// Right button (bit 2)
	if changed&0x04 != 0 {
		flag := uint32(mouseeventfRightUp)
		if buttonMask&0x04 != 0 {
			flag = mouseeventfRightDown
		}
		w.sendButton(flag, 0)
	}

	// Scroll up (bit 3)
	if buttonMask&0x08 != 0 && w.lastButtonMask&0x08 == 0 {
		w.sendButton(mouseeventfWheel, wheelDelta)
	}

	// Scroll down (bit 4)
	if buttonMask&0x10 != 0 && w.lastButtonMask&0x10 == 0 {
		w.sendWheelRaw(-wheelDelta)
	}

	w.lastButtonMask = buttonMask
	return nil
}

func (w *WindowsInput) sendButton(flags uint32, mouseData uint32) {
	mi := mouseInput{
		Flags:     flags,
		MouseData: mouseData,
	}
	var inp inputStruct
	inp.Type = inputMouse
	*(*mouseInput)(unsafe.Pointer(&inp.Data[0])) = mi
	procSendInput.Call(1, uintptr(unsafe.Pointer(&inp)), unsafe.Sizeof(inp))
}

func (w *WindowsInput) sendWheelRaw(delta int32) {
	mi := mouseInput{
		Flags:     mouseeventfWheel,
		MouseData: uint32(delta),
	}
	var inp inputStruct
	inp.Type = inputMouse
	*(*mouseInput)(unsafe.Pointer(&inp.Data[0])) = mi
	procSendInput.Call(1, uintptr(unsafe.Pointer(&inp)), unsafe.Sizeof(inp))
}

func (w *WindowsInput) Close() error {
	return nil
}

func isExtendedKey(vk uint16) bool {
	switch vk {
	case 0xA3, 0xA5, 0x5C, // VK_RCONTROL, VK_RMENU, VK_RWIN
		0x2D, 0x2E, 0x24, 0x23, // VK_INSERT, VK_DELETE, VK_HOME, VK_END
		0x21, 0x22, // VK_PRIOR, VK_NEXT
		0x25, 0x27, 0x26, 0x28, // VK_LEFT, VK_RIGHT, VK_UP, VK_DOWN
		0x2C, 0x5D, // VK_SNAPSHOT, VK_APPS
		0x6F, 0x90: // VK_DIVIDE, VK_NUMLOCK
		return true
	}
	return false
}

// xkeysymToVK maps X11 keysyms to Windows virtual key codes.
func xkeysymToVK(keysym uint32) uint16 {
	// ASCII printable range
	if keysym >= 0x20 && keysym <= 0x7E {
		if keysym >= 'a' && keysym <= 'z' {
			return uint16(keysym - 'a' + 'A')
		}
		if keysym >= '0' && keysym <= '9' {
			return uint16(keysym)
		}
		if keysym >= 'A' && keysym <= 'Z' {
			return uint16(keysym)
		}
		switch keysym {
		case 0x20:
			return 0x20 // VK_SPACE
		case '!':
			return '1'
		case '@':
			return '2'
		case '#':
			return '3'
		case '$':
			return '4'
		case '%':
			return '5'
		case '^':
			return '6'
		case '&':
			return '7'
		case '*':
			return '8'
		case '(':
			return '9'
		case ')':
			return '0'
		case '-', '_':
			return 0xBD // VK_OEM_MINUS
		case '=', '+':
			return 0xBB // VK_OEM_PLUS
		case '[', '{':
			return 0xDB // VK_OEM_4
		case ']', '}':
			return 0xDD // VK_OEM_6
		case '\\', '|':
			return 0xDC // VK_OEM_5
		case ';', ':':
			return 0xBA // VK_OEM_1
		case '\'', '"':
			return 0xDE // VK_OEM_7
		case ',', '<':
			return 0xBC // VK_OEM_COMMA
		case '.', '>':
			return 0xBE // VK_OEM_PERIOD
		case '/', '?':
			return 0xBF // VK_OEM_2
		case '`', '~':
			return 0xC0 // VK_OEM_3
		}
		return 0
	}

	// X11 special keys (0xFF00+ range)
	switch keysym {
	case 0xFF08:
		return 0x08 // VK_BACK
	case 0xFF09:
		return 0x09 // VK_TAB
	case 0xFF0D:
		return 0x0D // VK_RETURN
	case 0xFF1B:
		return 0x1B // VK_ESCAPE
	case 0xFFFF:
		return 0x2E // VK_DELETE
	case 0xFF50:
		return 0x24 // VK_HOME
	case 0xFF51:
		return 0x25 // VK_LEFT
	case 0xFF52:
		return 0x26 // VK_UP
	case 0xFF53:
		return 0x27 // VK_RIGHT
	case 0xFF54:
		return 0x28 // VK_DOWN
	case 0xFF55:
		return 0x21 // VK_PRIOR (Page Up)
	case 0xFF56:
		return 0x22 // VK_NEXT (Page Down)
	case 0xFF57:
		return 0x23 // VK_END
	case 0xFF63:
		return 0x2D // VK_INSERT
	case 0xFFE1:
		return 0xA0 // VK_LSHIFT
	case 0xFFE2:
		return 0xA1 // VK_RSHIFT
	case 0xFFE3:
		return 0xA2 // VK_LCONTROL
	case 0xFFE4:
		return 0xA3 // VK_RCONTROL
	case 0xFFE9:
		return 0xA4 // VK_LMENU
	case 0xFFEA:
		return 0xA5 // VK_RMENU
	case 0xFFEB:
		return 0x5B // VK_LWIN
	case 0xFFEC:
		return 0x5C // VK_RWIN
	case 0xFFE5:
		return 0x14 // VK_CAPITAL
	case 0xFF7F:
		return 0x90 // VK_NUMLOCK
	case 0xFF14:
		return 0x91 // VK_SCROLL
	case 0xFFBE:
		return 0x70 // VK_F1
	case 0xFFBF:
		return 0x71
	case 0xFFC0:
		return 0x72
	case 0xFFC1:
		return 0x73
	case 0xFFC2:
		return 0x74
	case 0xFFC3:
		return 0x75
	case 0xFFC4:
		return 0x76
	case 0xFFC5:
		return 0x77
	case 0xFFC6:
		return 0x78
	case 0xFFC7:
		return 0x79
	case 0xFFC8:
		return 0x7A
	case 0xFFC9:
		return 0x7B // VK_F12
	case 0xFF61:
		return 0x2C // VK_SNAPSHOT
	case 0xFF13:
		return 0x13 // VK_PAUSE
	case 0xFF67:
		return 0x5D // VK_APPS
	case 0xFF8D:
		return 0x0D // KP_Enter → VK_RETURN
	case 0xFFB0:
		return 0x60 // VK_NUMPAD0
	case 0xFFB1:
		return 0x61
	case 0xFFB2:
		return 0x62
	case 0xFFB3:
		return 0x63
	case 0xFFB4:
		return 0x64
	case 0xFFB5:
		return 0x65
	case 0xFFB6:
		return 0x66
	case 0xFFB7:
		return 0x67
	case 0xFFB8:
		return 0x68
	case 0xFFB9:
		return 0x69 // VK_NUMPAD9
	case 0xFFAA:
		return 0x6A // VK_MULTIPLY
	case 0xFFAB:
		return 0x6B // VK_ADD
	case 0xFFAD:
		return 0x6D // VK_SUBTRACT
	case 0xFFAE:
		return 0x6E // VK_DECIMAL
	case 0xFFAF:
		return 0x6F // VK_DIVIDE
	}
	return 0
}
