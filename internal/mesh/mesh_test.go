package mesh_test

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/margarit/printpipe/internal/mesh"
)

func TestASCIICube(t *testing.T) {
	m, err := mesh.Parse(mesh.Cube(20, 10, 5))
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Size(); s != (mesh.Vec{20, 10, 5}) {
		t.Fatalf("size %+v", s)
	}
	if v := m.Volume(); math.Abs(v-1000) > 1e-6 {
		t.Fatalf("volume %v", v)
	}
}

func TestBinarySTL(t *testing.T) {
	b := make([]byte, 84+50)
	binary.LittleEndian.PutUint32(b[80:], 1)
	put := func(off int, v float32) { binary.LittleEndian.PutUint32(b[off:], math.Float32bits(v)) }
	put(84+12, 0)
	put(84+16, 0)
	put(84+20, 0)
	put(84+24, 3)
	put(84+28, 0)
	put(84+32, 0)
	put(84+36, 0)
	put(84+40, 4)
	put(84+44, 2)
	m, err := mesh.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Size(); s != (mesh.Vec{3, 4, 2}) {
		t.Fatalf("size %+v", s)
	}
}

func TestEmpty(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("solid x\nendsolid x\n")} {
		if _, err := mesh.Parse(in); !errors.Is(err, mesh.ErrEmpty) {
			t.Errorf("%q: want ErrEmpty, got %v", in, err)
		}
	}
	if _, err := mesh.Parse([]byte("garbage that is not an stl")); err == nil {
		t.Error("garbage accepted")
	}
}
