package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	inFile      = flag.String("in", "", "input file name")
	outFile     = flag.String("out", "", "output file name")
	startTs     = flag.Int64("start", 0, "start TS at this point")
	endTs       = flag.Int64("end", 0, "end TS at this point")
	dumpTs      = flag.Int64("ts", 0, "dump first snapshots after a given timestamp")
	maxImages   = flag.Int("maximages", 0, "stop after crunching this many images")
	crunch      = flag.Bool("crunch", false, "crunch bin into a denser format")
	column      = flag.Bool("column", false, "output columnar (per-pixel) event format")
	average     = flag.Bool("average", false, "produce an averaged image of the given time period")
	crunchSplit = flag.Int("crunchsplit", 0, "split crunch bins into segments this many seconds long")
	canvasDir   = flag.String("datadir", "", "path of canvas_*.zip files")
	cpuprofile  = flag.String("cpuprofile", "", "write cpu profile to file")
)

type Snapshot struct {
	key  SnapKey
	name string
	full bool
	src  *zip.Reader
	base *Snapshot
}

type SnapKey int64

func GetKey(c int, ts int64) SnapKey {
	return SnapKey(ts*16 + int64(c))
}

func (k SnapKey) Split() (int, int64) {
	return int(k & 15), int64(k / 16)
}

func (k SnapKey) Ts() int64 {
	_, ts := k.Split()
	return ts
}

func (k SnapKey) C() int {
	c, _ := k.Split()
	return c
}

func (k SnapKey) OffsetX() int {
	c, _ := k.Split()
	return (c % 3) * 1000
}
func (k SnapKey) OffsetY() int {
	c, _ := k.Split()
	return (c / 3) * 1000
}

func (s SnapKey) String() string {
	c, ts := s.Split()
	return fmt.Sprintf("%d-%d", c, ts)
}

type ImageStitcher struct {
	snaps          map[SnapKey]*Snapshot
	filenameToPrev map[string]int64
	cache          [6][16]struct {
		key   SnapKey
		Image *image.Paletted
	}
}

func NewImageStitcher(path string) *ImageStitcher {
	i := &ImageStitcher{
		snaps:          make(map[SnapKey]*Snapshot),
		filenameToPrev: make(map[string]int64),
	}

	wslogs, err := filepath.Glob(path + "/wslog*.txt")
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range wslogs {
		i.addWslog(f)
	}

	orph := 0
	zips, err := filepath.Glob(path + "/*.zip")
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range zips {
		orph += i.addZip(f)
	}

	fmt.Println("ignored", orph, "deltas missing predecessors.")

	return i
}

func (i *ImageStitcher) SortedSnaps() []SnapKey {
	snaps := make([]SnapKey, 0, len(i.snaps))
	for k := range i.snaps {
		snaps = append(snaps, k)
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i] < snaps[j] })
	return snaps
}

func (i *ImageStitcher) GetSnap(c int, ts int64) *Snapshot {
	return i.snaps[GetKey(c, ts)]
}

func ApplyDelta(base, delta *image.Paletted) *image.Paletted {
	if !base.Rect.Eq(delta.Rect) {
		panic("applying delta onto wrong-sized base")
	}
	combined := image.NewPaletted(base.Rect, base.Palette)
	copy(combined.Pix, base.Pix)
	_, _, _, a := delta.Palette[0].RGBA()
	if a != 0 {
		panic("expected transparency to be zero")
	}
	if len(delta.Palette) != 33 {
		panic(fmt.Sprintf("delta bad len %d", len(delta.Palette)))
	}
	if len(base.Palette) != 33 {
		panic(fmt.Sprintf("base bad len %d", len(base.Palette)))
	}
	for i := 0; i < len(delta.Pix); i++ {
		if ci := delta.Pix[i]; ci > 0 {
			//combined.Pix[i] = uint8(combined.Palette.Index(delta.Palette[delta.Pix[i]]))
			combined.Pix[i] = ci
		}
	}
	return combined
}

