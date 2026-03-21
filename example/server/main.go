// Command server starts a VNC server that shares the screen.
// On Windows, macOS and Linux it captures the real screen and injects
// keyboard/mouse input. Use -demo to show a test gradient pattern instead.
//
// Usage:
//
//	go run ./example/server                        # real screen sharing
//	go run ./example/server -demo                  # gradient test pattern
//	go run ./example/server -port 5901             # custom port
//	go run ./example/server -password secret       # VNC auth enabled
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/rixcian/redvnc/rfb"
	"github.com/rixcian/redvnc/rfb/encodings"
	"github.com/rixcian/redvnc/rfb/security"
)

// ---------------------------------------------------------------------------
// Gradient screen capturer – generates a colour pattern that shifts over time
// so you can visually confirm the framebuffer is updating.
// ---------------------------------------------------------------------------

type gradientCapturer struct {
	width, height uint16
	mu            sync.Mutex
	frame         int
}

func (g *gradientCapturer) Bounds() (uint16, uint16) {
	return g.width, g.height
}

func (g *gradientCapturer) Capture() ([]byte, int, error) {
	g.mu.Lock()
	g.frame++
	frame := g.frame
	g.mu.Unlock()

	w := int(g.width)
	h := int(g.height)
	stride := w * 4
	pixels := make([]byte, h*stride)

	// Phase shifts the gradient so it visibly animates when the client
	// requests successive frames.
	phase := float64(frame) * 0.05

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := y*stride + x*4
			r := uint8(127 + 127*math.Sin(phase+float64(x)*0.02))
			g := uint8(127 + 127*math.Sin(phase+float64(y)*0.02))
			b := uint8(127 + 127*math.Sin(phase+float64(x+y)*0.01))
			// BGRA order (default pixel format)
			pixels[off+0] = b
			pixels[off+1] = g
			pixels[off+2] = r
			pixels[off+3] = 255
		}
	}
	return pixels, stride, nil
}

// ---------------------------------------------------------------------------
// Input logger – prints received keyboard and pointer events.
// ---------------------------------------------------------------------------

type inputLogger struct{}

func (i *inputLogger) KeyEvent(down bool, key uint32) {
	action := "up"
	if down {
		action = "down"
	}
	log.Printf("[input] key %s  keysym=0x%04x", action, key)
}

func (i *inputLogger) PointerEvent(buttonMask uint8, x, y uint16) {
	log.Printf("[input] pointer  x=%d y=%d buttons=0b%08b", x, y, buttonMask)
}

// ---------------------------------------------------------------------------

func main() {
	port := flag.Int("port", 5900, "TCP port to listen on")
	password := flag.String("password", "", "VNC password (empty = no auth)")
	width := flag.Int("width", 800, "Framebuffer width")
	height := flag.Int("height", 600, "Framebuffer height")
	demo := flag.Bool("demo", false, "Use gradient test pattern instead of real screen")
	flag.Parse()

	var capturer rfb.ScreenCapturer
	var inputHandler rfb.InputHandler

	if *demo {
		log.Println("using demo gradient capturer")
		capturer = &gradientCapturer{
			width:  uint16(*width),
			height: uint16(*height),
		}
		inputHandler = &inputLogger{}
	} else {
		var err error
		capturer, inputHandler, err = setupPlatformCaptureAndInput()
		if err != nil {
			log.Printf("platform capture/input unavailable: %v", err)
			log.Println("falling back to demo gradient capturer")
			capturer = &gradientCapturer{
				width:  uint16(*width),
				height: uint16(*height),
			}
			inputHandler = &inputLogger{}
		}
	}

	config := rfb.ServerConfig{
		Name:     "redvnc",
		Capturer: capturer,
		Input:    inputHandler,
		NewTightEncoder: func() rfb.MultiEncoder {
			return encodings.NewTight(75)
		},
	}

	if *password != "" {
		config.Security = []rfb.SecurityHandler{
			&security.VNCAuth{Password: *password},
		}
	}

	// Periodically log that the server is alive.
	go func() {
		for {
			time.Sleep(30 * time.Second)
			log.Println("server running …")
		}
	}()

	addr := fmt.Sprintf(":%d", *port)
	server := rfb.NewServer(config)
	log.Printf("starting VNC server on %s (password protected: %v)", addr, *password != "")
	log.Fatal(server.ListenAndServe(addr))
}
