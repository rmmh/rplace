package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/image/draw"

	"github.com/rmmh/rplace/delta"
)

type server struct {
	dr  *delta.DeltaReader
	col *ColumnarReader
}

func (s *server) fullHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ts, _ := strconv.Atoi(vars["ts"])

	fullReq := r.URL.Query().Has("full")

	ims := [6]*image.Paletted{}

	maxTs := 0
	for canvas := 0; canvas < 6; canvas++ {
		e := s.dr.FindNearestLeft(ts, canvas)
		if e != nil && e.Ts > maxTs {
			maxTs = e.Ts
		}
	}

	if maxTs != ts && !fullReq {
		http.Redirect(w, r, fmt.Sprintf("%d.png", maxTs), http.StatusMovedPermanently)
		return
	}

	for canvas := 0; canvas < 6; canvas++ {
		e := s.dr.FindNearestLeft(ts, canvas)
		if e == nil {
			continue
		}
		log.Printf("%d canvas %d -> %s", ts, canvas, e.F.Name)
		if math.Abs(float64(e.Ts-ts)) < 180_000 {
			im, err := s.dr.GetImage(e)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			ims[canvas] = im
		}
	}

	width := 3000
	height := 2000

	if ims[1] == nil {
		w.WriteHeader(404)
		return
	}

	var out *image.Paletted
	out = image.NewPaletted(image.Rect(0, 0, width, height), ims[1].Palette)
	for i, im := range ims {
		if im == nil {
			continue
		}
		ox := (i % 3) * 1000
		oy := (i / 3) * 1000
		for y := 0; y < 1000; y++ {
			for x := 0; x < 1000; x++ {
				out.SetColorIndex(x+ox, y+oy, im.ColorIndexAt(x, y))
			}
		}
	}

	var cropleft, cropright, croptop, cropbottom int

cropleft:
	for ; true; cropleft += 500 {
		for y := 0; y < height; y++ {
			for x := 0; x < 500; x++ {
				c := out.ColorIndexAt(x+cropleft, y)
				if c != 0 && c != 32 {
					break cropleft
				}
			}
		}
	}
cropright:
	for ; ; cropright += 500 {
		for y := 0; y < height; y++ {
			for x := 1; x <= 500; x++ {
				c := out.ColorIndexAt(width-x-cropright, y)
				if c != 0 && c != 32 {
					break cropright
				}
			}
		}
	}
croptop:
	for ; ; croptop += 500 {
		for y := 0; y < 500; y++ {
			for x := 0; x < 3000; x++ {
				c := out.ColorIndexAt(x, y+croptop)
				if c != 0 && c != 32 {
					break croptop
				}
			}
		}
	}
cropbottom:
	for ; ; cropbottom += 500 {
		for y := 1; y <= 500; y++ {
			for x := 0; x < width; x++ {
				c := out.ColorIndexAt(x, height-y-cropbottom)
				if c != 0 && c != 32 {
					break cropbottom
				}
			}
		}
	}
	cropped := image.NewPaletted(image.Rect(0, 0, width-cropleft-cropright, height-croptop-cropbottom), out.Palette)
	for y := croptop; y < height-cropbottom; y++ {
		for x := cropleft; x < width-cropright; x++ {
			cropped.SetColorIndex(x-cropleft, y-croptop, out.ColorIndexAt(x, y))
		}
	}

	w.Header().Add("cache-control", "max-age=25920000")
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err := enc.Encode(w, cropped)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

