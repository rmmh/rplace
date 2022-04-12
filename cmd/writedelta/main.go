package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base32"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/sync/semaphore"

	"github.com/rmmh/rplace/delta"
)

var (
	dataDir = flag.String("datadir", ".", "directory to store canvas zips")
	imgDir  = flag.String("imgdir", "", "directory to read pngs from")
	urls    = flag.String("urls", "", "file of full image urls to inject as canvas_ticks.zip")
)

func loadPng(path string) *image.Paletted {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(path, err)
	}
	im, err := png.Decode(f)
	if err != nil {
		log.Fatal(path, err)
	}
	err = f.Close()
	if err != nil {
		log.Fatal(path, err)
	}
	return im.(*image.Paletted)
}

type TimestampedImage struct {
	ts, quad int
	path     string
	img      *image.Paletted
}

func imhash(im image.Image) string {
	h := sha1.New()
	bmp.Encode(h, im)
	return base32.StdEncoding.EncodeToString(h.Sum(nil))
}

func computeDelta(base, target *image.Paletted) *image.Paletted {
	// TODO: this palette is wasteful: the base palette *already* has 0 as the
	// transparent color, but there's a fair amount of data to fix.
	delta := image.NewPaletted(image.Rect(0, 0, 1000, 1000), append(color.Palette{color.Transparent}, base.Palette...))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 1000; x++ {
			newColor := target.ColorIndexAt(x, y)
			if newColor != base.ColorIndexAt(x, y) {
				delta.SetColorIndex(x, y, newColor+1)
			}
		}
	}
	return delta
}

func writeDelta(target, base TimestampedImage, n int, add OrderedZipAdder) {
	im := loadPng(target.path)
	hashInput := imhash(im)
	diff := computeDelta(base.img, im)
	header := &zip.FileHeader{
		Name:     fmt.Sprintf("%d-%d-%d.png", target.ts, target.quad, base.ts),
		Modified: time.Unix(0, int64(target.ts)*int64(time.Millisecond)),
	}
	pngbuf := bytes.Buffer{}
	png.Encode(&pngbuf, diff)
	add(header, &pngbuf, n)

	recon := delta.ApplyDelta(base.img, diff)
	hashRecon := imhash(recon)
	if hashRecon != hashInput {
		log.Fatal("RECON FAILED")
	}
}

type OrderedZipWriter struct {
	w    *zip.Writer
	n, t int
	c    sync.Cond
}

func NewOrderedZipWriter(w io.Writer) *OrderedZipWriter {
	return &OrderedZipWriter{
		w: zip.NewWriter(w),
		c: *sync.NewCond(&sync.Mutex{}),
	}
}

type OrderedZipAdder func(header *zip.FileHeader, r io.Reader, n int)

func (o *OrderedZipWriter) Add(header *zip.FileHeader, r io.Reader, n int) {
	o.c.L.Lock()
	for o.n != n {
		o.c.Wait()
	}
	w, err := o.w.CreateHeader(header)
	if err != nil {
		log.Fatal(err)
	}
	io.Copy(w, r)
	o.n++
	o.c.L.Unlock()
	o.c.Broadcast()
}

func (o *OrderedZipWriter) NextNumber() int {
	r := o.t
	o.t++
	return r
}

func (o *OrderedZipWriter) Close() error {
	return o.w.Close()
}

