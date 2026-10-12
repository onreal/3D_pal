// Package assembly turns a model into ordered parts in their assembled positions, for the
// viewer's assembly timeline. A model that declares `assembly = [...]` gets one render per
// step; otherwise the assembled model is split into its separate pieces.
package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/margarit/printpipe/internal/mesh"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/scad"
)

// Modes say where the parts came from.
const (
	Steps  = "steps"  // the model's own assembly declaration, one render per step
	Pieces = "pieces" // the assembled model split into separate shells
)

type Part struct {
	Index     int      `json:"index"`
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Color     string   `json:"color,omitempty"`
	Count     int      `json:"count"` // identical instances in this step (four planets: 4)
	Min       mesh.Vec `json:"min"`
	Max       mesh.Vec `json:"max"`
	Volume    float64  `json:"volume_mm3"`
	Triangles int      `json:"triangles"`
	Error     string   `json:"error,omitempty"`
}

type Result struct {
	Project string    `json:"project"`
	Version int       `json:"version"`
	Status  string    `json:"status"` // running, done, failed
	Error   string    `json:"error,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	Note    string    `json:"note,omitempty"`
	Parts   []Part    `json:"parts"`
	Min     mesh.Vec  `json:"min"`
	Max     mesh.Vec  `json:"max"`
	Built   time.Time `json:"built_at"`
}

// Compiler is what the builder needs from OpenSCAD.
type Compiler interface {
	Export3MF(ctx context.Context, scadPath, out string, defines []string) error
}

type Service struct {
	scad     Compiler
	projects *project.Store
	publish  func(Result)
	parallel int

	mu       sync.Mutex
	inflight map[string]chan struct{}
}

func New(c Compiler, projects *project.Store, publish func(Result)) *Service {
	if publish == nil {
		publish = func(Result) {}
	}
	return &Service{scad: c, projects: projects, publish: publish, parallel: 3, inflight: map[string]chan struct{}{}}
}

const resultFile = "assembly.json"

func dir(v project.Version) string { return filepath.Join(v.Dir, "assembly") }

// PartFile is the STL of one part, in its assembled position.
func PartFile(v project.Version, index int) string {
	return filepath.Join(dir(v), fmt.Sprintf("part-%d.stl", index))
}

// Cached returns the built assembly of a version, if there is one.
func (s *Service) Cached(v project.Version) (Result, bool) {
	b, err := os.ReadFile(filepath.Join(dir(v), resultFile))
	if err != nil {
		return Result{}, false
	}
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return Result{}, false
	}
	return r, true
}

// Start returns the cached assembly, or starts building it in the background and returns
// a running result; the finished one is published.
func (s *Service) Start(v project.Version) Result {
	if r, ok := s.Cached(v); ok {
		return r
	}
	key := v.Project + "/" + strconv.Itoa(v.Number)
	s.mu.Lock()
	if _, running := s.inflight[key]; !running {
		done := make(chan struct{})
		s.inflight[key] = done
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			r := s.Build(ctx, v)
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
			close(done)
			s.publish(r)
		}()
	}
	s.mu.Unlock()
	return Result{Project: v.Project, Version: v.Number, Status: "running", Parts: []Part{}}
}

// Get returns the finished assembly, building it now if needed (waits up to ctx's deadline).
func (s *Service) Get(ctx context.Context, v project.Version) (Result, error) {
	if r := s.Start(v); r.Status != "running" {
		return r, nil
	}
	s.mu.Lock()
	done, ok := s.inflight[v.Project+"/"+strconv.Itoa(v.Number)]
	s.mu.Unlock()
	if ok {
		select {
		case <-done:
		case <-ctx.Done():
			return Result{}, fmt.Errorf("the assembly is still building: %w", ctx.Err())
		}
	}
	if r, ok := s.Cached(v); ok {
		return r, nil
	}
	return s.Build(ctx, v), nil // a failed build is not cached; report it
}

// Wait blocks until a running build of v finishes (tests).
func (s *Service) Wait(v project.Version) {
	s.mu.Lock()
	done, ok := s.inflight[v.Project+"/"+strconv.Itoa(v.Number)]
	s.mu.Unlock()
	if ok {
		<-done
	}
}

// Build renders the version's assembly and stores it beside the version.
func (s *Service) Build(ctx context.Context, v project.Version) Result {
	r := Result{Project: v.Project, Version: v.Number, Status: "done", Parts: []Part{}}
	fail := func(err error) Result {
		r.Status, r.Error = "failed", err.Error()
		return r
	}
	src, err := os.ReadFile(v.Path(project.SCAD))
	if err != nil {
		return fail(err)
	}
	params := scad.ParseParams(string(src))
	values := map[string]any{}
	for _, p := range params {
		if val, ok := v.Params[p.Name]; ok {
			values[p.Name] = val
		}
	}
	if opt, ok := scad.AssembledOption(params); ok {
		values["part"] = opt
	}
	defines, err := scad.Defines(params, values)
	if err != nil {
		return fail(err)
	}
	steps, err := scad.ParseAssembly(string(src))
	if err != nil {
		return fail(err)
	}
	out := dir(v)
	os.RemoveAll(out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fail(err)
	}
	if steps != nil {
		r.Mode = Steps
		r.Parts, err = s.buildSteps(ctx, v, steps, defines)
	} else {
		r.Mode = Pieces
		r.Note = "The model declares no assembly steps, so its separate pieces are shown bottom to top; parts that touch appear as one. Ask the agent to add assembly steps for a named, ordered timeline."
		r.Parts, err = s.buildPieces(ctx, v, defines)
	}
	if err != nil {
		return fail(err)
	}
	r.Min = mesh.Vec{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	r.Max = mesh.Vec{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, p := range r.Parts {
		if p.Error != "" {
			continue
		}
		r.Min = mesh.Vec{X: math.Min(r.Min.X, p.Min.X), Y: math.Min(r.Min.Y, p.Min.Y), Z: math.Min(r.Min.Z, p.Min.Z)}
		r.Max = mesh.Vec{X: math.Max(r.Max.X, p.Max.X), Y: math.Max(r.Max.Y, p.Max.Y), Z: math.Max(r.Max.Z, p.Max.Z)}
	}
	if math.IsInf(r.Min.X, 1) {
		return fail(errors.New("no part of the assembly rendered any geometry"))
	}
	r.Built = time.Now().UTC()
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(filepath.Join(out, resultFile), b, 0o644); err != nil {
		return fail(err)
	}
	return r
}

// buildSteps renders each declared step alone, in its assembled position, a few at a time.
func (s *Service) buildSteps(ctx context.Context, v project.Version, steps []scad.Step, defines []string) ([]Part, error) {
	parts := make([]Part, len(steps))
	sem := make(chan struct{}, s.parallel)
	var wg sync.WaitGroup
	for i, st := range steps {
		wg.Add(1)
		go func(i int, st scad.Step) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p := Part{Index: i, ID: st.ID, Label: st.Label, Count: 1}
			defs := append(append([]string{}, defines...), "-D", scad.AssemblyVar+"="+strconv.Quote(st.ID))
			tmp := filepath.Join(dir(v), fmt.Sprintf("step-%d.3mf", i))
			defer os.Remove(tmp)
			if err := s.scad.Export3MF(ctx, v.Path(project.SCAD), tmp, defs); err != nil {
				p.Error = err.Error()
				parts[i] = p
				return
			}
			m, err := mesh.Read3MF(tmp)
			if err != nil {
				p.Error = fmt.Sprintf("step %q renders nothing: draw its part when %s is %q", st.ID, scad.AssemblyVar, st.ID)
				parts[i] = p
				return
			}
			pieces := m.Components()
			p.Color = m.MajorityColor()
			p.Count = countInstances(pieces)
			p.Min, p.Max, p.Volume = bounds(pieces)
			p.Triangles = len(m.Faces)
			if err := m.WriteSTL(PartFile(v, i), nil); err != nil {
				p.Error = err.Error()
			}
			parts[i] = p
		}(i, st)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return parts, nil
}

// buildPieces renders the assembled model once and splits it into separate shells,
// grouping identical ones (same colour, volume and size) into one step.
func (s *Service) buildPieces(ctx context.Context, v project.Version, defines []string) ([]Part, error) {
	tmp := filepath.Join(dir(v), "assembled.3mf")
	defer os.Remove(tmp)
	if err := s.scad.Export3MF(ctx, v.Path(project.SCAD), tmp, defines); err != nil {
		return nil, err
	}
	m, err := mesh.Read3MF(tmp)
	if err != nil {
		return nil, err
	}
	groups := groupInstances(m.Components())
	sort.SliceStable(groups, func(a, b int) bool {
		za, zb := minZ(groups[a]), minZ(groups[b])
		if math.Abs(za-zb) > 0.01 {
			return za < zb
		}
		return volume(groups[a]) > volume(groups[b])
	})
	parts := make([]Part, len(groups))
	for i, g := range groups {
		var faces []int
		tris := 0
		for _, p := range g {
			faces = append(faces, p.Faces...)
			tris += len(p.Faces)
		}
		label := fmt.Sprintf("Part %d", i+1) // the viewer shows the count beside it
		p := Part{Index: i, ID: fmt.Sprintf("piece-%d", i+1), Label: label, Color: g[0].Color, Count: len(g), Triangles: tris}
		p.Min, p.Max, p.Volume = bounds(g)
		if err := m.WriteSTL(PartFile(v, i), faces); err != nil {
			return nil, err
		}
		parts[i] = p
	}
	return parts, nil
}

// similar reports whether two shells are copies of one part: same colour, volume within
// 1 % and the same sorted bounding-box dimensions within 0.2 mm (a rotated copy matches).
func similar(a, b mesh.Piece) bool {
	if a.Color != b.Color || a.Volume <= 0 || math.Abs(a.Volume-b.Volume)/a.Volume > 0.01 {
		return false
	}
	da, db := dims(a), dims(b)
	for i := range da {
		if math.Abs(da[i]-db[i]) > 0.2 {
			return false
		}
	}
	return true
}

func dims(p mesh.Piece) []float64 {
	s := p.Size()
	d := []float64{s.X, s.Y, s.Z}
	sort.Float64s(d)
	return d
}

func groupInstances(pieces []mesh.Piece) [][]mesh.Piece {
	var groups [][]mesh.Piece
	for _, p := range pieces {
		placed := false
		for gi := range groups {
			if similar(groups[gi][0], p) {
				groups[gi] = append(groups[gi], p)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []mesh.Piece{p})
		}
	}
	return groups
}

func countInstances(pieces []mesh.Piece) int {
	groups := groupInstances(pieces)
	if len(groups) == 1 {
		return len(groups[0])
	}
	return 1
}

func bounds(ps []mesh.Piece) (min, max mesh.Vec, vol float64) {
	min = mesh.Vec{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	max = mesh.Vec{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, p := range ps {
		min = mesh.Vec{X: math.Min(min.X, p.Min.X), Y: math.Min(min.Y, p.Min.Y), Z: math.Min(min.Z, p.Min.Z)}
		max = mesh.Vec{X: math.Max(max.X, p.Max.X), Y: math.Max(max.Y, p.Max.Y), Z: math.Max(max.Z, p.Max.Z)}
		vol += p.Volume
	}
	return min, max, vol
}

func minZ(g []mesh.Piece) float64 {
	z := math.Inf(1)
	for _, p := range g {
		z = math.Min(z, p.Min.Z)
	}
	return z
}

func volume(g []mesh.Piece) float64 {
	_, _, v := bounds(g)
	return v
}
