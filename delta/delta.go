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

func ApplyDelta(base, delta *image.Paletted) *image.Paletted {
	if !base.Rect.Eq(delta.Rect) {
		panic("applying delta onto wrong-sized base")
	}
	combined := image.NewPaletted(base.Rect, base.Palette)
	copy(combined.Pix, base.Pix)
	for y := 0; y < delta.Rect.Max.Y; y++ {
		for x := 0; x < delta.Rect.Max.X; x++ {
			if ci := delta.ColorIndexAt(x, y); ci > 0 {
				combined.SetColorIndex(x, y, ci-1)
			}
		}
	}
	return combined
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

	d := &DeltaReader{fzip: fzip, dzip: dzip, tzip: tzip}
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
		if len(comps) >= 3 {
			m.Base, err = strconv.Atoi(comps[2])
			if err != nil {
				return err
			}
		}
		if len(comps) == 4 {
			m.Delta, err = strconv.Atoi(comps[3])
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

func (d *DeltaReader) GetImage(e *DeltaReaderEntry) (*image.Paletted, error) {
	d.L.Lock()
	defer d.L.Unlock()

	im, err := e.Read()
	if err != nil {
		return nil, err
	}

	if e.Base != 0 {
		b, err := d.FileMap[e.Quad][e.Base].Read()
		if err != nil {
			return nil, err
		}
		if e.Delta != 0 {
			d, err := d.FileMap[e.Quad][e.Delta].Read()
			if err != nil {
				return nil, err
			}
			b = ApplyDelta(d, b)
		}
		im = ApplyDelta(b, im)
	}

	return im, nil
}