func makeFullDelta() {
	bf, err := os.Create(filepath.Join(*dataDir, "canvas_full.zip"))
	if err != nil {
		log.Fatal(err)
	}
	defer bf.Close()

	bfw := NewOrderedZipWriter(bf)

	bd, err := os.Create(filepath.Join(*dataDir, "canvas_delta.zip"))
	if err != nil {
		log.Fatal(err)
	}
	defer bd.Close()

	bdw := NewOrderedZipWriter(bd)

	n := 0
	inpSize := 0

	semWeight := int64(64)
	sem := semaphore.NewWeighted(semWeight)

	for quad := 0; quad <= 4; quad++ {
		pattern := fmt.Sprintf("%s/*-%d-f-*.png", *imgDir, quad)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			log.Fatal(err)
		}

		n += len(matches)

		sort.Strings(matches)

		images := []TimestampedImage{}
		for _, path := range matches {
			ts, err := strconv.Atoi(strings.Split(filepath.Base(path), "-")[0])
			if err != nil {
				log.Fatal(path, err)
			}
			// if ts > 1648862867221 {break}
			st, err := os.Stat(path)
			if err != nil {
				log.Fatal(path, err)
			}
			inpSize += int(st.Size())
			images = append(images, TimestampedImage{ts: ts, quad: quad, path: path})
		}

		baseImages := []TimestampedImage{}

		// first, determine the base images at given intervals
		for queryTime := 1648817087221; queryTime <= 1649116967221; queryTime += 120_000 {
			i := sort.Search(len(images), func(i int) bool {
				return images[i].ts > queryTime
			}) - 1
			if i < 0 {
				continue
			}
			match := images[i]
			if len(baseImages) > 0 && match.ts == baseImages[len(baseImages)-1].ts {
				continue
			}
			fmt.Println("BASELOAD:", queryTime, match.path)
			match.img = loadPng(match.path)
			baseImages = append(baseImages, match)
			sem.Acquire(context.Background(), 1)
			go func(i TimestampedImage, n int) {
				pngbuf := bytes.Buffer{}
				err := png.Encode(&pngbuf, i.img)
				if err != nil {
					log.Fatal(err)
				}
				bfw.Add(&zip.FileHeader{
					Name:     fmt.Sprintf("%d-%d.png", i.ts, quad),
					Modified: time.Unix(0, int64(i.ts)*int64(time.Millisecond)),
				}, &pngbuf, n)
				sem.Release(1)
			}(match, bfw.NextNumber())
		}

		// then, create image deltas off of the base images
		// this is split like this because sometimes the best image to delta against will be FORWARDS--
		// a delta for a base image 10 seconds in the future will be smaller than for a base image
		// 110 seconds in the past.
		lastInd := 0
		for _, match := range images {
			ind := sort.Search(len(baseImages), func(i int) bool {
				return baseImages[i].ts >= match.ts
			})
			if ind == len(baseImages) {
				ind--
			}
			if baseImages[ind].ts == match.ts {
				continue // base image, already stored
			}
			if ind > 0 && match.ts-baseImages[ind-1].ts < baseImages[ind].ts-match.ts {
				ind--
			}
			base := baseImages[ind]
			if ind > lastInd {
				fmt.Println("BASE:", base.path)
				lastInd = ind
			}
			sem.Acquire(context.Background(), 1)
			go writeDelta(match, base, bdw.NextNumber(), func(header *zip.FileHeader, r io.Reader, n int) {
				bdw.Add(header, r, n)
				sem.Release(1)
			})
		}
	}

	// wait for all outstanding work to complete
	sem.Acquire(context.Background(), semWeight)

	// write central zip dirs
	bdw.Close()
	bfw.Close()

	bfo, _ := bf.Seek(0, io.SeekCurrent)
	bdo, _ := bd.Seek(0, io.SeekCurrent)

	fmt.Printf("%d %.2fMiB input => %.2fMiB base + %.2fMiB delta = %.2fMiB total\n",
		n, float64(inpSize)/1024/1024,
		float64(bfo)/1024/1024, float64(bdo)/1024/1024, float64(bfo+bdo)/1024/1024)
}

func writeTick(target TimestampedImage, base *delta.DeltaReaderEntry, baseImg *image.Paletted, n int, add OrderedZipAdder, fetch func(string) *image.Paletted) {
	im := fetch(target.path)
	hashInput := imhash(im)
	diff := computeDelta(baseImg, im)
	header := &zip.FileHeader{
		Modified: time.Unix(0, int64(target.ts)*int64(time.Millisecond)),
	}
	if base.Base > 0 {
		header.Name = fmt.Sprintf("%d-%d-%d-%d.png", target.ts, target.quad, base.Ts, base.Base)
	} else {
		header.Name = fmt.Sprintf("%d-%d-%d.png", target.ts, target.quad, base.Ts)
	}
	pngbuf := bytes.Buffer{}
	png.Encode(&pngbuf, diff)
	add(header, &pngbuf, n)

	recon := delta.ApplyDelta(baseImg, diff)
	hashRecon := imhash(recon)
	if hashRecon != hashInput {
		log.Fatal("RECON FAILED")
	}
}

