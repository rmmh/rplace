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

	ims := []*image.Paletted{}

	maxTs := 0
	for quad := 0; quad < 4; quad++ {
		e := s.dr.FindNearestLeft(ts, quad)
		if e != nil && e.Ts > maxTs {
			maxTs = e.Ts
		}
	}

	if maxTs != ts && !fullReq {
		http.Redirect(w, r, fmt.Sprintf("%d.png", maxTs), http.StatusMovedPermanently)
		return
	}

	for quad := 0; quad < 4; quad++ {
		e := s.dr.FindNearestLeft(ts, quad)
		if e == nil {
			continue
		}
		log.Printf("%d quad %d -> %s", ts, quad, e.F.Name)
		if math.Abs(float64(e.Ts-ts)) < 180_000 {
			im, err := s.dr.GetImage(e)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			ims = append(ims, im)
		}
	}

	width := 1000
	height := 1000
	switch len(ims) {
	case 0, 3:
		http.Error(w, "no images found", 404)
		return
	case 2:
		width = 2000
	case 4:
		width = 2000
		height = 2000
	}

	if fullReq {
		width = 2000
		height = 2000
	}

	var out *image.Paletted
	if width == 1000 && height == 1000 {
		out = ims[0]
	} else {
		out = image.NewPaletted(image.Rect(0, 0, width, height), ims[0].Palette)
		for i, im := range ims {
			for y := 0; y < 1000; y++ {
				for x := 0; x < 1000; x++ {
					out.SetColorIndex(x+1000*(i%2), y+1000*(i/2), im.ColorIndexAt(x, y))
				}
			}
		}
	}

	w.Header().Add("cache-control", "max-age=25920000")
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err := enc.Encode(w, out)
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
<title>r/Place 2022 Timeline</title>
</head>
<body style="overflow:hidden;margin:0;background-color:black">
<div style="margin:5px">
<input id="slider" type="range" min="1648818287221" max="1649116859027" value="1648818287221" style="width:100%"><br>
<input id="slider2" type="range" min="-60000" max="60000" value="0" style="width:100%"><br>
</div>
<div id="viewport" style="height:100%;user-select:none;overflow:clip">
<img id="canvas" style="image-rendering:pixelated;touch-action:none" src="full/1648818287221.png" ondragstart="return false">
</div>
</body>
<script type="text/javascript">
var lastUpdate = 0;
function updateImage() {
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
