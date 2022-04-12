// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build example
// +build example

//
// This build tag means that "go install golang.org/x/exp/shiny/..." doesn't
// install this example program. Use "go run main.go" to run it or "go install
// -tags=example" to install it.

// Imageview is a basic image viewer. Supported image formats include BMP, GIF,
// JPEG, PNG, TIFF and WEBP.
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"io"
	"log"
	"os"
	"reflect"
	"time"

	"golang.org/x/exp/shiny/driver"
	"golang.org/x/exp/shiny/gesture"
	"golang.org/x/exp/shiny/screen"
	"golang.org/x/exp/shiny/unit"
	"golang.org/x/exp/shiny/widget"
	"golang.org/x/exp/shiny/widget/node"
	"golang.org/x/exp/shiny/widget/theme"
	"golang.org/x/image/math/f64"
	"golang.org/x/mobile/event/lifecycle"
	"golang.org/x/mobile/event/mouse"
	"golang.org/x/mobile/event/paint"
	"golang.org/x/mobile/event/size"
)

var (
	inFile = flag.String("in", "", "input binary events file name")
)

var palette = []color.Color{
	color.RGBA{0x6d, 0x0, 0x1a, 0xff}, color.RGBA{0xbe, 0x0, 0x39, 0xff}, color.RGBA{0xff, 0x45, 0x0, 0xff}, color.RGBA{0xff, 0xa8, 0x0, 0xff},
	color.RGBA{0xff, 0xd6, 0x35, 0xff}, color.RGBA{0xff, 0xf8, 0xb8, 0xff}, color.RGBA{0x0, 0xa3, 0x68, 0xff}, color.RGBA{0x0, 0xcc, 0x78, 0xff},
	color.RGBA{0x7e, 0xed, 0x56, 0xff}, color.RGBA{0x0, 0x75, 0x6f, 0xff}, color.RGBA{0x0, 0x9e, 0xaa, 0xff}, color.RGBA{0x0, 0xcc, 0xc0, 0xff},
	color.RGBA{0x24, 0x50, 0xa4, 0xff}, color.RGBA{0x36, 0x90, 0xea, 0xff}, color.RGBA{0x51, 0xe9, 0xf4, 0xff}, color.RGBA{0x49, 0x3a, 0xc1, 0xff},
	color.RGBA{0x6a, 0x5c, 0xff, 0xff}, color.RGBA{0x94, 0xb3, 0xff, 0xff}, color.RGBA{0x81, 0x1e, 0x9f, 0xff}, color.RGBA{0xb4, 0x4a, 0xc0, 0xff},
	color.RGBA{0xe4, 0xab, 0xff, 0xff}, color.RGBA{0xde, 0x10, 0x7f, 0xff}, color.RGBA{0xff, 0x38, 0x81, 0xff}, color.RGBA{0xff, 0x99, 0xaa, 0xff},
	color.RGBA{0x6d, 0x48, 0x2f, 0xff}, color.RGBA{0x9c, 0x69, 0x26, 0xff}, color.RGBA{0xff, 0xb4, 0x70, 0xff}, color.RGBA{0x0, 0x0, 0x0, 0xff},
	color.RGBA{0x51, 0x52, 0x52, 0xff}, color.RGBA{0x89, 0x8d, 0x90, 0xff}, color.RGBA{0xd4, 0xd7, 0xd9, 0xff}, color.RGBA{0xff, 0xff, 0xff, 0xff},
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	driver.Main(func(s screen.Screen) {
		// TODO: view multiple images.
		var src image.Image
		src = image.NewPaletted(image.Rect(0, 0, 2000, 2000), palette)

		wim := widget.NewImage(src, src.Bounds())
		root := widget.NewSheet(wim)

		w, err := s.NewWindow(&screen.NewWindowOptions{
			Title: "r/place timelapse",
		})
		if err != nil {
			log.Fatal(err)
		}
		defer w.Release()

		go func() {
			im := src.(*image.Paletted)

			for i := 0; i < len(im.Pix); i++ {
				im.Pix[i] = 31
			}

			r, err := os.Open(*inFile)
			if err != nil {
				log.Fatal(err)
			}
			defer r.Close()

			br := bufio.NewReader(r)

			var buf [8]byte

			br.Read(buf[:8])

			if !reflect.DeepEqual(buf[:], []byte("PIXELPAK")) {
				log.Fatal("unknown header", buf)
			}

			br.Read(buf[:8])
			// start_time := binary.LittleEndian.Uint64(buf[:8])

			for {
				for i := 0; i < 10000; i++ {
					n, err := br.Read(buf[:8])
					if err == io.EOF || n != 8 {
						break
					}

					packed := binary.LittleEndian.Uint32(buf[:4])
					// time_offset := binary.LittleEndian.Uint32(buf[4:])

					color := (packed >> 22) & 31
					x := packed & 0x7FF
					y := (packed >> 11) & 0x7FF

					im.SetColorIndex(int(x), int(y), uint8(color))
				}

				wim.Mark(node.MarkNeedsPaintBase)
				w.Send(paint.Event{})
				time.Sleep(10 * time.Millisecond)
			}
		}()

		paintPending := false

		t := &theme.Theme{}

		gef := gesture.EventFilter{EventDeque: w}

		for {
			e := w.NextEvent()

			if e = gef.Filter(e); e == nil {
				continue
			}

			/*
				format := "got %#v\n"
				if _, ok := e.(fmt.Stringer); ok {
					format = "got %v\n"
				}
				fmt.Printf(format, e)
			*/

			switch e := e.(type) {
			case lifecycle.Event:
				root.OnLifecycleEvent(e)
				if e.To == lifecycle.StageDead {
					return
				}

			case gesture.Event, mouse.Event:
				root.OnInputEvent(e, image.Point{})

			case paint.Event:
				ctx := &node.PaintContext{
					Theme:  t,
					Screen: s,
					Drawer: w,
					Src2Dst: f64.Aff3{
						1, 0, 0,
						0, 1, 0,
					},
				}
				if err := root.Paint(ctx, image.Point{}); err != nil {
					log.Fatal(err)
				}
				w.Publish()
				paintPending = false

			case size.Event:
				if dpi := float64(e.PixelsPerPt) * unit.PointsPerInch; dpi != t.GetDPI() {
					newT := new(theme.Theme)
					if t != nil {
						*newT = *t
					}
					newT.DPI = dpi
					t = newT
				}

				size := e.Size()
				root.Measure(t, size.X, size.Y)
				root.Wrappee().Rect = e.Bounds()
				root.Layout(t)
				// TODO: call Mark(node.MarkNeedsPaint)?

			case error:
				log.Fatal(e)
			}

			if !paintPending && root.Wrappee().Marks.NeedsPaint() {
				paintPending = true
				w.Send(paint.Event{})
			}
		}

	})
}
