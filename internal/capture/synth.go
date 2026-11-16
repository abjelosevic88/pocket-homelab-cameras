package capture

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand"
	"time"
)

// ---- demo cameras ----
//
// DEMO_DATA cameras draw their own pictures: a front door, a driveway, a
// back garden and a living room, lit for the time of day (grey "night
// vision" after dark), with people, a courier, cars and a cat passing by
// now and then — so the live view, movement checks and events work with
// no camera at all.

// Actor is something passing through a demo picture; P is how far along (0..1).
type Actor struct {
	Kind string // person | courier | car | cat | fox
	P    float64
}

// DemoScenes are the demo pictures.
var DemoScenes = []string{"door", "drive", "garden", "room"}

// demoCycle: one visitor every period, walking for `walk`.
func demoCycle(scene string) (period, walk time.Duration, offset time.Duration) {
	switch scene {
	case "door":
		return 6 * time.Minute, 12 * time.Second, 0
	case "drive":
		return 9 * time.Minute, 10 * time.Second, 2 * time.Minute
	case "garden":
		return 11 * time.Minute, 14 * time.Second, 4 * time.Minute
	}
	return 15 * time.Minute, 12 * time.Second, 7 * time.Minute
}

// DemoActorKind is who passes in a scene's n-th visit.
func DemoActorKind(scene string, n int64) string {
	pick := func(k ...string) string { return k[int(n%int64(len(k)))] }
	switch scene {
	case "door":
		return pick("courier", "person", "person", "courier", "cat")
	case "drive":
		return pick("car", "person", "car", "fox")
	case "garden":
		return pick("cat", "person", "fox", "cat")
	}
	return pick("person", "cat")
}

// DemoActors are who is in a scene at time t.
func DemoActors(scene string, t time.Time) []Actor {
	period, walk, off := demoCycle(scene)
	since := t.Add(-off).UnixNano() % int64(period)
	if since < 0 || time.Duration(since) > walk {
		return nil
	}
	n := t.Add(-off).UnixNano() / int64(period)
	return []Actor{{Kind: DemoActorKind(scene, n), P: float64(since) / float64(walk)}}
}

// DemoSource is a camera that draws its own pictures.
type DemoSource struct {
	Scene string
	FPS   float64
}

// Run implements Source.
func (s DemoSource) Run(ctx context.Context, emit func([]byte)) error {
	fps := s.FPS
	if fps <= 0 {
		fps = 3
	}
	tick := time.NewTicker(time.Duration(float64(time.Second) / fps))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t := <-tick.C:
			emit(EncodeJPEG(DemoFrame(s.Scene, t, DemoActors(s.Scene, t), 640, 360), 72))
		}
	}
}

// EncodeJPEG encodes a picture.
func EncodeJPEG(img image.Image, q int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: q})
	return b.Bytes()
}

type canvas struct {
	img  *image.RGBA
	w, h int
}

func (c canvas) set(x, y int, col color.RGBA) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	i := y*c.img.Stride + x*4
	if col.A == 255 {
		c.img.Pix[i], c.img.Pix[i+1], c.img.Pix[i+2], c.img.Pix[i+3] = col.R, col.G, col.B, 255
		return
	}
	a := float64(col.A) / 255
	c.img.Pix[i] = uint8(float64(c.img.Pix[i])*(1-a) + float64(col.R)*a)
	c.img.Pix[i+1] = uint8(float64(c.img.Pix[i+1])*(1-a) + float64(col.G)*a)
	c.img.Pix[i+2] = uint8(float64(c.img.Pix[i+2])*(1-a) + float64(col.B)*a)
}

func (c canvas) rect(x0, y0, x1, y1 float64, col color.RGBA) {
	for y := int(y0 * float64(c.h)); y < int(y1*float64(c.h)); y++ {
		for x := int(x0 * float64(c.w)); x < int(x1*float64(c.w)); x++ {
			c.set(x, y, col)
		}
	}
}

// vgrad fills a rectangle with a top-to-bottom gradient.
func (c canvas) vgrad(x0, y0, x1, y1 float64, a, b color.RGBA) {
	ya, yb := int(y0*float64(c.h)), int(y1*float64(c.h))
	for y := ya; y < yb; y++ {
		t := float64(y-ya) / math.Max(1, float64(yb-ya))
		col := mix(a, b, t)
		for x := int(x0 * float64(c.w)); x < int(x1*float64(c.w)); x++ {
			c.set(x, y, col)
		}
	}
}

