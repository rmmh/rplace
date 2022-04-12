package delta

import (
	"archive/zip"
	"errors"
	"image"
	"image/png"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type SimpleCache[K comparable, V any] struct {
	cap int
	i   int
	k   []K
	m   map[K]V
}

func NewSimpleCache[K comparable, V any](size int) *SimpleCache[K, V] {
	return &SimpleCache[K, V]{
		cap: size,
		k:   make([]K, size),
		m:   make(map[K]V),
	}
}

func (s *SimpleCache[K, V]) Get(k K) (V, bool) {
	v, ok := s.m[k]
	return v, ok
}

func (s *SimpleCache[K, V]) Put(k K, v V) {
	if len(s.m) >= s.cap {
		delete(s.m, s.k[s.i])
		s.i = (s.i + 1) % s.cap
	}
	s.k[(s.i+len(s.m))%s.cap] = k
	s.m[k] = v
}

type DeltaReaderEntry struct {
	Ts, Quad    int
	Base, Delta int
	F           *zip.File
}

func (d DeltaReaderEntry) Read() (*image.Paletted, error) {
	r, err := d.F.Open()
	defer r.Close()
	if err != nil {
		return nil, err
	}
	im, err := png.Decode(r)
	if err != nil {
		return nil, err
	}
	return im.(*image.Paletted), nil
}

type DeltaReader struct {
	fzip, dzip, tzip *zip.ReadCloser
	Files            [4][]DeltaReaderEntry
	FileMap          [4]map[int]DeltaReaderEntry
	L                sync.Mutex

	c *SimpleCache[int, *image.Paletted]
}

func MakeDeltaReader(full, delta, ticks string) (*DeltaReader, error) {
	fzip, err := zip.OpenReader(full)
	if err != nil {
		return nil, err
	}
	var dzip, tzip *zip.ReadCloser
	if delta != "" {
		dzip, err = zip.OpenReader(delta)
		if err != nil {
			return nil, err
		}
	}
	if ticks != "" {
		tzip, err = zip.OpenReader(ticks)
		if err != nil {
			return nil, err
		}
	}

	d := &DeltaReader{fzip: fzip, dzip: dzip, tzip: tzip,
		c: NewSimpleCache[int, *image.Paletted](128),
	}
	for i := 0; i < 4; i++ {
		d.FileMap[i] = make(map[int]DeltaReaderEntry)
	}

	addFile := func(f *zip.File) error {
		comps := strings.Split(strings.TrimSuffix(f.Name, ".png"), "-")
		if len(comps) < 2 {
			return errors.New("unknown entry in zip file " + f.Name)
		}
		ts, err := strconv.Atoi(comps[0])
		if err != nil {
			return err
		}
		quad, err := strconv.Atoi(comps[1])
		if err != nil {
			return err
		}
		m := DeltaReaderEntry{
			Ts:   ts,
			Quad: quad,
			F:    f,
		}
		if len(comps) == 4 {
			m.Base, err = strconv.Atoi(comps[3])
			if err != nil {
				return err
			}
			m.Delta, err = strconv.Atoi(comps[2])
			if err != nil {
				return err
			}
		} else if len(comps) >= 3 {
			m.Base, err = strconv.Atoi(comps[2])
			if err != nil {
				return err
			}
		}
		d.Files[quad] = append(d.Files[quad], m)
		d.FileMap[quad][ts] = m
		return nil
	}

	for _, f := range fzip.File {
		err = addFile(f)
		if err != nil {
			return nil, err
		}
	}
	if tzip != nil {
		for _, f := range tzip.File {
			err = addFile(f)
			if err != nil {
				return nil, err
			}
		}
	}
	if dzip != nil {
		for _, f := range dzip.File {
			err = addFile(f)
			if err != nil {
				return nil, err
			}
		}
	}

	for n := 0; n < 4; n++ {
		sort.Slice(d.Files[n], func(i, j int) bool {
			return d.Files[n][i].Ts < d.Files[n][j].Ts
		})
	}

	return d, nil
}

func (d *DeltaReader) FindNearest(ts, quad int) *DeltaReaderEntry {
	fs := d.Files[quad]
	ind := sort.Search(len(fs), func(i int) bool {
		return fs[i].Ts > ts
	})
	if ind == len(fs) {
		ind--
	}
	if ind < 0 {
		return nil
	}
	if ind > 0 && ts-fs[ind-1].Ts < fs[ind].Ts-ts {
		ind--
	}
	return &fs[ind]
}

func (d *DeltaReader) FindNearestLeft(ts, quad int) *DeltaReaderEntry {
	fs := d.Files[quad]
	ind := sort.Search(len(fs), func(i int) bool {
		return fs[i].Ts > ts
	}) - 1
	if ind < 0 {
		return nil
	}
	return &fs[ind]
}

func (d *DeltaReader) GetImageRaw(e DeltaReaderEntry) (*image.Paletted, error) {
	// lock must be held
	k := e.Ts<<2 + e.Quad
	im, ok := d.c.Get(k)
	if ok {
		return im, nil
	}

	im, err := e.Read()
	if err != nil {
		return nil, err
	}

	d.c.Put(k, im)

	return im, nil
}

// GetImage gets an image with deltas applied.
func (d *DeltaReader) GetImage(e *DeltaReaderEntry) (*image.Paletted, error) {
	d.L.Lock()
	defer d.L.Unlock()

	im, err := e.Read()
	if err != nil {
		return nil, err
	}

	if e.Base != 0 {
		b, err := d.GetImageRaw(d.FileMap[e.Quad][e.Base])
		if err != nil {
			return nil, err
		}
		if e.Delta != 0 {
			d, err := d.GetImageRaw(d.FileMap[e.Quad][e.Delta])
			if err != nil {
				return nil, err
			}
			b = ApplyDelta(b, d)
		}
		im = ApplyDelta(b, im)
	}

	return im, nil
}

var imagePool sync.Pool

func FreeImage(i *image.Paletted) {
	imagePool.Put(i)
}

func ApplyDelta(base, delta *image.Paletted) *image.Paletted {
	if !base.Rect.Eq(delta.Rect) {
		panic("applying delta onto wrong-sized base")
	}
	combined := image.NewPaletted(base.Rect, base.Palette)
	copy(combined.Pix, base.Pix)
	for i := 0; i < len(delta.Pix); i++ {
		if ci := delta.Pix[i]; ci > 0 {
			combined.Pix[i] = ci - 1
		}
	}
	return combined
}
