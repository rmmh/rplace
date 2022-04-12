// take reddit's bulky official events CSV and rewrite it in a friendlier format

package main

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// usage:
// pv 2022_place_canvas_history.csv.gzip | gzip -d | LC_ALL=C sort -s -k1,1 -t, -S 50% | gzip -9 > 2022_place_canvas_history_ordered.csv.gzip
// pv 2022_place_canvas_history_ordered.csv.gzip | go run raw_reproc.go | gzip -9 > 2022_place_canvas_history_cleaned.csv.gz

func main() {
	gr, err := gzip.NewReader(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}
	cr := csv.NewReader(gr)

	hashes := map[string]int{}

	fmt.Println("timestamp_millis,usernumber,color,x,y")

	last_time := time.Time{}

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if rec[0] == "timestamp" {
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		t, err := time.Parse("2006-01-02 15:04:05.999 UTC", rec[0])

		if t.Sub(last_time) < 0 {
			log.Fatal("input not in order!")
		}

		last_time = t

		uid, ok := hashes[rec[1]]
		if !ok {
			uid = len(hashes)
			hashes[rec[1]] = uid
		}

		if err != nil {
			log.Fatal("time parse failed", err)
		}
		rgb := rec[2]
		xy := strings.Split(rec[3], ",")
		x := xy[0]
		y := xy[1]
		if len(xy) == 4 {
			xmin, _ := strconv.Atoi(xy[0])
			ymin, _ := strconv.Atoi(xy[1])
			xmax, _ := strconv.Atoi(xy[2])
			ymax, _ := strconv.Atoi(xy[3])
			for y := ymin; y <= ymax; y++ {
				for x := xmin; x <= xmax; x++ {
					fmt.Printf("%d,%d,%s,%d,%d\n", t.UnixMilli(), uid, rgb, x, y)
				}
			}
		} else {
			fmt.Printf("%d,%d,%s,%s,%s\n", t.UnixMilli(), uid, rgb, x, y)
		}
	}
}
