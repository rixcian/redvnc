//go:build windows

package input

/*
#cgo LDFLAGS: -luser32

#include <windows.h>
#include <stdint.h>

// Map X11 keysym to Windows virtual key code.
// X11 keysyms: 0x20-0x7E are ASCII, 0xFF00+ are special keys.
static WORD xkeysym_to_vk(uint32_t keysym) {
	// ASCII printable range (0x20 - 0x7E)
	if (keysym >= 0x20 && keysym <= 0x7E) {
		// Letters: X11 keysyms use lowercase, VK codes use uppercase
		if (keysym >= 'a' && keysym <= 'z') {
			return (WORD)(keysym - 'a' + 'A');
		}
		// Digits 0-9
		if (keysym >= '0' && keysym <= '9') {
			return (WORD)keysym;
		}
		switch (keysym) {
			case 0x20: return VK_SPACE;
			case '!':  return '1';    // Shift+1
			case '@':  return '2';    // Shift+2
			case '#':  return '3';
			case '$':  return '4';
			case '%':  return '5';
			case '^':  return '6';
			case '&':  return '7';
			case '*':  return '8';
			case '(':  return '9';
			case ')':  return '0';
			case '-':  return VK_OEM_MINUS;
			case '_':  return VK_OEM_MINUS;
			case '=':  return VK_OEM_PLUS;
			case '+':  return VK_OEM_PLUS;
			case '[':  return VK_OEM_4;
			case '{':  return VK_OEM_4;
			case ']':  return VK_OEM_6;
			case '}':  return VK_OEM_6;
			case '\\': return VK_OEM_5;
			case '|':  return VK_OEM_5;
			case ';':  return VK_OEM_1;
			case ':':  return VK_OEM_1;
			case '\'': return VK_OEM_7;
			case '"':  return VK_OEM_7;
			case ',':  return VK_OEM_COMMA;
			case '<':  return VK_OEM_COMMA;
			case '.':  return VK_OEM_PERIOD;
			case '>':  return VK_OEM_PERIOD;
			case '/':  return VK_OEM_2;
			case '?':  return VK_OEM_2;
			case '`':  return VK_OEM_3;
			case '~':  return VK_OEM_3;
			// Uppercase letters map directly
			default:
				if (keysym >= 'A' && keysym <= 'Z') {
					return (WORD)keysym;
				}
				return 0;
		}
	}

	// X11 special keys (0xFF00+ range)
	switch (keysym) {
		// Navigation
		case 0xFF08: return VK_BACK;        // BackSpace
		case 0xFF09: return VK_TAB;         // Tab
		case 0xFF0D: return VK_RETURN;      // Return/Enter
		case 0xFF1B: return VK_ESCAPE;      // Escape
		case 0xFFFF: return VK_DELETE;       // Delete
		case 0xFF50: return VK_HOME;        // Home
		case 0xFF51: return VK_LEFT;        // Left
		case 0xFF52: return VK_UP;          // Up
		case 0xFF53: return VK_RIGHT;       // Right
		case 0xFF54: return VK_DOWN;        // Down
		case 0xFF55: return VK_PRIOR;       // Page Up
		case 0xFF56: return VK_NEXT;        // Page Down
		case 0xFF57: return VK_END;         // End
		case 0xFF63: return VK_INSERT;      // Insert

		// Modifiers
		case 0xFFE1: return VK_LSHIFT;      // Shift_L
		case 0xFFE2: return VK_RSHIFT;      // Shift_R
		case 0xFFE3: return VK_LCONTROL;    // Control_L
		case 0xFFE4: return VK_RCONTROL;    // Control_R
		case 0xFFE9: return VK_LMENU;       // Alt_L
		case 0xFFEA: return VK_RMENU;       // Alt_R
		case 0xFFEB: return VK_LWIN;        // Super_L
		case 0xFFEC: return VK_RWIN;        // Super_R
		case 0xFFE5: return VK_CAPITAL;     // Caps_Lock
		case 0xFF7F: return VK_NUMLOCK;     // Num_Lock
		case 0xFF14: return VK_SCROLL;      // Scroll_Lock

		// Function keys F1-F12
		case 0xFFBE: return VK_F1;
		case 0xFFBF: return VK_F2;
		case 0xFFC0: return VK_F3;
		case 0xFFC1: return VK_F4;
		case 0xFFC2: return VK_F5;
		case 0xFFC3: return VK_F6;
		case 0xFFC4: return VK_F7;
		case 0xFFC5: return VK_F8;
		case 0xFFC6: return VK_F9;
		case 0xFFC7: return VK_F10;
		case 0xFFC8: return VK_F11;
		case 0xFFC9: return VK_F12;

		// Misc
		case 0xFF61: return VK_SNAPSHOT;    // Print Screen
		case 0xFF13: return VK_PAUSE;       // Pause
		case 0xFF67: return VK_APPS;        // Menu key

		// Keypad
		case 0xFF8D: return VK_RETURN;      // KP_Enter
		case 0xFFB0: return VK_NUMPAD0;
		case 0xFFB1: return VK_NUMPAD1;
		case 0xFFB2: return VK_NUMPAD2;
		case 0xFFB3: return VK_NUMPAD3;
		case 0xFFB4: return VK_NUMPAD4;
		case 0xFFB5: return VK_NUMPAD5;
		case 0xFFB6: return VK_NUMPAD6;
		case 0xFFB7: return VK_NUMPAD7;
		case 0xFFB8: return VK_NUMPAD8;
		case 0xFFB9: return VK_NUMPAD9;
		case 0xFFAA: return VK_MULTIPLY;    // KP_Multiply
		case 0xFFAB: return VK_ADD;         // KP_Add
		case 0xFFAD: return VK_SUBTRACT;    // KP_Subtract
		case 0xFFAE: return VK_DECIMAL;     // KP_Decimal
		case 0xFFAF: return VK_DIVIDE;      // KP_Divide

		default: return 0;
	}
}

static int win_key_event(uint32_t keysym, int down) {
	WORD vk = xkeysym_to_vk(keysym);
	if (vk == 0) return -1;

	INPUT input;
	memset(&input, 0, sizeof(INPUT));
	input.type = INPUT_KEYBOARD;
	input.ki.wVk = vk;
	input.ki.wScan = (WORD)MapVirtualKeyW(vk, MAPVK_VK_TO_VSC);

	if (!down) {
		input.ki.dwFlags |= KEYEVENTF_KEYUP;
	}

	// Set extended key flag for right-side and navigation keys
	if (vk == VK_RCONTROL || vk == VK_RMENU || vk == VK_RWIN ||
	    vk == VK_INSERT || vk == VK_DELETE || vk == VK_HOME || vk == VK_END ||
	    vk == VK_PRIOR || vk == VK_NEXT || vk == VK_LEFT || vk == VK_RIGHT ||
	    vk == VK_UP || vk == VK_DOWN || vk == VK_SNAPSHOT || vk == VK_APPS ||
	    vk == VK_DIVIDE || vk == VK_NUMLOCK) {
		input.ki.dwFlags |= KEYEVENTF_EXTENDEDKEY;
	}

	UINT sent = SendInput(1, &input, sizeof(INPUT));
	return (sent == 1) ? 0 : -1;
}

static int win_pointer_event(int x, int y, uint32_t flags) {
	INPUT input;
	memset(&input, 0, sizeof(INPUT));
	input.type = INPUT_MOUSE;

	// Normalize to 0-65535 absolute coordinates
	int screenW = GetSystemMetrics(SM_CXSCREEN);
	int screenH = GetSystemMetrics(SM_CYSCREEN);
	if (screenW <= 0 || screenH <= 0) return -1;

	input.mi.dx = (LONG)((x * 65535 + screenW / 2) / screenW);
	input.mi.dy = (LONG)((y * 65535 + screenH / 2) / screenH);
	input.mi.dwFlags = flags | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_MOVE;
	input.mi.mouseData = 0;

	// Extract wheel data from flags if present
	// We encode wheel delta in the upper 16 bits of flags
	if (flags & MOUSEEVENTF_WHEEL) {
		input.mi.mouseData = WHEEL_DELTA;
	}

	UINT sent = SendInput(1, &input, sizeof(INPUT));
	return (sent == 1) ? 0 : -1;
}

static int win_pointer_button(uint32_t flags, int32_t mouseData) {
	INPUT input;
	memset(&input, 0, sizeof(INPUT));
	input.type = INPUT_MOUSE;
	input.mi.dwFlags = flags;
	input.mi.mouseData = mouseData;

	UINT sent = SendInput(1, &input, sizeof(INPUT));
	return (sent == 1) ? 0 : -1;
}
*/
import "C"

