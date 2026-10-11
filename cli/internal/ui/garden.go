package ui

import (
	"math"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The garden fills the space under the boxes. It is pure decoration: it plants, blooms and
// wilts on its own, unrelated to the sessions.

const (
	gardenFrame = 350 * time.Millisecond
	gardenBeds  = 10 // plants belong to beds; a bed nobody tends for a while starts to wilt
)

var (
	gGrass  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3e623f"))
	gGrass2 = lipgloss.NewStyle().Foreground(lipgloss.Color("#5d8f5e"))
	gPath   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6f6151"))
	gWater  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4f8fad"))
	gWater2 = lipgloss.NewStyle().Foreground(lipgloss.Color("#376479"))
	gTree   = lipgloss.NewStyle().Foreground(lipgloss.Color("#3b834e"))
	gSprout = lipgloss.NewStyle().Foreground(lipgloss.Color("#6da361"))
	gBush   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6dba70"))
	gWeed   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8d6760"))
	gFlower = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("#e5c07b")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#df8db5")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a89cf0")),
	}
	gMe = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
)

type gardenTickMsg struct{}

func gardenTick() tea.Cmd {
	return tea.Tick(gardenFrame, func(time.Time) tea.Msg { return gardenTickMsg{} })
}

type plant struct {
	x, y, born, bed int
	bloom           int // frame it bloomed, -1 while growing
}

type garden struct {
	t, cols, rows  int
	plants         []*plant
	tended         [gardenBeds]int // last frame each bed got attention
	gx, gy, tx, ty int             // the gardener and where it walks
	rnd            *rand.Rand
	ticking        bool // a frame is scheduled
}

func newGarden() *garden {
	g := &garden{rnd: rand.New(rand.NewSource(time.Now().UnixNano())), gx: 3, gy: 2, tx: 3, ty: 2}
	for i := range g.tended {
		g.tended[i] = -g.rnd.Intn(300)
	}
	return g
}

// resize starts the garden on first draw; plants that no longer fit stay hidden until it grows back.
func (g *garden) resize(cols, rows int) {
	first := g.cols == 0
	g.cols, g.rows = cols, rows
	if !first {
		return
	}
	for i := range cols * rows / 10 {
		bloom := g.rnd.Float64() < 0.4
		if p := g.add(i%gardenBeds, -g.rnd.Intn(40)); p != nil && bloom {
			p.bloom = p.born + 20
		}
	}
}

func (g *garden) terrain(x, y int) byte {
	R, cols := float64(g.rows), float64(g.cols)
	if y == int(math.Round((R-1)/2+math.Sin(float64(x)*0.07+1)*(R/2-0.6))) {
		return 'p'
	}
	ex, ey := (float64(x)-cols*0.72)/7, (float64(y)-(R-1)*0.5)/max(1.2, R*0.28)
	if ex*ex+ey*ey < 1 {
		return 'w'
	}
	if px := int(math.Round(cols * 0.33)); x == px || x == px+1 {
		return 'p'
	}
	if x > 0 && (uint32(x)*73856093^uint32(y)*19349663)%41 == 0 { // x = 0 always hashes to a tree
		return 't'
	}
	return ' '
}

func (g *garden) add(bed, born int) *plant {
	for range 40 {
		x, y := 1+g.rnd.Intn(max(1, g.cols-2)), g.rnd.Intn(g.rows)
		if g.terrain(x, y) == ' ' && g.at(x, y) == nil {
			p := &plant{x: x, y: y, born: born, bed: bed, bloom: -1}
			g.plants = append(g.plants, p)
			return p
		}
	}
	return nil
}

func (g *garden) at(x, y int) *plant {
	for _, p := range g.plants {
		if p.x == x && p.y == y {
			return p
		}
	}
	return nil
}

