// Command client connects to a VNC server, fetches a framebuffer update,
// optionally sends a few input events, and saves the received frame as a
// raw PPM image for quick visual verification.
//
// Usage:
//
//	go run ./example/client                                 # connect to localhost:5900
//	go run ./example/client -addr 192.168.1.10:5900         # remote server
//	go run ./example/client -password secret                # with VNC auth
//	go run ./example/client -output frame.ppm               # save screenshot
//	go run ./example/client -send-input                     # send test key/pointer events
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rixcian/redvnc/rfb"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5900", "VNC server address")
	password := flag.String("password", "", "VNC password (empty = no auth)")
	output := flag.String("output", "", "Save first frame as PPM image (e.g. frame.ppm)")
	sendInput := flag.Bool("send-input", false, "Send test keyboard/pointer events")
	flag.Parse()

	log.Printf("connecting to %s …", *addr)

	client, err := rfb.Connect(*addr, rfb.ClientConfig{
		Password:  *password,
		Shared:    true,
		Encodings: []int32{rfb.EncodingRaw},
	})
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	log.Printf("connected: name=%q  size=%dx%d  pixel_format={bpp:%d depth:%d}",
		client.Name, client.Width, client.Height,
		client.PixelFormat.BitsPerPixel, client.PixelFormat.Depth)

	// --- Send test input events -----------------------------------------------
	if *sendInput {
		log.Println("sending test pointer event: move to (100, 100)")
		if err := client.SendPointerEvent(0, 100, 100); err != nil {
			log.Fatalf("pointer event: %v", err)
		}

		// Send 'A' key press/release (X11 keysym 0x61)
		log.Println("sending test key event: 'a' press + release")
		if err := client.SendKeyEvent(true, 0x61); err != nil {
			log.Fatalf("key down: %v", err)
		}
		if err := client.SendKeyEvent(false, 0x61); err != nil {
			log.Fatalf("key up: %v", err)
		}
	}

	// --- Request full framebuffer ---------------------------------------------
	log.Println("requesting full framebuffer update …")
	if err := client.RequestFramebufferUpdate(false, 0, 0, client.Width, client.Height); err != nil {
		log.Fatalf("framebuffer request: %v", err)
	}

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		log.Fatalf("read message: %v", err)
	}

	if msgType != rfb.MsgFramebufferUpdate {
		log.Fatalf("unexpected message type: %d", msgType)
	}

	update := msg.(*rfb.FramebufferUpdate)
	log.Printf("received framebuffer update: %d rectangle(s)", len(update.Rects))

	for i, rect := range update.Rects {
		log.Printf("  rect[%d]: pos=(%d,%d) size=%dx%d encoding=%d data=%d bytes",
			i, rect.X, rect.Y, rect.Width, rect.Height, rect.Encoding, len(rect.Data))
	}

	// --- Save as PPM ----------------------------------------------------------
	if *output != "" && len(update.Rects) > 0 {
		rect := update.Rects[0]
		if err := writePPM(*output, rect.Data, int(rect.Width), int(rect.Height)); err != nil {
			log.Fatalf("write PPM: %v", err)
		}
		log.Printf("saved frame to %s (%dx%d)", *output, rect.Width, rect.Height)
		log.Printf("  open with: open %s   (macOS Preview)", *output)
	}

	log.Println("done")
}

// writePPM converts BGRA pixel data to a PPM (P6) image file.
func writePPM(path string, bgra []byte, width, height int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	header := fmt.Sprintf("P6\n%d %d\n255\n", width, height)
	if _, err := f.WriteString(header); err != nil {
		return err
	}

	bpp := 4
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			off := (y*width + x) * bpp
			if off+2 >= len(bgra) {
				// Write black if out of bounds
				if _, err := f.Write([]byte{0, 0, 0}); err != nil {
					return err
				}
				continue
			}
			b := bgra[off+0]
			g := bgra[off+1]
			r := bgra[off+2]
			// PPM is RGB
			if err := binary.Write(f, binary.BigEndian, [3]byte{r, g, b}); err != nil {
				return err
			}
		}
	}
	return nil
}