func makeTickDelta(urlspath string) {
	deltaReader, err := delta.MakeDeltaReader(
		filepath.Join(*dataDir, "canvas_full.zip"), filepath.Join(*dataDir, "canvas_delta.zip"), "")
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range deltaReader.Files[0] {
		fmt.Printf("%#v\n", f)
	}

	f, err := os.Open(urlspath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)

	semWeight := int64(8)
	sem := semaphore.NewWeighted(semWeight)

	tn := -1

	var zf *os.File
	var zw *OrderedZipWriter

	haves := map[int]bool{}

	for {
		p := fmt.Sprintf("%s/canvas_ticks.%05d.zip", *dataDir, tn+1)
		if _, err := os.Stat(p); err == nil {
			tn++
			f, err := zip.OpenReader(p)
			if err != nil {
				log.Fatal(err)
			}
			for _, e := range f.File {
				comps := strings.Split(filepath.Base(e.Name), "-")
				ts, err := strconv.Atoi(comps[0])
				if err != nil {
					log.Fatal(err)
				}
				quad, err := strconv.Atoi(comps[1])
				if err != nil {
					log.Fatal(err)
				}
				haves[ts<<2+quad] = true
			}
			continue
		}
		break
	}

	nextZip := func() {
		fmt.Println("NEXTZIP...")
		sem.Acquire(context.Background(), semWeight)
		fmt.Println("BOOYA")
		if zw != nil {
			zw.Close()
			zf.Close()
			os.Rename(
				fmt.Sprintf("%s/canvas_ticks.%05d.zip.tmp", *dataDir, tn),
				fmt.Sprintf("%s/canvas_ticks.%05d.zip", *dataDir, tn))
		}

		tn++
		zf, err = os.Create(fmt.Sprintf("%s/canvas_ticks.%05d.zip.tmp", *dataDir, tn))
		if err != nil {
			log.Fatal(err)
		}
		zw = NewOrderedZipWriter(zf)

		sem.Release(semWeight)
	}

	nextZip()

	cli := &http.Client{
		Timeout: time.Second * 10,
	}

	for s.Scan() {
		u := s.Text()
		comps := strings.Split(filepath.Base(u), "-")
		ts, err := strconv.Atoi(comps[0])
		if err != nil {
			log.Fatal(err)
		}
		quad, err := strconv.Atoi(comps[1])
		if err != nil {
			log.Fatal(err)
		}
		if haves[ts<<2+quad] {
			continue
		}
		m := deltaReader.FindNearest(ts, quad)
		if m.Ts == ts {
			fmt.Println("HAVE", ts)
			continue
		}
		fmt.Println(u, m.Ts, m.Base)
		im, err := deltaReader.GetImage(m)
		if err != nil {
			log.Fatal(err)
		}

		sem.Acquire(context.Background(), 1)
		go writeTick(TimestampedImage{ts: ts, quad: quad, path: u}, m, im, zw.NextNumber(), func(header *zip.FileHeader, r io.Reader, n int) {
			zw.Add(header, r, n)
			sem.Release(1)
		}, func(url string) *image.Paletted {
			for {
				resp, err := cli.Get(url)
				if err != nil {
					log.Println(url, err)
					time.Sleep(10 * time.Second)
					continue
				}
				if resp.StatusCode != 200 {
					resp.Body.Close()
					log.Println(url, resp.StatusCode)
					time.Sleep(10 * time.Second)
					continue
				}
				im, err := png.Decode(resp.Body)
				resp.Body.Close()
				if err != nil {
					log.Println("unable to decode image??")
					time.Sleep(10 * time.Second)
					continue
				}
				return im.(*image.Paletted)
			}
		})

		if zw.t >= 10000 {
			nextZip()
		}
	}

	sem.Acquire(context.Background(), semWeight)

	zw.Close()
	zf.Close()
	os.Rename(
		fmt.Sprintf("%s/canvas_ticks.%05d.zip.tmp", *dataDir, tn),
		fmt.Sprintf("%s/canvas_ticks.%05d.zip", *dataDir, tn))
}

func main() {
	flag.Parse()
	if *urls != "" {
		makeTickDelta(*urls)
	} else {
		makeFullDelta()
	}
}
