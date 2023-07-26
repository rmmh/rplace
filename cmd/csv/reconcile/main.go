package main

import (
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rmmh/rplace/delta"
)

var (
	csvFile   = flag.String("csv", "", "path of cleaned csv.gz input")
	canvasDir = flag.String("datadir", "", "path of canvas_*.zip files")
)

var colorToId = map[string]uint8{
	"#6D001A": 0,
	"#BE0039": 1,
	"#FF4500": 2,
	"#FFA800": 3,
	"#FFD635": 4,
	"#FFF8B8": 5,
	"#00A368": 6,
	"#00CC78": 7,
	"#7EED56": 8,
	"#00756F": 9,
	"#009EAA": 10,
	"#00CCC0": 11,
	"#2450A4": 12,
	"#3690EA": 13,
	"#51E9F4": 14,
	"#493AC1": 15,
	"#6A5CFF": 16,
	"#94B3FF": 17,
	"#811E9F": 18,
	"#B44AC0": 19,
	"#E4ABFF": 20,
	"#DE107F": 21,
	"#FF3881": 22,
	"#FF99AA": 23,
	"#6D482F": 24,
	"#9C6926": 25,
	"#FFB470": 26,
	"#000000": 27,
	"#515252": 28,
	"#898D90": 29,
	"#D4D7D9": 30,
	"#FFFFFF": 31,
}

func main() {
	flag.Parse()

	var f io.Reader
	f, err := os.Open(*csvFile)
	if err != nil {
		log.Fatal(err)
	}

	if strings.HasSuffix(*csvFile, ".gz") {
		f, err = gzip.NewReader(f)
		if err != nil {
			log.Fatal(err)
		}
	}

	cr := csv.NewReader(f)

	dr, err := delta.MakeDeltaReader(filepath.Join(*canvasDir, "canvas_full.zip"),
		filepath.Join(*canvasDir, "canvas_delta.zip"),
		filepath.Join(*canvasDir, "canvas_ticks.zip"),
	)

	if err != nil {
		log.Fatal(err)
	}

	lastImage, err := dr.GetImage(&dr.Files[0][len(dr.Files[0])-1])
	if err != nil {
		log.Fatal(err)
	}

	// snaps is an ordered list of every canvas stored in delta format
	snaps := append([]delta.DeltaReaderEntry{}, dr.Files[0]...)
	snaps = append(snaps, dr.Files[1]...)
	snaps = append(snaps, dr.Files[2]...)
	snaps = append(snaps, dr.Files[3]...)
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Ts < snaps[j].Ts
	})

	snapN := 0
	s := &snaps[snapN]
	si, err := dr.GetImage(s)
	if err != nil {
		log.Fatal(err)
	}

	state := image.NewPaletted(lastImage.Rect, lastImage.Palette)
	for i := 0; i < len(state.Pix); i++ {
		state.Pix[i] = 32
	}
	fmt.Println(len(lastImage.Palette))

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		if rec[0] == "timestamp_millis" {
			continue
		}

		ts, err := strconv.Atoi(rec[0])
		if err != nil {
			log.Fatal(err)
		}

		if ts >= s.Ts {
			for y := 0; y < 1000; y++ {
				for x := 0; x < 1000; x++ {
					wc := si.ColorIndexAt(x, y)
					sc := state.ColorIndexAt(x+1000*(s.Canvas%2), y+1000*(s.Canvas/2))
					if wc != sc {
						fmt.Printf("WHAT snap:%d @ %d (%d,%d) wc:%d sc:%d\n", snapN, s.Ts, x, y, wc, sc)
						state.SetColorIndex(x+1000*(s.Canvas%2), y+1000*(s.Canvas/2), wc)
					}
				}
			}
			snapN++
			s = &snaps[snapN]
			si, err = dr.GetImage(s)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			mm := 0
			for y := 0; y < 1000; y++ {
				for x := 0; x < 1000; x++ {
					wc := si.ColorIndexAt(x, y)
					sc := state.ColorIndexAt(x+1000*(s.Canvas%2), y+1000*(s.Canvas/2))
					if wc != sc {
						mm++
					}
				}
			}
			if mm == 0 {
				fmt.Printf("SNAP %d ACTUALLY CAPTURED AT %d\n", s.Ts, ts)
			}
		}

		/*
			uid, err := strconv.Atoi(rec[1])
			if err != nil {
				log.Fatal(err)
			}
		*/
		x, err := strconv.Atoi(rec[3])
		if err != nil {
			log.Fatal(err)
		}
		y, err := strconv.Atoi(rec[4])
		if err != nil {
			log.Fatal(err)
		}
		color, ok := colorToId[rec[2]]
		if !ok {
			log.Fatal("unknown color", rec[2])
		}

		state.SetColorIndex(x, y, color+1) // 0 is transparent
	}
}