var pal = color.Palette{
	color.NRGBA{0x0, 0x0, 0x0, 0x0}, color.RGBA{0x6d, 0x0, 0x1a, 0xff}, color.RGBA{0xbe, 0x0, 0x39, 0xff}, color.RGBA{0xff, 0x45, 0x0, 0xff},
	color.RGBA{0xff, 0xa8, 0x0, 0xff}, color.RGBA{0xff, 0xd6, 0x35, 0xff}, color.RGBA{0xff, 0xf8, 0xb8, 0xff}, color.RGBA{0x0, 0xa3, 0x68, 0xff},
	color.RGBA{0x0, 0xcc, 0x78, 0xff}, color.RGBA{0x7e, 0xed, 0x56, 0xff}, color.RGBA{0x0, 0x75, 0x6f, 0xff}, color.RGBA{0x0, 0x9e, 0xaa, 0xff},
	color.RGBA{0x0, 0xcc, 0xc0, 0xff}, color.RGBA{0x24, 0x50, 0xa4, 0xff}, color.RGBA{0x36, 0x90, 0xea, 0xff}, color.RGBA{0x51, 0xe9, 0xf4, 0xff},
	color.RGBA{0x49, 0x3a, 0xc1, 0xff}, color.RGBA{0x6a, 0x5c, 0xff, 0xff}, color.RGBA{0x94, 0xb3, 0xff, 0xff}, color.RGBA{0x81, 0x1e, 0x9f, 0xff},
	color.RGBA{0xb4, 0x4a, 0xc0, 0xff}, color.RGBA{0xe4, 0xab, 0xff, 0xff}, color.RGBA{0xde, 0x10, 0x7f, 0xff}, color.RGBA{0xff, 0x38, 0x81, 0xff},
	color.RGBA{0xff, 0x99, 0xaa, 0xff}, color.RGBA{0x6d, 0x48, 0x2f, 0xff}, color.RGBA{0x9c, 0x69, 0x26, 0xff}, color.RGBA{0xff, 0xb4, 0x70, 0xff},
	color.RGBA{0x0, 0x0, 0x0, 0xff}, color.RGBA{0x51, 0x52, 0x52, 0xff}, color.RGBA{0x89, 0x8d, 0x90, 0xff}, color.RGBA{0xd4, 0xd7, 0xd9, 0xff},
	color.RGBA{0xff, 0xff, 0xff, 0xff},
}

func (s *server) deltaHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ts, _ := strconv.Atoi(vars["ts"])
	quad, _ := strconv.Atoi(vars["quad"])

	e, ok := s.dr.FileMap[quad][ts]
	if !ok {
		http.Error(w, "no images found", 404)
		return
	}

	s.dr.L.Lock()
	defer s.dr.L.Unlock()
	fr, err := e.F.Open()
	if err != nil {
		http.Error(w, err.Error(), 500)
	}
	defer fr.Close()
	_, err = io.Copy(w, fr)
	if err != nil {
		http.Error(w, err.Error(), 500)
	}
}

var indexTmpl = template.Must(template.New("index").Parse(`
<html>
<head>
<title>r/Place 2023 Timeline</title>
</head>
<body style="overflow:hidden;margin:0;background-color:black;color:white;">
<div style="margin:5px;display:flex;">
<span id="timestamp"></span>&nbsp;<br>
<input id="slider" type="range" min="1689858080999" max="1690320892999" value="1689858080999" style="width:100%">
</div>
<div style="margin:5px;display:flex;">
<input id="slider2" type="range" min="-60000" max="60000" value="0" style="width:100%"><br>
</div>
<div id="viewport" style="height:100%;user-select:none;overflow:clip">
<img id="canvas" style="image-rendering:pixelated;touch-action:none" src="full/1689858080999.png" ondragstart="return false">
</div>
</body>
<script type="text/javascript">
var lastUpdate = 0;
function updateImage() {
	timestamp.innerText = new Date(+slider.value).toISOString().slice(0, 19);
	if (!canvas.complete) {
		setTimeout(updateImage, 100);
		return;
	}
	lastUpdate = +new Date();
	canvas.src = "full/" + (+slider.value + +slider2.value) + ".png";
}
slider2.oninput = slider.oninput = updateImage;

var zoom=1;

viewport.onwheel = function(e) {
	zoom = Math.min(10, Math.max(0, Math.sign(e.deltaY) + zoom|0));
	if (zoom === 0) {
		zoom = 0.5;
	}
	canvas.style["image-rendering"] = zoom < 1 ? 'auto' : 'pixelated';
	updateTransform();
}

function updateTransform() {
	canvas.style.transform = "translate(" + tx + "px," + ty + "px) scale(" + zoom + ")";
}

var tx = 0, ty = 0;
viewport.onmousemove = function(e) {
	if (e.buttons) {
		tx = Math.min(2000, Math.max(-3000, tx + e.movementX));
		ty = Math.min(2000, Math.max(-3000, ty + e.movementY));
		console.log(tx, ty);
		updateTransform();
	}
}

updateTransform();

setInterval(function() {
	if (new Date() - lastUpdate < 100) {
		return;
	}
	slider.value = Math.min(+slider.max,+slider.value + 30000);
	updateImage();
}, 100)
</script>
</html>
`))

