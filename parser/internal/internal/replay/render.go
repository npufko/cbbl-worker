package replay

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os/exec"
	"sort"

	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const size = 720

// cbbl.net palette (navy + purple), CT/T kept conventional so they read instantly.
var (
	cBg      = color.RGBA{8, 12, 22, 255}
	cMapLo   = color.RGBA{26, 34, 56, 255}
	cMapHi   = color.RGBA{60, 72, 104, 255}
	cCT      = color.RGBA{91, 155, 213, 255}
	cT       = color.RGBA{217, 164, 65, 255}
	cAccent  = color.RGBA{168, 85, 247, 255}
	cText    = color.RGBA{241, 243, 248, 255}
	cMuted   = color.RGBA{138, 147, 168, 255}
	cDead    = color.RGBA{90, 98, 120, 255}
	cBar     = color.RGBA{15, 21, 36, 255}
)

type bounds struct{ minX, minY, scale, offX, offY float64 }

// Map extent from where players actually walked (1st–99th percentile), so no game assets are needed.
func computeBounds(pts [][2]float64) bounds {
	if len(pts) == 0 {
		return bounds{scale: 1}
	}
	xs, ys := make([]float64, len(pts)), make([]float64, len(pts))
	for i, p := range pts {
		xs[i], ys[i] = p[0], p[1]
	}
	sort.Float64s(xs)
	sort.Float64s(ys)
	q := func(v []float64, f float64) float64 { return v[int(f*float64(len(v)-1))] }
	minX, maxX, minY, maxY := q(xs, 0.01), q(xs, 0.99), q(ys, 0.01), q(ys, 0.99)
	pad := 64.0
	area := float64(size) - 2*pad
	scale := math.Min(area/(maxX-minX), (area-48)/(maxY-minY))
	return bounds{
		minX: minX, minY: minY, scale: scale,
		offX: pad + (area-(maxX-minX)*scale)/2,
		offY: pad + 48 + (area-48-(maxY-minY)*scale)/2,
	}
}

func (b bounds) px(x, y float64) (int, int) {
	// Game Y grows "up"; image Y grows down.
	return int(b.offX + (x-b.minX)*b.scale), size - int(b.offY+(y-b.minY)*b.scale) + 48
}

// Render writes an MP4 of the highlight. Requires ffmpeg on PATH (preinstalled on GitHub-hosted Ubuntu runners;
// otherwise `sudo apt-get install -y ffmpeg`).
func Render(rec *Recording, h Highlight, out string) error {
	r := h.roundRef
	if r == nil {
		return fmt.Errorf("highlight has no round data")
	}
	b := computeBounds(rec.Density)
	bg := background(rec, b)
	fps := int(math.Round(rec.TickRate / float64(tickStep(r))))
	if fps <= 0 {
		fps = 32
	}

	cmd := exec.Command("ffmpeg", "-loglevel", "error", "-y",
		"-f", "rawvideo", "-pix_fmt", "rgba", "-s", fmt.Sprintf("%dx%d", size, size), "-r", fmt.Sprint(fps), "-i", "-",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "veryfast", "-crf", "26", "-movflags", "+faststart", out)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	frame := image.NewRGBA(image.Rect(0, 0, size, size))
	lastPos := map[uint64][2]float64{}
	for _, f := range r.Frames {
		if f.Tick < h.FromTick || f.Tick > h.ToTick {
			for _, p := range f.Players {
				lastPos[p.ID] = [2]float64{p.X, p.Y}
			}
			continue
		}
		copy(frame.Pix, bg.Pix)
		drawKills(frame, b, rec, r, f.Tick, lastPos)
		for _, p := range f.Players {
			if p.Alive {
				lastPos[p.ID] = [2]float64{p.X, p.Y}
			}
		}
		drawPlayers(frame, b, f, h.SteamID, lastPos)
		drawHUD(frame, rec, r, h, f.Tick)
		if _, err := stdin.Write(frame.Pix); err != nil {
			return fmt.Errorf("write frame: %w", err)
		}
	}
	stdin.Close()
	return cmd.Wait()
}

func tickStep(r *Round) int {
	if len(r.Frames) < 2 {
		return 2
	}
	return r.Frames[1].Tick - r.Frames[0].Tick
}

func background(rec *Recording, b bounds) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill(img, img.Bounds(), cBg)
	heat := make([]int, size*size)
	for _, p := range rec.Density {
		x, y := b.px(p[0], p[1])
		for dx := -2; dx <= 2; dx++ {
			for dy := -2; dy <= 2; dy++ {
				if xx, yy := x+dx, y+dy; xx >= 0 && yy >= 0 && xx < size && yy < size {
					heat[yy*size+xx]++
				}
			}
		}
	}
	for i, v := range heat {
		if v == 0 {
			continue
		}
		t := math.Min(1, math.Log1p(float64(v))/math.Log1p(40))
		img.SetRGBA(i%size, i/size, lerp(cMapLo, cMapHi, t))
	}
	fill(img, image.Rect(0, 0, size, 48), cBar)
	return img
}

