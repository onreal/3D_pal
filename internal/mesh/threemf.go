package mesh

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

// Colored is an indexed mesh with one colour per triangle, as OpenSCAD exports to 3MF.
type Colored struct {
	Vertices []Vec
	Faces    [][3]int
	Colors   []string // "#RRGGBB" per face; "" when none
}

type model3mf struct {
	Resources struct {
		Base []struct {
			ID    string `xml:"id,attr"`
			Bases []struct {
				Color string `xml:"displaycolor,attr"`
			} `xml:"base"`
		} `xml:"basematerials"`
		Objects []struct {
			PID    string `xml:"pid,attr"`
			PIndex string `xml:"pindex,attr"`
			Mesh   struct {
				Vertices []struct {
					X float64 `xml:"x,attr"`
					Y float64 `xml:"y,attr"`
					Z float64 `xml:"z,attr"`
				} `xml:"vertices>vertex"`
				Triangles []struct {
					V1  int    `xml:"v1,attr"`
					V2  int    `xml:"v2,attr"`
					V3  int    `xml:"v3,attr"`
					PID string `xml:"pid,attr"`
					P1  string `xml:"p1,attr"`
				} `xml:"triangles>triangle"`
			} `xml:"mesh"`
		} `xml:"object"`
	} `xml:"resources"`
}

// Read3MF reads every mesh object in a 3MF file into one coloured mesh.
func Read3MF(path string) (*Colored, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var raw []byte
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "3D/3dmodel.model") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			raw, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	if raw == nil {
		return nil, errors.New("3MF has no 3D/3dmodel.model")
	}
	var m model3mf
	if err := xml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("3MF model: %w", err)
	}
	palettes := map[string][]string{}
	for _, b := range m.Resources.Base {
		for _, base := range b.Bases {
			c := base.Color
			if len(c) == 9 { // #RRGGBBAA
				c = c[:7]
			}
			palettes[b.ID] = append(palettes[b.ID], strings.ToUpper(c))
		}
	}
	color := func(pid, idx string) string {
		var i int
		if _, err := fmt.Sscan(idx, &i); err != nil {
			return ""
		}
		if p := palettes[pid]; i >= 0 && i < len(p) {
			return p[i]
		}
		return ""
	}
	out := &Colored{}
	for _, o := range m.Resources.Objects {
		base := len(out.Vertices)
		for _, v := range o.Mesh.Vertices {
			out.Vertices = append(out.Vertices, Vec{v.X, v.Y, v.Z})
		}
		def := color(o.PID, o.PIndex)
		for _, t := range o.Mesh.Triangles {
			out.Faces = append(out.Faces, [3]int{base + t.V1, base + t.V2, base + t.V3})
			c := def
			if t.P1 != "" {
				pid := t.PID
				if pid == "" {
					pid = o.PID
				}
				c = color(pid, t.P1)
			}
			out.Colors = append(out.Colors, c)
		}
	}
	if len(out.Faces) == 0 {
		return nil, ErrEmpty
	}
	return out, nil
}

// Piece is one connected shell of a coloured mesh.
type Piece struct {
	Faces  []int // indices into Colored.Faces
	Color  string
	Min    Vec
	Max    Vec
	Volume float64
}

func (p Piece) Size() Vec { return Vec{p.Max.X - p.Min.X, p.Max.Y - p.Min.Y, p.Max.Z - p.Min.Z} }

// Components splits the mesh into connected shells (faces sharing a vertex), each with
// its majority colour, bounds and volume.
func (c *Colored) Components() []Piece {
	parent := make([]int, len(c.Vertices))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(a int) int {
		for parent[a] != a {
			parent[a] = parent[parent[a]]
			a = parent[a]
		}
		return a
	}
	for _, f := range c.Faces {
		for _, pair := range [][2]int{{f[0], f[1]}, {f[1], f[2]}} {
			a, b := find(pair[0]), find(pair[1])
			if a != b {
				parent[a] = b
			}
		}
	}
	byRoot := map[int]*Piece{}
	var order []int
	votes := map[int]map[string]int{}
	for i, f := range c.Faces {
		r := find(f[0])
		p, ok := byRoot[r]
		if !ok {
			p = &Piece{Min: Vec{math.Inf(1), math.Inf(1), math.Inf(1)}, Max: Vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
			byRoot[r] = p
			votes[r] = map[string]int{}
			order = append(order, r)
		}
		p.Faces = append(p.Faces, i)
		votes[r][c.Colors[i]]++
		a, b, d := c.Vertices[f[0]], c.Vertices[f[1]], c.Vertices[f[2]]
		for _, v := range []Vec{a, b, d} {
			p.Min = Vec{math.Min(p.Min.X, v.X), math.Min(p.Min.Y, v.Y), math.Min(p.Min.Z, v.Z)}
			p.Max = Vec{math.Max(p.Max.X, v.X), math.Max(p.Max.Y, v.Y), math.Max(p.Max.Z, v.Z)}
		}
		p.Volume += (a.X*(b.Y*d.Z-b.Z*d.Y) - a.Y*(b.X*d.Z-b.Z*d.X) + a.Z*(b.X*d.Y-b.Y*d.X)) / 6
	}
	out := make([]Piece, 0, len(order))
	for _, r := range order {
		p := byRoot[r]
		p.Volume = math.Abs(p.Volume)
		best, n := "", -1
		keys := make([]string, 0, len(votes[r]))
		for k := range votes[r] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if votes[r][k] > n {
				best, n = k, votes[r][k]
			}
		}
		p.Color = best
		out = append(out, *p)
	}
	return out
}