func (c canvas) ellipse(cx, cy, rx, ry float64, col color.RGBA) {
	px, py, prx, pry := cx*float64(c.w), cy*float64(c.h), rx*float64(c.w), ry*float64(c.h)
	for y := int(py - pry); y <= int(py+pry); y++ {
		for x := int(px - prx); x <= int(px+prx); x++ {
			dx, dy := (float64(x)-px)/prx, (float64(y)-py)/pry
			if dx*dx+dy*dy <= 1 {
				c.set(x, y, col)
			}
		}
	}
}

func (c canvas) poly(pts [][2]float64, col color.RGBA) {
	minY, maxY := 1.0, 0.0
	for _, p := range pts {
		minY, maxY = math.Min(minY, p[1]), math.Max(maxY, p[1])
	}
	for y := int(minY * float64(c.h)); y <= int(maxY*float64(c.h)); y++ {
		fy := (float64(y) + 0.5) / float64(c.h)
		for x := 0; x < c.w; x++ {
			if InPolygon((float64(x)+0.5)/float64(c.w), fy, pts) {
				c.set(x, y, col)
			}
		}
	}
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

func mix(a, b color.RGBA, t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))
	return color.RGBA{uint8(float64(a.R)*(1-t) + float64(b.R)*t), uint8(float64(a.G)*(1-t) + float64(b.G)*t), uint8(float64(a.B)*(1-t) + float64(b.B)*t), 255}
}

// daylight is 0 at night … 1 at noon (local time).
func daylight(t time.Time) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	rise, set := 7.0, 16.8
	switch {
	case h < rise-1 || h > set+1:
		return 0
	case h < rise+1:
		return (h - (rise - 1)) / 2
	case h > set-1:
		return (set + 1 - h) / 2
	}
	return 1
}

