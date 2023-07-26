package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime/pprof"
	"sort"

	"github.com/rmmh/rplace/delta"
)

var (
	inFile      = flag.String("in", "", "input file name")
	outFile     = flag.String("out", "", "output file name")
	dumpCsv     = flag.Bool("dumpcsv", false, "dump csv")
	crunch      = flag.Bool("crunch", false, "crunch bin into a denser format")
	crunchSplit = flag.Int("crunchsplit", 0, "split crunch bins into segments this many seconds long")
	canvasDir   = flag.String("datadir", "", "path of canvas_*.zip files")
	cpuprofile  = flag.String("cpuprofile", "", "write cpu profile to file")
)

func writeEventsBinary() {
	if *canvasDir == "" {
		log.Fatal("-datadir is required")
	}

	dr, err := delta.MakeDeltaReader(filepath.Join(*canvasDir, "canvas_full.zip"),
		filepath.Join(*canvasDir, "canvas_delta.zip"),
		filepath.Join(*canvasDir, "canvas_ticks.zip"),
	)

	if err != nil {
		log.Fatal(err)
	}

	wf, err := os.Create(*outFile)
	if err != nil {
		log.Fatal(err)
	}
	defer wf.Close()
	w := bufio.NewWriter(wf)
	defer w.Flush()

	// snaps is an ordered list of every canvas stored in delta format
	snaps := append([]delta.DeltaReaderEntry{}, dr.Files[0]...)
	snaps = append(snaps, dr.Files[1]...)
	snaps = append(snaps, dr.Files[2]...)
	snaps = append(snaps, dr.Files[3]...)
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Ts < snaps[j].Ts
	})

	w.Write([]byte("PIXELPAK"))

	var buf [8]byte
	start_time := uint64(1648817050351)
	binary.LittleEndian.PutUint64(buf[:8], start_time)
	w.Write(buf[:8])

	firstImage, err := dr.GetImage(&dr.Files[0][0])
	if err != nil {
		log.Fatal(err)
	}

	state := image.NewPaletted(image.Rect(0, 0, 2000, 2000), firstImage.Palette)
	for i := 0; i < len(state.Pix); i++ {
		state.Pix[i] = 32
	}

	ev := 0

	for snapN, s := range snaps {
		si, err := dr.GetImage(&s)
		if err != nil {
			log.Fatal(err)
		}

		if snapN == 5000 {
			break
		}

		binary.LittleEndian.PutUint32(buf[4:], uint32(uint64(s.Ts)-start_time))

		for y := 0; y < 1000; y++ {
			for x := 0; x < 1000; x++ {
				ox := x + 1000*(s.Canvas%2)
				oy := y + 1000*(s.Canvas/2)

				wc := si.Pix[x+y*1000]
				sc := state.Pix[ox+oy*2000]

				if wc != sc {
					ev++
					state.SetColorIndex(ox, oy, wc)
					// pixel changed event!
					// format:
					// 4B pos+color: 11b x, 11b y, 5b new color 5b old color
					// 4B timestamp: offset since start time
					// easily reversible!
					packed := uint32(uint32(ox) + uint32(oy)<<11 + uint32(wc-1)<<22 + uint32(sc-1)<<27)
					binary.LittleEndian.PutUint32(buf[:4], packed)
					w.Write(buf[:])
				}
			}
		}

		// if snapN > 1000 {break}

		fmt.Printf("%d/%d %d %d\r", snapN, len(snaps), ev, s.Ts)
	}
}

var palette = []string{
	"#6D001A", "#BE0039", "#FF4500", "#FFA800", "#FFD635", "#FFF8B8", "#00A368", "#00CC78",
	"#7EED56", "#00756F", "#009EAA", "#00CCC0", "#2450A4", "#3690EA", "#51E9F4", "#493AC1",
	"#6A5CFF", "#94B3FF", "#811E9F", "#B44AC0", "#E4ABFF", "#DE107F", "#FF3881", "#FF99AA",
	"#6D482F", "#9C6926", "#FFB470", "#000000", "#515252", "#898D90", "#D4D7D9", "#FFFFFF",
}

