//go:build darwin

package input

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

static int isAccessibilityTrusted(void) {
    return AXIsProcessTrusted();
}

static void moveMouse(int x, int y) {
    CGEventRef event = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, CGPointMake(x, y), kCGMouseButtonLeft);
    if (event) {
        CGEventPost(kCGHIDEventTap, event);
        CFRelease(event);
    }
}

static void mouseButton(int x, int y, int button, int down) {
    CGEventType type;
    CGMouseButton btn;

    switch (button) {
    case 0: // left
        btn = kCGMouseButtonLeft;
        type = down ? kCGEventLeftMouseDown : kCGEventLeftMouseUp;
        break;
    case 1: // middle
        btn = kCGMouseButtonCenter;
        type = down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp;
        break;
    case 2: // right
        btn = kCGMouseButtonRight;
        type = down ? kCGEventRightMouseDown : kCGEventRightMouseUp;
        break;
    default:
        btn = (CGMouseButton)button;
        type = down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp;
        break;
    }

    CGEventRef event = CGEventCreateMouseEvent(NULL, type, CGPointMake(x, y), btn);
    if (event) {
        CGEventPost(kCGHIDEventTap, event);
        CFRelease(event);
    }
}

static void scrollWheel(int x, int y, int dy) {
    CGEventRef event = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 1, dy);
    if (event) {
        CGEventPost(kCGHIDEventTap, event);
        CFRelease(event);
    }
}

static void keyEvent(int keycode, int down) {
    CGEventRef event = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, down ? true : false);
    if (event) {
        CGEventPost(kCGHIDEventTap, event);
        CFRelease(event);
    }
}

// For printable characters, we use a Unicode key event approach.
static void keyEventWithChar(int keycode, int down, unsigned short ch) {
    CGEventRef event = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, down ? true : false);
    if (event) {
        UniChar c = (UniChar)ch;
        CGEventKeyboardSetUnicodeString(event, 1, &c);
        CGEventPost(kCGHIDEventTap, event);
        CFRelease(event);
    }
}
*/
import "C"

import "log"

// DarwinInput injects input using macOS CGEvent API.
type DarwinInput struct {
	lastButtonMask uint8
}

func NewInputInjector() (InputInjector, error) {
	return &DarwinInput{}, nil
}

func (d *DarwinInput) Init() error {
	if C.isAccessibilityTrusted() == 0 {
		log.Println("WARNING: Accessibility permission NOT granted. Input injection will not work.")
		log.Println("Go to System Settings > Privacy & Security > Accessibility and add this application.")
	} else {
		log.Println("Accessibility permission granted — input injection enabled")
	}
	return nil
}

func (d *DarwinInput) KeyEvent(down bool, key uint32) error {
	keycode, ch, ok := keysymToMacKeycode(key)
	if !ok {
		return nil // unknown key — ignore
	}

	downInt := C.int(0)
	if down {
		downInt = 1
	}

	if ch != 0 {
		C.keyEventWithChar(C.int(keycode), downInt, C.ushort(ch))
	} else {
		C.keyEvent(C.int(keycode), downInt)
	}
	return nil
}

func (d *DarwinInput) PointerEvent(buttonMask uint8, x, y uint16) error {
	cx, cy := C.int(x), C.int(y)

	// Detect button state changes
	changed := d.lastButtonMask ^ buttonMask

	// Move the mouse first
	if changed == 0 && buttonMask == 0 {
		C.moveMouse(cx, cy)
	}

	// VNC button mask: bit 0 = left, bit 1 = middle, bit 2 = right, bit 3 = scroll up, bit 4 = scroll down
	for i := uint8(0); i < 3; i++ {
		if changed&(1<<i) != 0 {
			down := C.int(0)
			if buttonMask&(1<<i) != 0 {
				down = 1
			}
			C.mouseButton(cx, cy, C.int(i), down)
		}
	}

	// Scroll wheel (bits 3 and 4)
	if buttonMask&(1<<3) != 0 {
		C.scrollWheel(cx, cy, C.int(3))
	}
	if buttonMask&(1<<4) != 0 {
		C.scrollWheel(cx, cy, C.int(-3))
	}

	// If buttons are held but no change, send move with drag
	if changed == 0 && buttonMask != 0 {
		// Use a mouse button event to send drag — OS treats it as drag when button is held
		if buttonMask&1 != 0 {
			C.mouseButton(cx, cy, 0, 1)
		} else if buttonMask&4 != 0 {
			C.mouseButton(cx, cy, 2, 1)
		} else if buttonMask&2 != 0 {
			C.mouseButton(cx, cy, 1, 1)
		}
	}

	d.lastButtonMask = buttonMask
	return nil
}

func (d *DarwinInput) Close() error {
	return nil
}