// DemoFrame draws one demo picture.
func DemoFrame(scene string, t time.Time, actors []Actor, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := canvas{img, w, h}
	day := daylight(t)
	switch scene {
	case "door":
		c.vgrad(0, 0, 1, 0.18, rgb(150, 190, 230), rgb(205, 222, 238))
		c.rect(0, 0.18, 1, 0.72, rgb(222, 205, 178)) // wall
		for y := 0.22; y < 0.72; y += 0.06 {         // siding lines
			c.rect(0, y, 1, y+0.004, rgb(205, 187, 160))
		}
		c.rect(0.52, 0.26, 0.74, 0.72, rgb(120, 72, 40)) // door
		c.rect(0.545, 0.3, 0.715, 0.48, rgb(104, 62, 34))
		c.rect(0.545, 0.52, 0.715, 0.68, rgb(104, 62, 34))
		c.ellipse(0.7, 0.5, 0.008, 0.014, rgb(212, 175, 55))
		c.rect(0.12, 0.3, 0.34, 0.52, rgb(70, 92, 118)) // window
		c.rect(0.225, 0.3, 0.235, 0.52, rgb(240, 240, 240))
		c.rect(0.12, 0.405, 0.34, 0.415, rgb(240, 240, 240))
		c.rect(0.78, 0.36, 0.84, 0.46, rgb(60, 60, 64)) // mailbox
		c.vgrad(0, 0.72, 1, 1, rgb(150, 150, 146), rgb(120, 120, 116))
		c.rect(0.46, 0.72, 0.8, 0.77, rgb(170, 168, 160)) // step
		c.ellipse(0.06, 0.74, 0.1, 0.12, rgb(52, 110, 58))
		c.ellipse(0.94, 0.76, 0.09, 0.1, rgb(46, 102, 52))
		if day < 0.5 {
			c.ellipse(0.48, 0.3, 0.02, 0.03, color.RGBA{255, 230, 160, uint8(200 * (1 - day))}) // porch light
		}
	case "drive":
		c.vgrad(0, 0, 1, 0.3, rgb(140, 182, 228), rgb(210, 226, 240))
		c.ellipse(0.15, 0.3, 0.14, 0.16, rgb(60, 112, 64))
		c.ellipse(0.32, 0.27, 0.1, 0.13, rgb(70, 124, 70))
		c.rect(0.55, 0.12, 0.98, 0.34, rgb(200, 188, 170)) // neighbour's house
		c.poly([][2]float64{{0.52, 0.13}, {0.765, 0.02}, {1.01, 0.13}}, rgb(150, 70, 60))
		c.rect(0, 0.3, 1, 0.42, rgb(96, 96, 100)) // street
		c.rect(0.05, 0.355, 0.15, 0.365, rgb(230, 230, 230))
		c.rect(0.35, 0.355, 0.45, 0.365, rgb(230, 230, 230))
		c.rect(0.65, 0.355, 0.75, 0.365, rgb(230, 230, 230))
		c.vgrad(0, 0.42, 1, 1, rgb(92, 150, 72), rgb(70, 130, 56)) // lawn
		c.poly([][2]float64{{0.35, 0.42}, {0.62, 0.42}, {0.9, 1}, {0.12, 1}}, rgb(168, 160, 150))
		c.rect(0.0, 0.42, 0.33, 0.44, rgb(240, 240, 236)) // fence
		c.rect(0.64, 0.42, 1, 0.44, rgb(240, 240, 236))
		for x := 0.02; x < 0.33; x += 0.04 {
			c.rect(x, 0.39, x+0.012, 0.47, rgb(240, 240, 236))
		}
		for x := 0.66; x < 1; x += 0.04 {
			c.rect(x, 0.39, x+0.012, 0.47, rgb(240, 240, 236))
		}
		drawCar(c, 0.5, 0.78, 1.5, rgb(40, 70, 120)) // own car, parked
	case "garden":
		c.vgrad(0, 0, 1, 0.34, rgb(140, 186, 232), rgb(214, 228, 242))
		c.rect(0, 0.26, 1, 0.42, rgb(150, 112, 74)) // fence
		for x := 0.0; x < 1; x += 0.05 {
			c.rect(x, 0.26, x+0.004, 0.42, rgb(120, 88, 56))
		}
		c.ellipse(0.12, 0.2, 0.12, 0.2, rgb(56, 108, 60))
		c.ellipse(0.85, 0.16, 0.15, 0.2, rgb(64, 118, 64))
		c.rect(0.83, 0.2, 0.86, 0.42, rgb(96, 70, 48))
		c.vgrad(0, 0.42, 1, 1, rgb(98, 160, 76), rgb(74, 138, 58)) // lawn
		c.rect(0.6, 0.3, 0.78, 0.5, rgb(170, 140, 100))            // shed
		c.poly([][2]float64{{0.58, 0.31}, {0.69, 0.22}, {0.8, 0.31}}, rgb(110, 80, 60))
		c.rect(0.66, 0.38, 0.72, 0.5, rgb(120, 92, 64))
		c.ellipse(0.25, 0.7, 0.12, 0.04, rgb(140, 98, 70)) // flower bed
		for i := 0; i < 9; i++ {
			c.ellipse(0.15+float64(i)*0.025, 0.68, 0.008, 0.014, []color.RGBA{rgb(230, 80, 90), rgb(250, 210, 70), rgb(240, 240, 240)}[i%3])
		}
		c.rect(0.4, 0.62, 0.5, 0.64, rgb(220, 220, 220)) // bench
		c.rect(0.4, 0.58, 0.5, 0.6, rgb(220, 220, 220))
	default: // room
		c.rect(0, 0, 1, 0.66, rgb(236, 232, 224))
		c.vgrad(0.62, 0.08, 0.92, 0.5, rgb(150, 190, 228), rgb(196, 220, 240)) // window
		c.rect(0.765, 0.08, 0.775, 0.5, rgb(250, 250, 250))
		c.rect(0.6, 0.06, 0.94, 0.08, rgb(250, 250, 250))
		c.rect(0.6, 0.5, 0.94, 0.52, rgb(250, 250, 250))
		c.vgrad(0, 0.66, 1, 1, rgb(176, 140, 104), rgb(150, 116, 84)) // floor
		c.rect(0.06, 0.4, 0.5, 0.62, rgb(96, 110, 140))               // sofa
		c.rect(0.04, 0.48, 0.52, 0.72, rgb(84, 98, 128))
		c.rect(0.08, 0.36, 0.48, 0.44, rgb(104, 118, 150))
		c.rect(0.18, 0.74, 0.4, 0.78, rgb(120, 86, 60)) // table
		c.ellipse(0.88, 0.62, 0.05, 0.12, rgb(60, 120, 70))
		c.rect(0.86, 0.68, 0.9, 0.76, rgb(180, 110, 80))
		c.rect(0.2, 0.12, 0.38, 0.28, rgb(200, 150, 90)) // picture
		c.rect(0.215, 0.14, 0.365, 0.26, rgb(120, 170, 200))
	}
	for _, a := range actors {
		drawActor(c, scene, a)
	}
	// light: indoor stays lit at night (a lamp); outdoor turns to night vision
	if scene == "room" {
		if day < 0.6 {
			shade(c, 0.55+0.45*day, false)
		}
	} else if day < 0.35 {
		shade(c, 0, true)
	} else if day < 1 {
		shade(c, 0.45+0.55*day, false)
	}
	grain(c, t.UnixNano())
	stamp(c, t)
	return img
}

