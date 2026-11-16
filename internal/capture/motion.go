package capture

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"strconv"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func secs(d time.Duration) string { return ftoa(d.Seconds()) }

// Zone is a part of the picture (points 0..1). Only movement inside "watch"
// zones counts (the whole picture when there are none); movement inside
// "ignore" zones never does.
type Zone struct {
	Kind   string
	Points [][2]float64
}

// Motion is what one picture showed.
type Motion struct {
	Moving   bool       // something moved (two pictures in a row)
	Score    float64    // the part of the watched picture that changed, 0..1
	Box      [4]float64 // where: x, y, w, h (0..1)
	Lighting bool       // the whole picture changed (lights, night mode): ignored
}

// Detector tells movement from a still scene. It shrinks each picture to a
// small grid of brightness, keeps a slowly learning background, and counts
// the grid cells that differ from it (with their neighbours, so rain and
// noise do not count).
type Detector struct {
	gw, gh  int
	bg      []float32
	mask    []bool
	cells   int
	zones   []Zone
	thresh  float32
	minArea float64
	streak  int
	skip    int
	ready   bool
}

// NewDetector makes a detector; sensitivity 1 (only big things) … 10 (everything).
func NewDetector(zones []Zone, sensitivity int) *Detector {
	if sensitivity < 1 {
		sensitivity = 1
	}
	if sensitivity > 10 {
		sensitivity = 10
	}
	s := float64(sensitivity)
	return &Detector{zones: zones, thresh: float32(40 - 2.5*s), minArea: 0.06 * math.Pow(0.6, s-1)}
}

// FeedJPEG decodes and checks one picture.
func (d *Detector) FeedJPEG(b []byte) (Motion, error) {
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return Motion{}, err
	}
	return d.Feed(img), nil
}

// Feed checks one picture.
func (d *Detector) Feed(img image.Image) Motion {
	r := img.Bounds()
	if r.Dx() < 16 || r.Dy() < 16 {
		return Motion{}
	}
	gw := 64
	gh := int(math.Round(float64(gw) * float64(r.Dy()) / float64(r.Dx())))
	if gh < 8 {
		gh = 8
	}
	if gw != d.gw || gh != d.gh {
		d.gw, d.gh, d.ready = gw, gh, false
		d.mask, d.cells = zoneMask(d.zones, gw, gh)
	}
	cur := grid(img, gw, gh)
	if !d.ready {
		d.bg, d.ready, d.streak = cur, true, 0
		return Motion{}
	}
	n := gw * gh
	changed := make([]bool, n)
	all := 0
	for i := 0; i < n; i++ {
		if abs32(cur[i]-d.bg[i]) > d.thresh {
			changed[i] = true
			all++
		}
	}
	// the whole picture changed: lights on, the camera's night mode, a cloud
	if float64(all) > 0.55*float64(n) {
		copy(d.bg, cur)
		d.streak, d.skip = 0, 2
		return Motion{Lighting: true}
	}
	if d.skip > 0 {
		d.skip--
		copy(d.bg, cur)
		return Motion{}
	}
	count := 0
	x0, y0, x1, y1 := gw, gh, -1, -1
	for y := 0; y < gh; y++ {
		for x := 0; x < gw; x++ {
			i := y*gw + x
			if !changed[i] || !d.mask[i] {
				continue
			}
			nb := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					xx, yy := x+dx, y+dy
					if (dx != 0 || dy != 0) && xx >= 0 && yy >= 0 && xx < gw && yy < gh && changed[yy*gw+xx] {
						nb++
					}
				}
			}
			if nb < 2 {
				continue
			}
			count++
			x0, y0 = min(x0, x), min(y0, y)
			x1, y1 = max(x1, x), max(y1, y)
		}
	}
	// learn the scene: fast where nothing moves, slowly where something does
	for i := 0; i < n; i++ {
		a := float32(0.08)
		if changed[i] {
			a = 0.02
		}
		d.bg[i] += a * (cur[i] - d.bg[i])
	}
	score := 0.0
	if d.cells > 0 {
		score = float64(count) / float64(d.cells)
	}
	m := Motion{Score: score}
	if count > 0 {
		m.Box = [4]float64{float64(x0) / float64(gw), float64(y0) / float64(gh), float64(x1-x0+1) / float64(gw), float64(y1-y0+1) / float64(gh)}
	}
	if score >= d.minArea {
		d.streak++
	} else {
		d.streak = 0
	}
	m.Moving = d.streak >= 2
	return m
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// grid is the picture's brightness averaged into gw × gh cells.
func grid(img image.Image, gw, gh int) []float32 {
	r := img.Bounds()
	w, h := r.Dx(), r.Dy()
	sum := make([]float32, gw*gh)
	cnt := make([]float32, gw*gh)
	step := 1
	if w > 800 {
		step = 2
	}
	if yc, ok := img.(*image.YCbCr); ok {
		for y := 0; y < h; y += step {
			gy := y * gh / h
			row := yc.Y[(y)*yc.YStride:]
			for x := 0; x < w; x += step {
				i := gy*gw + x*gw/w
				sum[i] += float32(row[x])
				cnt[i]++
			}
		}
	} else if g, ok := img.(*image.Gray); ok {
		for y := 0; y < h; y += step {
			gy := y * gh / h
			row := g.Pix[y*g.Stride:]
			for x := 0; x < w; x += step {
				i := gy*gw + x*gw/w
				sum[i] += float32(row[x])
				cnt[i]++
			}
		}
	} else {
		for y := 0; y < h; y += step {
			gy := y * gh / h
			for x := 0; x < w; x += step {
				cr, cg, cb, _ := img.At(r.Min.X+x, r.Min.Y+y).RGBA()
				i := gy*gw + x*gw/w
				sum[i] += float32(0.299*float64(cr>>8) + 0.587*float64(cg>>8) + 0.114*float64(cb>>8))
				cnt[i]++
			}
		}
	}
	for i := range sum {
		if cnt[i] > 0 {
			sum[i] /= cnt[i]
		}
	}
	return sum
}

// zoneMask marks the grid cells that count, and how many there are.
func zoneMask(zones []Zone, gw, gh int) ([]bool, int) {
	m := make([]bool, gw*gh)
	watch := false
	for _, z := range zones {
		if z.Kind != "ignore" && len(z.Points) >= 3 {
			watch = true
		}
	}
	n := 0
	for y := 0; y < gh; y++ {
		for x := 0; x < gw; x++ {
			px, py := (float64(x)+0.5)/float64(gw), (float64(y)+0.5)/float64(gh)
			on := !watch
			for _, z := range zones {
				if len(z.Points) < 3 || !InPolygon(px, py, z.Points) {
					continue
				}
				if z.Kind == "ignore" {
					on = false
					break
				}
				on = true
			}
			m[y*gw+x] = on
			if on {
				n++
			}
		}
	}
	return m, n
}

// InPolygon reports whether (x, y) is inside the polygon.
func InPolygon(x, y float64, pts [][2]float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		xi, yi, xj, yj := pts[i][0], pts[i][1], pts[j][0], pts[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}
