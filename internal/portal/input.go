//go:build linux

package portal

import (
	"fmt"

	"github.com/godbus/dbus/v5"
)

// KeyState values for NotifyKeyboardKeycode.
const (
	KeyStateReleased uint32 = 0
	KeyStatePressed  uint32 = 1
)

// ButtonState values for NotifyPointerButton.
const (
	ButtonStateReleased uint32 = 0
	ButtonStatePressed  uint32 = 1
)

// Linux evdev button codes. These are the values portal expects in
// NotifyPointerButton and match <linux/input-event-codes.h>.
const (
	BtnLeft    int32 = 0x110
	BtnRight   int32 = 0x111
	BtnMiddle  int32 = 0x112
	BtnSide    int32 = 0x113
	BtnExtra   int32 = 0x114
	BtnForward int32 = 0x115
	BtnBack    int32 = 0x116
)

// AxisHorizontal / AxisVertical are the discrete-axis identifiers used
// by NotifyPointerAxisDiscrete.
const (
	AxisHorizontal uint32 = 0
	AxisVertical   uint32 = 1
)

func (s *Session) checkInputSession() error {
	if !s.opts.Input {
		return fmt.Errorf("portal: input not enabled on this session")
	}
	if s.sessionHandle == "" {
		return fmt.Errorf("portal: session not started")
	}
	return nil
}

// NotifyKeyboardKeycode injects a keyboard key press or release using
// the evdev keycode (not the keysym). state must be KeyStatePressed or
// KeyStateReleased.
func (s *Session) NotifyKeyboardKeycode(keycode int32, state uint32) error {
	if err := s.checkInputSession(); err != nil {
		return err
	}
	obj := s.conn.Object(busName, objectPath)
	return obj.Call(
		ifaceRemoteDesktop+".NotifyKeyboardKeycode",
		0,
		s.sessionHandle,
		map[string]dbus.Variant{},
		keycode,
		state,
	).Err
}

// NotifyKeyboardKeysym injects a keyboard event identified by X11
// keysym (e.g. XK_a = 0x61, XK_Return = 0xff0d). This is what VNC
// uses on the wire, so it maps straight through without a translation
// table. Not every portal backend supports this method — callers
// should fall back to NotifyKeyboardKeycode when it fails.
func (s *Session) NotifyKeyboardKeysym(keysym int32, state uint32) error {
	if err := s.checkInputSession(); err != nil {
		return err
	}
	obj := s.conn.Object(busName, objectPath)
	return obj.Call(
		ifaceRemoteDesktop+".NotifyKeyboardKeysym",
		0,
		s.sessionHandle,
		map[string]dbus.Variant{},
		keysym,
		state,
	).Err
}

// NotifyPointerMotionAbsolute moves the pointer to the given absolute
// position within a stream. x and y are in logical pixels relative to
// the upper-left corner of the stream whose node_id is given. For
// single-monitor sessions the streamNodeID should be the one returned
// by PipeWireNodeID().
func (s *Session) NotifyPointerMotionAbsolute(streamNodeID uint32, x, y float64) error {
	if err := s.checkInputSession(); err != nil {
		return err
	}
	obj := s.conn.Object(busName, objectPath)
	return obj.Call(
		ifaceRemoteDesktop+".NotifyPointerMotionAbsolute",
		0,
		s.sessionHandle,
		map[string]dbus.Variant{},
		streamNodeID,
		x,
		y,
	).Err
}

// NotifyPointerButton injects a pointer button press or release using
// a Linux evdev button code (BtnLeft, BtnRight, ...).
func (s *Session) NotifyPointerButton(button int32, state uint32) error {
	if err := s.checkInputSession(); err != nil {
		return err
	}
	obj := s.conn.Object(busName, objectPath)
	return obj.Call(
		ifaceRemoteDesktop+".NotifyPointerButton",
		0,
		s.sessionHandle,
		map[string]dbus.Variant{},
		button,
		state,
	).Err
}

// NotifyPointerAxisDiscrete injects a discrete scroll step (wheel
// click). axis is AxisHorizontal or AxisVertical. steps is the signed
// number of wheel detents — typically +/-1 for a VNC scroll button
// event.
func (s *Session) NotifyPointerAxisDiscrete(axis uint32, steps int32) error {
	if err := s.checkInputSession(); err != nil {
		return err
	}
	obj := s.conn.Object(busName, objectPath)
	return obj.Call(
		ifaceRemoteDesktop+".NotifyPointerAxisDiscrete",
		0,
		s.sessionHandle,
		map[string]dbus.Variant{},
		axis,
		steps,
	).Err
}