func drawCar(c canvas, cx, cy, s float64, col color.RGBA) {
	w, h := 0.16*s, 0.07*s
	c.rect(cx-w/2, cy-h, cx+w/2, cy, col)
	c.rect(cx-w*0.32, cy-h*1.7, cx+w*0.3, cy-h+0.002, col)
	c.rect(cx-w*0.27, cy-h*1.55, cx+w*0.25, cy-h*1.05, rgb(170, 200, 220))
	c.ellipse(cx-w*0.3, cy, 0.017*s, 0.03*s, rgb(30, 30, 30))
	c.ellipse(cx+w*0.3, cy, 0.017*s, 0.03*s, rgb(30, 30, 30))
}

func drawPerson(c canvas, x, foot, s float64, shirt color.RGBA, box bool) {
	hh := 0.36 * s
	c.rect(x-0.018*s, foot-hh*0.42, x-0.004*s, foot, rgb(40, 44, 60)) // legs
	c.rect(x+0.004*s, foot-hh*0.42, x+0.018*s, foot, rgb(40, 44, 60))
	c.rect(x-0.026*s, foot-hh*0.82, x+0.026*s, foot-hh*0.4, shirt)  // body
	c.ellipse(x, foot-hh*0.9, 0.018*s, 0.034*s, rgb(222, 184, 150)) // head
	c.ellipse(x, foot-hh*0.95, 0.018*s, 0.02*s, rgb(60, 44, 34))    // hair
	if box {
		c.rect(x+0.01*s, foot-hh*0.7, x+0.07*s, foot-hh*0.5, rgb(196, 150, 96))
		c.rect(x+0.01*s, foot-hh*0.61, x+0.07*s, foot-hh*0.595, rgb(150, 110, 70))
	}
}

func drawAnimal(c canvas, x, foot, s float64, col color.RGBA, tail bool) {
	c.ellipse(x, foot-0.03*s, 0.04*s, 0.025*s, col)
	c.ellipse(x+0.04*s, foot-0.05*s, 0.016*s, 0.022*s, col)
	c.poly([][2]float64{{x + 0.03*s, foot - 0.065*s}, {x + 0.036*s, foot - 0.09*s}, {x + 0.044*s, foot - 0.066*s}}, col)
	c.rect(x-0.03*s, foot-0.012*s, x-0.024*s, foot, col)
	c.rect(x+0.02*s, foot-0.012*s, x+0.026*s, foot, col)
	if tail {
		c.ellipse(x-0.055*s, foot-0.035*s, 0.022*s, 0.008*s, col)
	}
}

