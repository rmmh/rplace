package main

import (
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/rmmh/rplace/delta"
)

type server struct {
	dr *delta.DeltaReader
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

func main() {
	dataDir := flag.String("datadir", ".", "directory holding canvas zips")
	port := flag.Int("port", 9999, "port number to listen on")
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

	r := mux.NewRouter()
	s := &server{
		dr: dr,
	}

	r.HandleFunc("/", s.indexHandler)
	r.HandleFunc("/full/{ts:[0-9]+}.png", s.fullHandler)
	r.HandleFunc("/delta/{quad:[0-3]}/{ts:[0-9]+}.png", s.deltaHandler)

	srv := &http.Server{
		Handler:      r,
		Addr:         fmt.Sprintf("127.0.0.1:%d", *port),
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
	}

	log.Println("listening on", srv.Addr)

	log.Fatal(srv.ListenAndServe())
}
