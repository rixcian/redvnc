//go:build linux

package input

// keysymToEvdev is a small mapping from X11 keysyms to Linux evdev
// keycodes (see <linux/input-event-codes.h>). It is used by the
// Wayland input backend as a fallback when the portal backend does
// not implement NotifyKeyboardKeysym.
//
// It deliberately only covers the commonly-used keys — alphanumerics,
// navigation, function keys, and modifiers. Less common keys end up
// as silently-ignored events under that fallback; production Wayland
// desktops (GNOME, KDE) do implement NotifyKeyboardKeysym so the
// primary path handles them.
//
// Evdev keycode values are the "input_event.code" values, not the
// legacy XT scancodes.
var keysymToEvdev = map[uint32]int32{
	// Letters (X11 XK_a..XK_z = 0x61..0x7a, uppercase 0x41..0x5a)
	0x61: 30, 0x62: 48, 0x63: 46, 0x64: 32, 0x65: 18,
	0x66: 33, 0x67: 34, 0x68: 35, 0x69: 23, 0x6a: 36,
	0x6b: 37, 0x6c: 38, 0x6d: 50, 0x6e: 49, 0x6f: 24,
	0x70: 25, 0x71: 16, 0x72: 19, 0x73: 31, 0x74: 20,
	0x75: 22, 0x76: 47, 0x77: 17, 0x78: 45, 0x79: 21,
	0x7a: 44,
	0x41: 30, 0x42: 48, 0x43: 46, 0x44: 32, 0x45: 18,
	0x46: 33, 0x47: 34, 0x48: 35, 0x49: 23, 0x4a: 36,
	0x4b: 37, 0x4c: 38, 0x4d: 50, 0x4e: 49, 0x4f: 24,
	0x50: 25, 0x51: 16, 0x52: 19, 0x53: 31, 0x54: 20,
	0x55: 22, 0x56: 47, 0x57: 17, 0x58: 45, 0x59: 21,
	0x5a: 44,

	// Digits (XK_0..XK_9 = 0x30..0x39)
	0x30: 11, 0x31: 2, 0x32: 3, 0x33: 4, 0x34: 5,
	0x35: 6, 0x36: 7, 0x37: 8, 0x38: 9, 0x39: 10,

	// Common punctuation
	0x20: 57, // space
	0x2d: 12, // minus
	0x3d: 13, // equal
	0x5b: 26, // [
	0x5d: 27, // ]
	0x5c: 43, // backslash
	0x3b: 39, // ;
	0x27: 40, // '
	0x60: 41, // `
	0x2c: 51, // ,
	0x2e: 52, // .
	0x2f: 53, // /

	// Control keys (X11 keysyms in the 0xff00 range)
	0xff08: 14,  // BackSpace
	0xff09: 15,  // Tab
	0xff0d: 28,  // Return
	0xff1b: 1,   // Escape
	0xffff: 111, // Delete
	0xff50: 102, // Home
	0xff51: 105, // Left
	0xff52: 103, // Up
	0xff53: 106, // Right
	0xff54: 108, // Down
	0xff55: 104, // Page_Up
	0xff56: 109, // Page_Down
	0xff57: 107, // End
	0xff63: 110, // Insert

	// Modifiers
	0xffe1: 42,  // Shift_L
	0xffe2: 54,  // Shift_R
	0xffe3: 29,  // Control_L
	0xffe4: 97,  // Control_R
	0xffe9: 56,  // Alt_L
	0xffea: 100, // Alt_R (AltGr)
	0xffeb: 125, // Super_L
	0xffec: 126, // Super_R
	0xffe5: 58,  // Caps_Lock
	0xff7f: 69,  // Num_Lock
	0xff14: 70,  // Scroll_Lock

	// Function keys F1..F12 (XK_F1 = 0xffbe)
	0xffbe: 59, 0xffbf: 60, 0xffc0: 61, 0xffc1: 62,
	0xffc2: 63, 0xffc3: 64, 0xffc4: 65, 0xffc5: 66,
	0xffc6: 67, 0xffc7: 68, 0xffc8: 87, 0xffc9: 88,
}