func (i *ImageStitcher) GetImage(k SnapKey) (*image.Paletted, error) {
	s := i.snaps[k]
	if s == nil {
		return nil, fmt.Errorf("bad image key: %s", k)
	}
	c := k.C()
	ent := &i.cache[c][(k>>4)&15]
	if ent.key == k {
		return ent.Image, nil
	}

	var err error
	var pi, base *image.Paletted
	if !s.full {
		base, err = i.GetImage(s.base.key)
		if err != nil {
			return nil, err
		}
	}

	f, err := s.src.Open(s.name)
	if err != nil {
		log.Fatal(s, err)
	}
	im, err := png.Decode(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("%s: %v", s.name, err)
	}
	pi = im.(*image.Paletted)
	if base != nil {
		pi = ApplyDelta(base, pi)
	}
	ent.key = k
	ent.Image = pi
	return pi, nil
}

func parseImageFilename(path string) (int, int64, bool) {
	path = filepath.Base(path)
	path = strings.Split(path, ".")[0]
	parts := strings.Split(path, "-")
	if len(parts) == 4 {
		ts, _ := strconv.ParseInt(parts[0], 10, 0)
		canvas, _ := strconv.Atoi(parts[1])
		full := parts[2] != "d"
		return canvas, ts, full
	}
	// the 30-second images
	ts, _ := strconv.ParseInt(parts[1], 10, 0)
	ts *= 1000
	canvas, _ := strconv.Atoi(parts[0])
	return canvas, ts, true
}

func (i *ImageStitcher) addZip(filename string) int {
	f, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	st, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}

	r, err := zip.NewReader(f, st.Size())
	if err != nil {
		log.Println(filename, err)
		return 0
	}

	orph := 0

	files := make([]string, 0, len(r.File))
	for _, f := range r.File {
		files = append(files, f.Name)
	}
	sort.Strings(files)

	for _, f := range files {
		if !strings.HasSuffix(f, ".png") {
			continue
		}
		canvas, ts, full := parseImageFilename(f)
		if filename == "framedata_from_discord.zip" && ts >= 1689873917000 {
			// idfk, dude
			continue
		}
		var base *Snapshot
		if !full {
			// only add delta frames if we have the required previous frame too
			baseTs := i.filenameToPrev[filepath.Base(f)]
			base = i.snaps[GetKey(canvas, baseTs)]
			if base == nil {
				orph++
				continue
			}
		}
		k := GetKey(canvas, ts)
		i.snaps[k] = &Snapshot{
			key:  k,
			name: f,
			src:  r,
			full: full,
			base: base,
		}
	}

	return orph
}

func (i *ImageStitcher) addWslog(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		var line struct {
			Ty                string `json:"__typename"`
			Name              string `json:"name"`
			CurrentTimestamp  int64  `json:"currentTimestamp"`
			PreviousTimestamp int64  `json:"previousTimestamp"`
		}
		err := json.Unmarshal(s.Bytes(), &line)
		if err == nil {
			if line.Ty == "DiffFrameMessageData" {
				i.filenameToPrev[filepath.Base(line.Name)] = line.PreviousTimestamp
			}
		}
	}
}