// WindowsInput injects input using the Windows SendInput API.
type WindowsInput struct {
	lastButtonMask uint8
}

func NewInputInjector() (InputInjector, error) {
	return &WindowsInput{}, nil
}

func (w *WindowsInput) Init() error {
	// SendInput does not require initialization.
	return nil
}

func (w *WindowsInput) KeyEvent(down bool, key uint32) error {
	downInt := C.int(0)
	if down {
		downInt = 1
	}

	rc := C.win_key_event(C.uint32_t(key), downInt)
	if rc != 0 {
		// Unknown keysym — ignore rather than error
		return nil
	}
	return nil
}

func (w *WindowsInput) PointerEvent(buttonMask uint8, xPos, yPos uint16) error {
	// Move the pointer
	var flags C.uint32_t = 0
	C.win_pointer_event(C.int(xPos), C.int(yPos), flags)

	// Detect button state changes
	changed := w.lastButtonMask ^ buttonMask

	// VNC button mask: bit 0 = left, bit 1 = middle, bit 2 = right,
	// bit 3 = scroll up, bit 4 = scroll down

	// Left button (bit 0)
	if changed&0x01 != 0 {
		if buttonMask&0x01 != 0 {
			C.win_pointer_button(C.MOUSEEVENTF_LEFTDOWN, 0)
		} else {
			C.win_pointer_button(C.MOUSEEVENTF_LEFTUP, 0)
		}
	}

	// Middle button (bit 1)
	if changed&0x02 != 0 {
		if buttonMask&0x02 != 0 {
			C.win_pointer_button(C.MOUSEEVENTF_MIDDLEDOWN, 0)
		} else {
			C.win_pointer_button(C.MOUSEEVENTF_MIDDLEUP, 0)
		}
	}

	// Right button (bit 2)
	if changed&0x04 != 0 {
		if buttonMask&0x04 != 0 {
			C.win_pointer_button(C.MOUSEEVENTF_RIGHTDOWN, 0)
		} else {
			C.win_pointer_button(C.MOUSEEVENTF_RIGHTUP, 0)
		}
	}

	// Scroll up (bit 3) — send wheel event on press only
	if buttonMask&0x08 != 0 && w.lastButtonMask&0x08 == 0 {
		C.win_pointer_button(C.MOUSEEVENTF_WHEEL, C.WHEEL_DELTA)
	}

	// Scroll down (bit 4) — send wheel event on press only
	if buttonMask&0x10 != 0 && w.lastButtonMask&0x10 == 0 {
		C.win_pointer_button(C.MOUSEEVENTF_WHEEL, -C.WHEEL_DELTA)
	}

	w.lastButtonMask = buttonMask
	return nil
}

func (w *WindowsInput) Close() error {
	return nil
}