func (g *garden) step() {
	if g.cols == 0 {
		return
	}
	g.t++
	t := g.t
	if t%9 == 0 { // something happens: a seed goes in, or something blooms
		bed := g.rnd.Intn(gardenBeds)
		var p *plant
		if g.rnd.Float64() < 0.55 {
			p = g.add(bed, t)
		} else {
			for _, q := range g.plants { // a grown plant of this bed, else anything still growing
				if q.bloom >= 0 {
					continue
				}
				if q.bed == bed && t-q.born > 12 {
					p = q
					break
				}
				if p == nil {
					p = q
				}
			}
			if p != nil {
				p.bloom = t
			}
		}
		g.tended[bed] = t
		if limit := g.cols * g.rows * 22 / 100; len(g.plants) > limit {
			g.plants = g.plants[len(g.plants)-limit:]
		}
		if p != nil {
			g.tx, g.ty = p.x+1, p.y
		}
	}
	if g.gx != g.tx {
		g.gx += sign(g.tx - g.gx)
	} else if g.gy != g.ty {
		g.gy += sign(g.ty - g.gy)
	}
	if g.gx != g.tx && g.gy != g.ty && t%3 == 0 {
		g.gy += sign(g.ty - g.gy)
	}
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	return 1
}

type cell struct {
	ch string
	st *lipgloss.Style
}

func (g *garden) lines() []string {
	t := g.t
	grid := make([][]cell, g.rows)
	for y := range grid {
		grid[y] = make([]cell, g.cols)
		for x := range grid[y] {
			grid[y][x] = g.ground(x, y)
		}
	}
	for i, p := range g.plants {
		if p.x >= g.cols || p.y >= g.rows {
			continue
		}
		c := &grid[p.y][p.x]
		switch age := t - p.born; {
		case p.bloom >= 0 && t >= p.bloom:
			c.ch, c.st = []string{"✿", "*", "❀"}[i%3], &gFlower[i%3]
			if t-p.bloom < 4 && t%2 == 1 {
				c.ch = "+"
			}
		case t-g.tended[p.bed] > 160 && i%2 == 0:
			*c = cell{"%", &gWeed}
		case age < 4:
			*c = cell{".", &gSprout}
		case age < 10:
			*c = cell{",", &gSprout}
		case age < 18:
			*c = cell{"v", &gBush}
		default:
			*c = cell{"♣", &gBush}
		}
	}
	if g.gx < g.cols && g.gy < g.rows {
		grid[g.gy][g.gx] = cell{"@", &gMe}
	}
	out := make([]string, g.rows)
	for y, row := range grid { // one escape sequence per run of the same colour
		var b, run strings.Builder
		var cur *lipgloss.Style
		for _, c := range row {
			if c.st != cur && run.Len() > 0 {
				b.WriteString(cur.Render(run.String()))
				run.Reset()
			}
			cur = c.st
			run.WriteString(c.ch)
		}
		b.WriteString(cur.Render(run.String()))
		out[y] = b.String()
	}
	return out
}

func (g *garden) ground(x, y int) cell {
	switch g.terrain(x, y) {
	case 'p':
		return cell{"·", &gPath}
	case 'w':
		if math.Sin(float64(x)*0.6+float64(y)-float64(g.t)*0.3) > 0.4 {
			return cell{"≈", &gWater}
		}
		return cell{"~", &gWater2}
	case 't':
		return cell{"♠", &gTree}
	}
	wave := math.Sin(float64(x)*0.35 - float64(g.t)*0.45 + float64(y)*0.8)
	h := uint32(x)*2654435761 ^ uint32(y)*40503
	switch {
	case h%6 == 0 && wave > 0.3:
		return cell{`"`, &gGrass2}
	case h%6 == 0:
		return cell{"'", &gGrass}
	case h%4 == 0 && wave > 0.6:
		return cell{",", &gGrass}
	case h%4 == 0:
		return cell{".", &gGrass}
	}
	return cell{" ", &gGrass}
}

func (g *garden) counts() (bloom, grow int) {
	for _, p := range g.plants {
		if p.bloom >= 0 && g.t >= p.bloom {
			bloom++
		} else {
			grow++
		}
	}
	return
}