func writeEventsBinary() {
	if *canvasDir == "" {
		log.Fatal("-datadir is required")
	}

	i := NewImageStitcher(*canvasDir)
	snaps := i.SortedSnaps()
	fmt.Println("scanned", len(snaps), "images", snaps[:30], "...", snaps[len(snaps)-30:])

	wf, err := os.Create(*outFile)
	if err != nil {
		log.Fatal(err)
	}
	defer wf.Close()
	w := bufio.NewWriter(wf)
	defer w.Flush()

	// snaps is an ordered list of every canvas stored in delta format

	w.Write([]byte("PIXELPAK"))

	var buf [8]byte
	var start_time uint64

	firstImage, err := i.GetImage(snaps[0])
	if err != nil {
		log.Fatal(err)
	}

	state := image.NewPaletted(image.Rect(0, 0, 3000, 2000), firstImage.Palette)
	for i := 0; i < len(state.Pix); i++ {
		state.Pix[i] = 32
	}

	ev := 0

	for snapN, s := range snaps {
		if s.Ts() < 1689820000000 {
			continue
		}
		if *startTs != 0 && s.Ts() < *startTs {
			continue
		}
		if *endTs != 0 && s.Ts() > *endTs {
			break
		}

		if start_time == 0 {
			start_time = uint64(s.Ts())
			binary.LittleEndian.PutUint64(buf[:8], start_time)
			w.Write(buf[:8])
		}

		start := time.Now()
		si, err := i.GetImage(s)
		elapsed := time.Since(start)
		if err != nil {
			log.Println("error decoding", i.snaps[s].name, err)
			continue
		}
		if elapsed > 20*time.Millisecond {
			log.Println("SLOW", s, i.snaps[s].full, elapsed)
		}

		if *maxImages > 0 && snapN >= *maxImages {
			break
		}

		binary.LittleEndian.PutUint32(buf[4:], uint32(uint64(s.Ts())-start_time))

		sev := ev
		for y := 0; y < 1000; y++ {
			for x := 0; x < 1000; x++ {
				ox := x + s.OffsetX()
				oy := y + s.OffsetY()

				wc := si.Pix[x+y*1000]
				sc := state.Pix[ox+oy*3000]

				if wc != sc {
					ev++
					state.SetColorIndex(ox, oy, wc)
					// pixel changed event!
					// format:
					// 4B pos+color: 11b x, 11b y, 5b new color 5b old color
					// 23b timestamp: offset since start time (little endian)
					// 1b 12th bit of x offset
					// easily reversible!
					packed := uint32((uint32(ox) & 2047) + uint32(oy)<<11 + uint32(wc-1)<<22 + uint32(sc-1)<<27)
					binary.LittleEndian.PutUint32(buf[:4], packed)
					buf[7] = (buf[7] & 127) | byte((ox>>4)&128)
					w.Write(buf[:])
				}
			}
		}

		// if snapN > 1000 {break}

		if ev-sev > 10000 {
			fmt.Printf("\n!!! huge delta %d events generated %d\n\n", ev-sev, s.Ts())
		}

		if snapN&0x7f == 0 {
			sts := time.Unix(s.Ts()/1000, 0)
			fmt.Printf("%d/%d %d %d %s\r", snapN, len(snaps), ev, s.Ts(), sts.Format("2006-01-02 15:04:05"))
		}
	}
}

var palette = []string{
	"#6D001A", "#BE0039", "#FF4500", "#FFA800", "#FFD635", "#FFF8B8", "#00A368", "#00CC78",
	"#7EED56", "#00756F", "#009EAA", "#00CCC0", "#2450A4", "#3690EA", "#51E9F4", "#493AC1",
	"#6A5CFF", "#94B3FF", "#811E9F", "#B44AC0", "#E4ABFF", "#DE107F", "#FF3881", "#FF99AA",
	"#6D482F", "#9C6926", "#FFB470", "#000000", "#515252", "#898D90", "#D4D7D9", "#FFFFFF",
}

func crunchEventsBinary() {
	r, err := os.Open(*inFile)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	br := bufio.NewReader(r)

	var buf [10]byte
	var tstart [8]byte

	br.Read(buf[:8])
	if !reflect.DeepEqual(buf[:8], []byte("PIXELPAK")) {
		log.Fatal("unknown header", buf[:8])
	}

	br.Read(tstart[:8])
	startTime := binary.LittleEndian.Uint64(tstart[:])

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
		n += binary.PutUvarint(buf[n:], uint64(obcount<<4+int(curOct)))
		w.Write(buf[:n])
		w.Write(obuf)
		obuf = obuf[:0]
		obcount = 0
	}

	writeHeader()

	ocs := make([]uint8, 3000*2000)
	for i := range ocs {
		ocs[i] = 31
	}

	for {
		n, err := br.Read(buf[:8])
		if err == io.EOF || n != 8 {
			break
		}

		packed := binary.LittleEndian.Uint32(buf[:4])
		x := (packed & 0x7FF) | ((uint32(buf[7]) << 4) & 2048)
		y := (packed >> 11) & 0x7FF
		new_color := (packed >> 22) & 31
		old_color := (packed >> 27) & 31
		buf[7] &= 127
		timeOffset := binary.LittleEndian.Uint32(buf[4:])

		// fmt.Println(x, y, new_color, old_color, packed, timeOffset)

		oct := y/500 + 4*(x/1000)
		if oct >= 12 {
			panic("bad octant")
		}
		ocs[x+y*3000] ^= uint8(new_color ^ old_color)

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
	log.Println("splits:", splitN, "groups:", groupCount, "startTs:", startTime, "endTs:", curTs+startTime)
}