// MajorityColor is the colour most faces carry.
func (c *Colored) MajorityColor() string {
	count := map[string]int{}
	best, n := "", -1
	for _, col := range c.Colors {
		count[col]++
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if count[k] > n {
			best, n = k, count[k]
		}
	}
	return best
}

// WriteSTL writes the given faces (all when nil) as binary STL.
func (c *Colored) WriteSTL(path string, faces []int) error {
	if faces == nil {
		faces = make([]int, len(c.Faces))
		for i := range faces {
			faces[i] = i
		}
	}
	var buf bytes.Buffer
	buf.Write(make([]byte, 80))
	binary.Write(&buf, binary.LittleEndian, uint32(len(faces)))
	for _, fi := range faces {
		f := c.Faces[fi]
		a, b, d := c.Vertices[f[0]], c.Vertices[f[1]], c.Vertices[f[2]]
		ux, uy, uz := b.X-a.X, b.Y-a.Y, b.Z-a.Z
		vx, vy, vz := d.X-a.X, d.Y-a.Y, d.Z-a.Z
		nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
		if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 0 {
			nx, ny, nz = nx/l, ny/l, nz/l
		}
		for _, x := range []float64{nx, ny, nz, a.X, a.Y, a.Z, b.X, b.Y, b.Z, d.X, d.Y, d.Z} {
			binary.Write(&buf, binary.LittleEndian, float32(x))
		}
		buf.Write([]byte{0, 0})
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Write3MF writes boxes as one coloured 3MF object (tests and the fake OpenSCAD use it).
func Write3MF(path string, boxes []Box) error {
	var verts, tris strings.Builder
	var mats strings.Builder
	colorIdx := map[string]int{}
	n := 0
	for _, b := range boxes {
		if _, ok := colorIdx[b.Color]; !ok {
			colorIdx[b.Color] = len(colorIdx)
			fmt.Fprintf(&mats, `<base name="c%d" displaycolor="%sFF"/>`, len(colorIdx), b.Color)
		}
		x0, y0, z0 := b.Min.X, b.Min.Y, b.Min.Z
		x1, y1, z1 := b.Max.X, b.Max.Y, b.Max.Z
		p := [8]Vec{{x0, y0, z0}, {x1, y0, z0}, {x1, y1, z0}, {x0, y1, z0}, {x0, y0, z1}, {x1, y0, z1}, {x1, y1, z1}, {x0, y1, z1}}
		for _, v := range p {
			fmt.Fprintf(&verts, `<vertex x="%g" y="%g" z="%g"/>`, v.X, v.Y, v.Z)
		}
		for _, f := range [][3]int{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}} {
			fmt.Fprintf(&tris, `<triangle v1="%d" v2="%d" v3="%d" pid="1" p1="%d"/>`, n+f[0], n+f[1], n+f[2], colorIdx[b.Color])
		}
		n += 8
	}
	model := `<?xml version="1.0" encoding="UTF-8"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources>` +
		`<basematerials id="1">` + mats.String() + `</basematerials><object id="2" type="model" pid="1" pindex="0"><mesh><vertices>` +
		verts.String() + `</vertices><triangles>` + tris.String() + `</triangles></mesh></object></resources><build><item objectid="2"/></build></model>`
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, _ := zw.Create("3D/3dmodel.model")
	w.Write([]byte(model))
	return zw.Close()
}

// Box is an axis-aligned box with a colour, for Write3MF.
type Box struct {
	Min, Max Vec
	Color    string // "#RRGGBB"
}