// keysymToMacKeycode maps X11 keysym to macOS virtual keycode.
// Returns (keycode, unicodeChar, ok). unicodeChar is non-zero for printable chars.
func keysymToMacKeycode(keysym uint32) (int, uint16, bool) {
	// ASCII printable range (0x20-0x7E)
	if keysym >= 0x20 && keysym <= 0x7E {
		kc, ok := asciiToMacKeycode[byte(keysym)]
		if !ok {
			kc = 0 // fallback — Unicode string will handle it
		}
		return int(kc), uint16(keysym), true
	}

	// Special keys (X11 keysyms 0xFF00+)
	if kc, ok := specialKeysymMap[keysym]; ok {
		return int(kc), 0, true
	}

	return 0, 0, false
}

// macOS virtual keycodes for ASCII printable characters.
var asciiToMacKeycode = map[byte]byte{
	'a': 0x00, 'A': 0x00,
	's': 0x01, 'S': 0x01,
	'd': 0x02, 'D': 0x02,
	'f': 0x03, 'F': 0x03,
	'h': 0x04, 'H': 0x04,
	'g': 0x05, 'G': 0x05,
	'z': 0x06, 'Z': 0x06,
	'x': 0x07, 'X': 0x07,
	'c': 0x08, 'C': 0x08,
	'v': 0x09, 'V': 0x09,
	'b': 0x0B, 'B': 0x0B,
	'q': 0x0C, 'Q': 0x0C,
	'w': 0x0D, 'W': 0x0D,
	'e': 0x0E, 'E': 0x0E,
	'r': 0x0F, 'R': 0x0F,
	'y': 0x10, 'Y': 0x10,
	't': 0x11, 'T': 0x11,
	'1': 0x12, '!': 0x12,
	'2': 0x13, '@': 0x13,
	'3': 0x14, '#': 0x14,
	'4': 0x15, '$': 0x15,
	'6': 0x16, '^': 0x16,
	'5': 0x17, '%': 0x17,
	'=': 0x18, '+': 0x18,
	'9': 0x19, '(': 0x19,
	'7': 0x1A, '&': 0x1A,
	'-': 0x1B, '_': 0x1B,
	'8': 0x1C, '*': 0x1C,
	'0': 0x1D, ')': 0x1D,
	']': 0x1E, '}': 0x1E,
	'o': 0x1F, 'O': 0x1F,
	'u': 0x20, 'U': 0x20,
	'[': 0x21, '{': 0x21,
	'i': 0x22, 'I': 0x22,
	'p': 0x23, 'P': 0x23,
	'l': 0x25, 'L': 0x25,
	'j': 0x26, 'J': 0x26,
	'\'': 0x27, '"': 0x27,
	'k': 0x28, 'K': 0x28,
	';': 0x29, ':': 0x29,
	'\\': 0x2A, '|': 0x2A,
	',': 0x2B, '<': 0x2B,
	'/': 0x2C, '?': 0x2C,
	'n': 0x2D, 'N': 0x2D,
	'm': 0x2E, 'M': 0x2E,
	'.': 0x2F, '>': 0x2F,
	'`': 0x32, '~': 0x32,
	' ': 0x31,
}

// X11 keysym → macOS virtual keycode for special/modifier keys.
var specialKeysymMap = map[uint32]byte{
	0xFF08: 0x33, // BackSpace
	0xFF09: 0x30, // Tab
	0xFF0D: 0x24, // Return
	0xFF1B: 0x35, // Escape
	0xFFFF: 0x75, // Delete (forward)
	0xFF50: 0x73, // Home
	0xFF51: 0x7B, // Left
	0xFF52: 0x7E, // Up
	0xFF53: 0x7C, // Right
	0xFF54: 0x7D, // Down
	0xFF55: 0x74, // Page_Up
	0xFF56: 0x79, // Page_Down
	0xFF57: 0x77, // End
	0xFFE1: 0x38, // Shift_L
	0xFFE2: 0x3C, // Shift_R
	0xFFE3: 0x3B, // Control_L
	0xFFE4: 0x3E, // Control_R
	0xFFE7: 0x37, // Meta_L (Command)
	0xFFE8: 0x36, // Meta_R (Command)
	0xFFE9: 0x3A, // Alt_L (Option)
	0xFFEA: 0x3D, // Alt_R (Option)
	0xFFBE: 0x7A, // F1
	0xFFBF: 0x78, // F2
	0xFFC0: 0x63, // F3
	0xFFC1: 0x76, // F4
	0xFFC2: 0x60, // F5
	0xFFC3: 0x61, // F6
	0xFFC4: 0x62, // F7
	0xFFC5: 0x64, // F8
	0xFFC6: 0x65, // F9
	0xFFC7: 0x6D, // F10
	0xFFC8: 0x67, // F11
	0xFFC9: 0x6F, // F12
	0xFF14: 0x71, // Scroll_Lock → F15 on macOS
	0xFFE5: 0x39, // Caps_Lock
}