func crunchEventsColumn() {
	r, err := os.Open(*inFile)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	br := bufio.NewReader(r)

	var buf [10]byte
	var tstart [8]byte

	br.Read(buf[:8])
	if !reflect.DeepEqual(buf[:8], []byte("PIXELPAK")) {
		log.Fatal("unknown header", buf[:8])
	}

	br.Read(tstart[:8])

	if *outFile == "" {
		log.Fatal("required -out")
	}
	w, err := os.Create(*outFile)
	if err != nil {
		log.Fatal(err)
	}

	w.Write([]byte("COLMPACK"))
	w.Write(tstart[:8])

	lastTs := make([]uint32, 3000*2000)
	bufs := make([]bytes.Buffer, 3000*2000)

	for {
		n, err := br.Read(buf[:8])
		if err == io.EOF || n != 8 {
			break
		}

		packed := binary.LittleEndian.Uint32(buf[:4])
		x := (packed & 0x7FF) | ((uint32(buf[7]) << 4) & 2048)
		y := (packed >> 11) & 0x7FF
		new_color := (packed >> 22) & 31
		buf[7] &= 127
		timeOffset := binary.LittleEndian.Uint32(buf[4:])

		o := x + y*3000
		pixTs := lastTs[o]
		lastTs[o] = timeOffset

		n = binary.PutUvarint(buf[:], uint64(new_color)|(uint64(timeOffset-pixTs))<<5)
		bufs[o].Write(buf[:n])
	}

	var lenBuf bytes.Buffer
	for _, b := range bufs {
		n := binary.PutUvarint(buf[:], uint64(b.Len()))
		lenBuf.Write(buf[:n])
	}

	binary.Write(w, binary.LittleEndian, 20+uint32(lenBuf.Len()))
	w.Write(lenBuf.Bytes())

	for _, b := range bufs {
		_, err := w.Write(b.Bytes())
		if err != nil {
			log.Fatal(err)
		}
	}
}

func computeAverage() {
	if *canvasDir == "" {
		log.Fatal("-datadir is required")
	}

	i := NewImageStitcher(*canvasDir)
	snaps := i.SortedSnaps()
	fmt.Println("scanned", len(snaps), "images", snaps[:30], "...", snaps[len(snaps)-30:])

	counts := make([][33]int, 3000*2000)

	firstImage, err := i.GetImage(snaps[0])
	if err != nil {
		log.Fatal(err)
	}

	state := image.NewPaletted(image.Rect(0, 0, 3000, 2000), firstImage.Palette)
	for i := 0; i < len(state.Pix); i++ {
		state.Pix[i] = 32
	}

	for snapN, s := range snaps {
		if *startTs != 0 && s.Ts() < *startTs {
			continue
		}
		if *endTs != 0 && s.Ts() > *endTs {
			break
		}
		image, err := i.GetImage(s)
		if err != nil {
			log.Fatal(err)
		}

		for y := 0; y < 1000; y++ {
			for x := 0; x < 1000; x++ {
				ox := x + s.OffsetX()
				oy := y + s.OffsetY()
				counts[ox+oy*3000][image.Pix[x+y*1000]]++
			}
		}

		sts := time.Unix(s.Ts()/1000, 0)
		fmt.Printf("%d/%d %d %s\r", snapN, len(snaps), s.Ts(), sts.Format("2006-01-02 15:04:05"))
	}

	for i, cs := range counts {
		max := 0
		maxC := uint8(0)
		for j, c := range cs {
			if c > max {
				maxC = uint8(j)
				max = c
			}
		}
		state.Pix[i] = maxC
	}

	wf, err := os.Create(*outFile)
	if err != nil {
		log.Fatal(err)
	}
	defer wf.Close()

	err = png.Encode(wf, state)
	if err != nil {
		log.Fatal(err)
	}

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

	if *dumpTs != 0 {
		i := NewImageStitcher(*canvasDir)
		snaps := i.SortedSnaps()
		for _, c := range []int{1, 2, 5, 4} {
			for _, s := range snaps {
				if s.C() == c && s.Ts() > *dumpTs {
					fmt.Println(s)
					im, err := i.GetImage(s)
					if err != nil {
						log.Fatal(err)
					}
					f, err := os.Create("hack/" + s.String() + ".png")
					if err != nil {
						log.Fatal(err)
					}
					png.Encode(f, im)
					f.Close()
					break
				}
			}
		}
		return
	}

	if *outFile == "" {
		log.Fatal("-out is required")
	}

	if *average {
		computeAverage()
	} else if *crunch {
		crunchEventsBinary()
	} else if *column {
		crunchEventsColumn()
	} else {
		writeEventsBinary()
	}
}
