// Package mesh reads STL files: enough to size a part, check it fits, and estimate volume.
package mesh

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

type Vec struct{ X, Y, Z float64 }

type Mesh struct {
	Triangles [][3]Vec
}

var ErrEmpty = errors.New("the STL has no triangles: the model is empty")

func ReadFile(path string) (*Mesh, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse reads binary or ASCII STL.
func Parse(b []byte) (*Mesh, error) {
	if len(b) >= 84 {
		n := binary.LittleEndian.Uint32(b[80:84])
		if uint64(len(b)) == 84+uint64(n)*50 {
			return parseBinary(b, int(n))
		}
	}
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("solid")) {
		return parseASCII(b)
	}
	if len(b) == 0 {
		return nil, ErrEmpty
	}
	return nil, errors.New("not an STL file")
}

func parseBinary(b []byte, n int) (*Mesh, error) {
	m := &Mesh{Triangles: make([][3]Vec, 0, n)}
	f := func(off int) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))) }
	for i := 0; i < n; i++ {
		base := 84 + i*50 + 12 // skip the normal
		var t [3]Vec
		for v := 0; v < 3; v++ {
			o := base + v*12
			t[v] = Vec{f(o), f(o + 4), f(o + 8)}
		}
		m.Triangles = append(m.Triangles, t)
	}
	if len(m.Triangles) == 0 {
		return nil, ErrEmpty
	}
	return m, nil
}

func parseASCII(b []byte) (*Mesh, error) {
	m := &Mesh{}
	var cur []Vec
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 4 && fields[0] == "vertex" {
			var v [3]float64
			for i := range v {
				x, err := strconv.ParseFloat(fields[i+1], 64)
				if err != nil {
					return nil, fmt.Errorf("bad vertex %q", sc.Text())
				}
				v[i] = x
			}
			cur = append(cur, Vec{v[0], v[1], v[2]})
			if len(cur) == 3 {
				m.Triangles = append(m.Triangles, [3]Vec{cur[0], cur[1], cur[2]})
				cur = cur[:0]
			}
		}
	}
	if len(m.Triangles) == 0 {
		return nil, ErrEmpty
	}
	return m, sc.Err()
}

// Bounds returns the axis-aligned bounding box.
func (m *Mesh) Bounds() (min, max Vec) {
	min = Vec{math.Inf(1), math.Inf(1), math.Inf(1)}
	max = Vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, t := range m.Triangles {
		for _, v := range t {
			min = Vec{math.Min(min.X, v.X), math.Min(min.Y, v.Y), math.Min(min.Z, v.Z)}
			max = Vec{math.Max(max.X, v.X), math.Max(max.Y, v.Y), math.Max(max.Z, v.Z)}
		}
	}
	return min, max
}

// Size is the bounding box size in mm.
func (m *Mesh) Size() Vec {
	lo, hi := m.Bounds()
	return Vec{hi.X - lo.X, hi.Y - lo.Y, hi.Z - lo.Z}
}

// Volume is the enclosed volume in mm³ (signed tetrahedra; assumes a closed mesh).
func (m *Mesh) Volume() float64 {
	var v float64
	for _, t := range m.Triangles {
		a, b, c := t[0], t[1], t[2]
		v += a.X*(b.Y*c.Z-b.Z*c.Y) - a.Y*(b.X*c.Z-b.Z*c.X) + a.Z*(b.X*c.Y-b.Y*c.X)
	}
	return math.Abs(v) / 6
}

// Cube returns an ASCII STL of an axis-aligned box from the origin; tests and the fake slicer use it.
func Cube(x, y, z float64) []byte {
	p := [8]Vec{{0, 0, 0}, {x, 0, 0}, {x, y, 0}, {0, y, 0}, {0, 0, z}, {x, 0, z}, {x, y, z}, {0, y, z}}
	faces := [][3]int{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}}
	var b strings.Builder
	b.WriteString("solid cube\n")
	for _, f := range faces {
		b.WriteString(" facet normal 0 0 0\n  outer loop\n")
		for _, i := range f {
			fmt.Fprintf(&b, "   vertex %g %g %g\n", p[i].X, p[i].Y, p[i].Z)
		}
		b.WriteString("  endloop\n endfacet\n")
	}
	b.WriteString("endsolid cube\n")
	return []byte(b.String())
}