func (s *server) indexHandler(w http.ResponseWriter, r *http.Request) {
	err := indexTmpl.Execute(w, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

type ColumnarReader struct {
	f       io.ReaderAt
	startTs uint64
	offsets [3000*2000 + 1]uint32
}

func MakeColumnarReader(filename string) (*ColumnarReader, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 8)
	f.Read(buf[:8])

	if !reflect.DeepEqual(buf[:8], []byte("COLMPACK")) {
		log.Fatal("unknown header", buf[:8])
	}

	r := &ColumnarReader{
		f: f,
	}

	var o uint32
	binary.Read(f, binary.LittleEndian, &r.startTs)
	binary.Read(f, binary.LittleEndian, &o)

	r.offsets[0] = o
	br := bufio.NewReader(f)
	for i := 0; i < 3000*2000; i++ {
		n, err := binary.ReadUvarint(br)
		if err != nil {
			return nil, err
		}
		o += uint32(n)
		r.offsets[i+1] = o
	}

	fmt.Println(r.startTs, o, r.offsets[:20])

	return r, nil
}

type historyEntry uint64

func (h historyEntry) color() uint8 {
	return uint8(h & 31)
}

func (h historyEntry) ts() uint32 {
	return uint32(h >> 5)
}

func (h historyEntry) String() string {
	return fmt.Sprintf("%d:%d", h.ts(), h.color())
}

func (r *ColumnarReader) GetPixelHistory(x, y int) []historyEntry {
	if x < 0 || x >= 3000 || y < 0 || y >= 2000 {
		return nil
	}
	o := x + y*3000
	buf := make([]byte, r.offsets[o+1]-r.offsets[o])
	r.f.ReadAt(buf, int64(r.offsets[o]))

	ents := make([]historyEntry, 0, len(buf)/2)
	for o := 0; o < len(buf); {
		e, n := binary.Uvarint(buf[o:])
		if n == 0 {
			break
		}
		//fmt.Println("getpixelhistory", x, y, len(buf), o, e, n)
		o += n
		ents = append(ents, historyEntry(e))
	}

	return ents
}

func (s *server) gifHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	vals := r.URL.Query()
	cx, _ := strconv.Atoi(vars["x"])
	cy, _ := strconv.Atoi(vars["y"])

	width, _ := strconv.Atoi(vars["w"])
	height, _ := strconv.Atoi(vars["h"])
	scale := 1
	if s, ok := vals["scale"]; ok && len(s) == 1 {
		scale, _ = strconv.Atoi(s[0])
		if scale > 4 || scale < 1 {
			scale = 1
		}
	}

	if width*height > 600*600*scale*scale {
		http.Error(w, "too big", 400)
		return
	}

	interval := uint32(1000 * 37.6 * 1)
	interval = 1000 * 60 * 1
	frameCount := 462812000 / int(interval)
	g := gif.GIF{
		Image: make([]*image.Paletted, frameCount),
		Delay: make([]int, frameCount),
		Config: image.Config{
			ColorModel: pal,
			Width:      width,
			Height:     height,
		},
		BackgroundIndex: 0,
	}
	nonEmptyFrames := make([]bool, frameCount)
	fmt.Println("VARS", cx, cy, width, height, frameCount, len(nonEmptyFrames))

	for i := 0; i < frameCount; i++ {
		g.Image[i] = image.NewPaletted(image.Rect(0, 0, width, height), pal)
		g.Delay[i] = 2
	}

	maxTT := uint32(0)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			hist := s.col.GetPixelHistory(cx-width/2+x, cy-height/2+y)
			// fmt.Println(x, y, hist)
			c := uint8(32)
			maxT := interval
			i := 0
			t := uint32(0)
		histloop:
			for _, e := range hist {
				t += e.ts()
				for t > maxT {
					g.Image[i].SetColorIndex(x, y, c)
					if c != 32 {
						nonEmptyFrames[i] = true
					}
					maxT += interval
					i++
					if i >= len(g.Image) {
						break histloop
					}
				}
				c = e.color() + 1
			}
			if maxT-interval > maxTT {
				maxTT = maxT - interval
			}

		}
	}
	fmt.Println("last frame", uint64(maxTT)+s.col.startTs, s.col.startTs)

	// clear early blank white images
	for i, e := range nonEmptyFrames {
		if e {
			g.Image = g.Image[i:]
			g.Delay = g.Delay[i:]
			frameCount -= i
			break
		}
	}

	for i := frameCount - 1; i > 0; i-- {
		for j := 0; j < width*height; j++ {
			if g.Image[i].Pix[j] == g.Image[i-1].Pix[j] {
				g.Image[i].Pix[j] = 0
			}
		}
	}

	if scale != 1 {
		g.Config.Width *= scale
		g.Config.Height *= scale
		for i := 0; i < frameCount; i++ {
			dst := image.NewPaletted(image.Rect(0, 0, width*scale, height*scale), pal)
			draw.NearestNeighbor.Scale(dst, dst.Rect, g.Image[i], g.Image[i].Bounds(), draw.Over, nil)
			g.Image[i] = dst
		}
	}

	for i := 0; i < frameCount; i++ {
		im := g.Image[i]

		var cropleft, cropright, croptop, cropbottom int

		w := im.Rect.Dx()
		h := im.Rect.Dy()

	cropleft:
		for ; cropleft < w; cropleft++ {
			for y := 0; y < h; y++ {
				c := im.ColorIndexAt(cropleft, y)
				if c != 0 {
					break cropleft
				}
			}
		}
	cropright:
		for cropright = w - 1; cropright > cropleft; cropright-- {
			for y := 0; y < h; y++ {
				c := im.ColorIndexAt(cropright, y)
				if c != 0 {
					break cropright
				}
			}
		}
	croptop:
		for ; croptop < h; croptop++ {
			for x := 0; x < w; x++ {
				c := im.ColorIndexAt(x, croptop)
				if c != 0 {
					break croptop
				}
			}
		}

	cropbottom:
		for cropbottom = h - 1; cropbottom > 0; cropbottom-- {
			for x := 0; x < w; x++ {
				c := im.ColorIndexAt(x, cropbottom)
				if c != 0 {
					break cropbottom
				}
			}
		}

		g.Image[i] = im.SubImage(image.Rect(cropleft, croptop, cropright+1, cropbottom+1)).(*image.Paletted)
	}

	// coalesce empty frames into previous ones
	empty := 0
	for i := len(g.Image) - 1; i > 0; i-- {
		if g.Image[i].Rect.Dx() == 0 {
			g.Delay[i-1] += g.Delay[i]
			empty++
			// this is the simple O(N^2) way to do the coalesce
			copy(g.Image[i:], g.Image[i+1:])
			copy(g.Delay[i:], g.Delay[i+1:])
			g.Image = g.Image[:len(g.Image)-1]
			g.Delay = g.Delay[:len(g.Delay)-1]
		}
	}
	if empty > 0 {
		fmt.Println("removed", empty, "empty frames")
	}

	gif.EncodeAll(w, &g)
}