func drawPlayers(img *image.RGBA, b bounds, f Frame, focus uint64, lastPos map[uint64][2]float64) {
	// Dead first so the living draw on top.
	for _, p := range f.Players {
		if !p.Alive {
			if lp, ok := lastPos[p.ID]; ok {
				x, y := b.px(lp[0], lp[1])
				cross(img, x, y, 5, cDead)
			}
		}
	}
	for _, p := range f.Players {
		if !p.Alive {
			continue
		}
		x, y := b.px(p.X, p.Y)
		c := cT
		if p.CT {
			c = cCT
		}
		rad := 7
		if p.ID == focus {
			rad = 10
			ring(img, x, y, 15, cAccent)
		}
		yaw := p.Yaw * math.Pi / 180
		line(img, x, y, x+int(20*math.Cos(yaw)), y-int(20*math.Sin(yaw)), c, 2)
		disc(img, x, y, rad, c)
		if p.ID == focus {
			text(img, x+16, y-10, p.Name, cText, 2)
		}
	}
}

// Kill lines fade over one second.
func drawKills(img *image.RGBA, b bounds, rec *Recording, r *Round, tick int, lastPos map[uint64][2]float64) {
	for _, k := range r.Kills {
		age := float64(tick-k.Tick) / rec.TickRate
		if age < 0 || age > 1 || k.Killer == 0 {
			continue
		}
		kp, ok1 := lastPos[k.Killer]
		vp, ok2 := lastPos[k.Victim]
		if !ok1 || !ok2 {
			continue
		}
		x1, y1 := b.px(kp[0], kp[1])
		x2, y2 := b.px(vp[0], vp[1])
		line(img, x1, y1, x2, y2, lerp(cText, cBg, age), 2)
	}
}

func drawHUD(img *image.RGBA, rec *Recording, r *Round, h Highlight, tick int) {
	text(img, 16, 12, fmt.Sprintf("%s  |  %s  |  R%d  |  %s", h.Name, h.Label, r.N, rec.Map), cText, 2)
	text(img, size-110, 12, "cbbl.net", cAccent, 2)
	secs := float64(tick-r.StartTick) / rec.TickRate
	text(img, 16, size-28, fmt.Sprintf("%d:%02d", int(secs)/60, int(secs)%60), cMuted, 2)

	// Kill feed: last five kills so far, newest at the bottom.
	var feed []Kill
	for _, k := range r.Kills {
		if k.Tick <= tick {
			feed = append(feed, k)
		}
	}
	if len(feed) > 5 {
		feed = feed[len(feed)-5:]
	}
	for i, k := range feed {
		kc, vc := cT, cCT
		if k.KillerCT {
			kc, vc = cCT, cT
		}
		y := 64 + i*20
		name := k.KillerName
		if name == "" {
			name = "world"
		}
		hs := ""
		if k.HS {
			hs = " HS"
		}
		x := size - 16 - textWidth(name) - textWidth(k.Weapon+hs) - textWidth(k.VictimName) - 14
		x = text(img, x, y, name, kc, 1)
		x = text(img, x+7, y, k.Weapon+hs, cMuted, 1)
		text(img, x+7, y, k.VictimName, vc, 1)
	}
}

// ---------- tiny raster helpers (no external graphics deps) ----------

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func disc(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				img.SetRGBA(cx+x, cy+y, c)
			}
		}
	}
}

func ring(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for a := 0.0; a < 2*math.Pi; a += 0.02 {
		for w := 0; w < 2; w++ {
			img.SetRGBA(cx+int(float64(r-w)*math.Cos(a)), cy+int(float64(r-w)*math.Sin(a)), c)
		}
	}
}

func cross(img *image.RGBA, x, y, s int, c color.RGBA) {
	line(img, x-s, y-s, x+s, y+s, c, 2)
	line(img, x-s, y+s, x+s, y-s, c, 2)
}

func line(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, w int) {
	dx, dy := math.Abs(float64(x1-x0)), math.Abs(float64(y1-y0))
	steps := int(math.Max(dx, dy))
	if steps == 0 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := x0 + int(t*float64(x1-x0))
		y := y0 + int(t*float64(y1-y0))
		for ox := 0; ox < w; ox++ {
			for oy := 0; oy < w; oy++ {
				img.SetRGBA(x+ox, y+oy, c)
			}
		}
	}
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
}

// Go fonts (BSD-licensed, bundled in x/image) cover Latin, Cyrillic and Greek — common in player names.
// Glyphs they lack are replaced by "?" rather than drawn as boxes.
var (
	fontOnce         sync.Once
	faceUI, faceMono font.Face
)

func faces() {
	fontOnce.Do(func() {
		mk := func(ttf []byte, pt float64) font.Face {
			f, err := opentype.Parse(ttf)
			if err != nil {
				panic(err)
			}
			face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: pt, DPI: 72, Hinting: font.HintingFull})
			if err != nil {
				panic(err)
			}
			return face
		}
		faceUI = mk(gomedium.TTF, 22)
		faceMono = mk(gomono.TTF, 13)
	})
}

func printable(face font.Face, s string) string {
	out := []rune(s)
	for i, r := range out {
		if _, ok := face.GlyphAdvance(r); !ok {
			out[i] = '?'
		}
	}
	return string(out)
}

// text draws s with its top-left at (x, y); scale 1 = small mono (kill feed), 2 = UI face. Returns the end x.
func text(dst *image.RGBA, x, y int, s string, c color.RGBA, scale int) int {
	faces()
	face, top := faceMono, 11
	if scale > 1 {
		face, top = faceUI, 18
	}
	s = printable(face, s)
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y+top)}
	d.DrawString(s)
	return d.Dot.X.Round()
}

// textWidth measures s in the kill-feed face, for right-aligning.
func textWidth(s string) int {
	faces()
	return font.MeasureString(faceMono, printable(faceMono, s)).Round()
}
