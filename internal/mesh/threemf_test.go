package mesh_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/mesh"
)

func TestRead3MFAndSplit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.3mf")
	boxes := []mesh.Box{
		{Min: mesh.Vec{X: 0, Y: 0, Z: 0}, Max: mesh.Vec{X: 10, Y: 10, Z: 2}, Color: "#FF720C"},
		{Min: mesh.Vec{X: 2, Y: 2, Z: 3}, Max: mesh.Vec{X: 4, Y: 4, Z: 5}, Color: "#28282B"},
		{Min: mesh.Vec{X: 6, Y: 6, Z: 3}, Max: mesh.Vec{X: 8, Y: 8, Z: 5}, Color: "#28282B"},
	}
	if err := mesh.Write3MF(path, boxes); err != nil {
		t.Fatal(err)
	}
	m, err := mesh.Read3MF(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Faces) != 36 || m.Colors[0] != "#FF720C" || m.Colors[35] != "#28282B" {
		t.Fatalf("faces %d colors %v..%v", len(m.Faces), m.Colors[0], m.Colors[35])
	}
	pieces := m.Components()
	if len(pieces) != 3 {
		t.Fatalf("%d pieces", len(pieces))
	}
	if p := pieces[0]; p.Color != "#FF720C" || p.Size() != (mesh.Vec{X: 10, Y: 10, Z: 2}) || p.Volume < 199.9 || p.Volume > 200.1 {
		t.Fatalf("%+v", p)
	}
	if m.MajorityColor() != "#28282B" {
		t.Fatalf("majority %s", m.MajorityColor())
	}
	stl := filepath.Join(t.TempDir(), "p.stl")
	if err := m.WriteSTL(stl, pieces[1].Faces); err != nil {
		t.Fatal(err)
	}
	read, err := mesh.ReadFile(stl)
	if err != nil || len(read.Triangles) != 12 || read.Size() != (mesh.Vec{X: 2, Y: 2, Z: 2}) {
		t.Fatalf("%v %v", err, read)
	}
	all := filepath.Join(t.TempDir(), "all.stl")
	m.WriteSTL(all, nil)
	if st, _ := os.Stat(all); st.Size() != 84+36*50 {
		t.Fatalf("size %d", st.Size())
	}
}

func TestRead3MFErrors(t *testing.T) {
	if _, err := mesh.Read3MF(filepath.Join(t.TempDir(), "missing.3mf")); err == nil {
		t.Fatal("missing file read")
	}
	empty := filepath.Join(t.TempDir(), "e.3mf")
	mesh.Write3MF(empty, nil)
	if _, err := mesh.Read3MF(empty); err == nil {
		t.Fatal("empty model read")
	}
}