func main() {
	dataDir := flag.String("datadir", ".", "directory holding canvas zips")
	port := flag.Int("port", 9999, "port number to listen on")
	column := flag.String("column", "", "columnar datafile to generate gifs from")
	flag.Parse()

	fname := filepath.Join(*dataDir, "canvas_full.zip")
	dname := filepath.Join(*dataDir, "canvas_delta.zip")
	tname := filepath.Join(*dataDir, "canvas_ticks.zip")
	if _, err := os.Stat(dname); err != nil && !os.IsNotExist(err) {
		dname = ""
		tname = ""
	} else {
		if _, err := os.Stat(tname); err != nil && os.IsNotExist(err) {
			tname = ""
		}
	}

	log.Println(fname, dname, tname)

	dr, err := delta.MakeDeltaReader(fname, dname, tname)
	if err != nil {
		log.Fatal(err)
	}

	var col *ColumnarReader

	if *column != "" {
		col, err = MakeColumnarReader(*column)
		if err != nil {
			log.Fatal(err)
		}
	}

	r := mux.NewRouter()
	s := &server{
		dr:  dr,
		col: col,
	}

	r.HandleFunc("/", s.indexHandler)
	r.HandleFunc("/full/{ts:[0-9]+}.png", s.fullHandler)
	r.HandleFunc("/delta/{quad:[0-3]}/{ts:[0-9]+}.png", s.deltaHandler)
	if col != nil {
		r.HandleFunc("/gif/{x:[0-9]+}_{y:[0-9]+}-{w:[0-9]+}x{h:[0-9]+}.gif", s.gifHandler)
	}

	srv := &http.Server{
		Handler:      r,
		Addr:         fmt.Sprintf("127.0.0.1:%d", *port),
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
	}

	log.Println("listening on", srv.Addr)

	log.Fatal(srv.ListenAndServe())
}