func drawActor(c canvas, scene string, a Actor) {
	p := a.P
	switch scene {
	case "door":
		switch a.Kind {
		case "courier":
			// walks to the door, leaves the box, walks away
			x := 0.05 + 0.55*math.Min(1, p*2)
			if p > 0.5 {
				x = 0.6 - 0.6*(p-0.5)*2
				c.rect(0.57, 0.69, 0.63, 0.74, rgb(196, 150, 96))
			}
			drawPerson(c, x, 0.92, 1.6, rgb(240, 130, 30), p <= 0.5)
		case "cat":
			drawAnimal(c, 0.1+0.8*p, 0.95, 1.4, rgb(40, 38, 36), true)
		default:
			x := 0.05 + 0.58*math.Min(1, p*1.6)
			drawPerson(c, x, 0.93, 1.6, rgb(70, 110, 160), false)
		}
	case "drive":
		switch a.Kind {
		case "car":
			drawCar(c, -0.15+1.3*p, 0.4, 0.9, rgb(160, 40, 40))
		case "fox":
			drawAnimal(c, 0.15+0.7*p, 0.62, 1.2, rgb(200, 100, 40), true)
		default:
			drawPerson(c, 0.45+0.1*p, 0.42+0.5*p, 0.8+0.9*p, rgb(60, 140, 90), false)
		}
	case "garden":
		switch a.Kind {
		case "cat":
			drawAnimal(c, 0.9-0.8*p, 0.86, 1.3, rgb(230, 140, 60), true)
		case "fox":
			drawAnimal(c, 0.1+0.8*p, 0.75, 1.3, rgb(200, 96, 36), true)
		default:
			drawPerson(c, 0.2+0.6*p, 0.9, 1.5, rgb(150, 80, 150), false)
		}
	default:
		if a.Kind == "cat" {
			drawAnimal(c, 0.2+0.6*p, 0.86, 1.3, rgb(40, 38, 36), true)
		} else {
			drawPerson(c, 0.6-0.3*p, 0.9, 1.7, rgb(190, 70, 70), false)
		}
	}
}

// shade darkens the picture (dusk), or turns it into night vision.
func shade(c canvas, k float64, ir bool) {
	p := c.img.Pix
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			i := y*c.img.Stride + x*4
			r, g, b := float64(p[i]), float64(p[i+1]), float64(p[i+2])
			if ir {
				dx, dy := (float64(x)/float64(c.w)-0.5)*1.4, (float64(y)/float64(c.h)-0.55)*1.6
				v := (0.33*r + 0.5*g + 0.17*b) * 0.85 * math.Max(0.25, 1-(dx*dx+dy*dy))
				p[i], p[i+1], p[i+2] = uint8(v), uint8(math.Min(255, v*1.02)), uint8(v)
				continue
			}
			p[i], p[i+1], p[i+2] = uint8(r*k), uint8(g*k), uint8(b*math.Min(1, k*1.08))
		}
	}
}

func grain(c canvas, seed int64) {
	rnd := rand.New(rand.NewSource(seed))
	p := c.img.Pix
	for n := 0; n < c.w*c.h/6; n++ {
		i := rnd.Intn(c.w*c.h) * 4
		d := rnd.Intn(9) - 4
		for k := 0; k < 3; k++ {
			v := int(p[i+k]) + d
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			p[i+k] = uint8(v)
		}
	}
}

// a 3×5 font for the time stamp in the corner
var digits = map[rune][5]string{
	'0': {"111", "101", "101", "101", "111"}, '1': {"010", "110", "010", "010", "111"}, '2': {"111", "001", "111", "100", "111"},
	'3': {"111", "001", "111", "001", "111"}, '4': {"101", "101", "111", "001", "001"}, '5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"}, '7': {"111", "001", "010", "010", "010"}, '8': {"111", "101", "111", "101", "111"},
	'9': {"111", "101", "111", "001", "111"}, '-': {"000", "000", "111", "000", "000"}, ':': {"000", "010", "000", "010", "000"}, ' ': {"000", "000", "000", "000", "000"},
}

func stamp(c canvas, t time.Time) {
	text := t.Format("2006-01-02 15:04:05")
	px := 3
	x0, y0 := 12, 12
	white, shadow := rgb(255, 255, 255), color.RGBA{0, 0, 0, 160}
	for i, r := range text {
		g := digits[r]
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if g[row][col] != '1' {
					continue
				}
				for dy := 0; dy < px; dy++ {
					for dx := 0; dx < px; dx++ {
						x, y := x0+i*4*px+col*px+dx, y0+row*px+dy
						c.set(x+1, y+1, shadow)
						c.set(x, y, white)
					}
				}
			}
		}
	}
}