func readEventsBinary() {
	r, err := os.Open(*inFile)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	br := bufio.NewReader(r)

	w := io.Writer(os.Stdout)
	if *outFile != "" {
		w, err = os.Create(*outFile)
		if err != nil {
			log.Fatal(err)
		}
	}

	var buf [8]byte

	fmt.Fprintln(w, "timestamp_millis,color,x,y")

	br.Read(buf[:8])

	if !reflect.DeepEqual(buf[:], []byte("PIXELPAK")) {
		log.Fatal("unknown header", buf)
	}

	br.Read(buf[:8])
	start_time := binary.LittleEndian.Uint64(buf[:8])

	for {
		n, err := br.Read(buf[:8])
		if err == io.EOF || n != 8 {
			break
		}

		packed := binary.LittleEndian.Uint32(buf[:4])
		time_offset := binary.LittleEndian.Uint32(buf[4:])

		fmt.Fprintf(w, "%d,%s,%d,%d\n", start_time+uint64(time_offset), palette[(packed>>22)&31], packed&0x7FF, (packed>>11)&0x7FF)
	}
}

func crunchEventsBinary() {
	r, err := os.Open(*inFile)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	br := bufio.NewReader(r)

	var buf [8]byte
	var tstart [8]byte

	br.Read(buf[:8])
	if !reflect.DeepEqual(buf[:], []byte("PIXELPAK")) {
		log.Fatal("unknown header", buf)
	}

	br.Read(tstart[:8])

	curTs := uint64(0)
	curOct := uint32(0)

	obuf := make([]byte, 0, 1024)
	obcount := 0
	lastTime := uint64(0)

	splitN := 0
	splitStart := 0

	w := io.WriteCloser(os.Stdout)

	newSplit := func() {
		if splitN > 0 {
			w.Close()
		}
		w, err = os.Create(fmt.Sprintf("%s.%03d.bin", *outFile, splitN))
		if err != nil {
			log.Fatal(err)
		}
		splitN++
	}

	if *crunchSplit != 0 {
		*crunchSplit *= 1000
		newSplit()
	} else if *outFile != "" {
		w, err = os.Create(*outFile)
		if err != nil {
			log.Fatal(err)
		}
	}

	writeHeader := func() {
		w.Write([]byte("PIXLPACK"))
		w.Write(tstart[:8])
		lastTime = 0
	}

	groupCount := 0

	writeChunk := func() {
		if obcount == 0 {
			return
		}
		groupCount++
		n := binary.PutUvarint(buf[:], curTs-lastTime)
		lastTime = curTs
		n += binary.PutUvarint(buf[n:], uint64(obcount<<3+int(curOct)))
		w.Write(buf[:n])
		w.Write(obuf)
		obuf = obuf[:0]
		obcount = 0
	}

	writeHeader()

	ocs := make([]uint8, 2000*2000)
	for i := range ocs {
		ocs[i] = 31
	}

	for {
		n, err := br.Read(buf[:8])
		if err == io.EOF || n != 8 {
			break
		}

		packed := binary.LittleEndian.Uint32(buf[:4])
		timeOffset := binary.LittleEndian.Uint32(buf[4:])

		new_color := (packed >> 22) & 31
		old_color := (packed >> 27) & 31
		x := packed & 0x7FF
		y := (packed >> 11) & 0x7FF
		oct := x/1000 + 2*(y/500)

		ocs[x+y*2000] ^= uint8(new_color ^ old_color)

		if uint64(timeOffset) != curTs || oct != curOct {
			writeChunk()
			curTs = uint64(timeOffset)
			curOct = oct
		}
		if *crunchSplit > 0 && splitStart+int(*crunchSplit) <= int(timeOffset) {
			newSplit()
			writeHeader()
			splitStart += *crunchSplit
		}

		repack := (x % 1000) | (y%500)<<10 | (new_color^old_color)<<19
		obuf = append(obuf, byte(repack), byte(repack>>8), byte(repack>>16))
		obcount++
	}
	writeChunk()
	log.Println("groups:", groupCount)
}

func main() {
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *outFile == "" {
		log.Fatal("-out is required")
	}

	if *dumpCsv {
		readEventsBinary()
	} else if *crunch {
		crunchEventsBinary()
	} else {
		writeEventsBinary()
	}
}
