package bambustudio

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/margarit/printpipe/internal/slicer"
)

// sliceInfo is Metadata/slice_info.config in a .gcode.3mf. Bambu doesn't document the
// format; the integration test checks these names against a real sliced file.
type sliceInfo struct {
	Plates []struct {
		Metadata []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:"value,attr"`
		} `xml:"metadata"`
		Filaments []struct {
			Type  string `xml:"type,attr"`
			UsedM string `xml:"used_m,attr"`
			UsedG string `xml:"used_g,attr"`
		} `xml:"filament"`
	} `xml:"plate"`
}

// ReadResult reads time, filament, layers and supports from a sliced .gcode.3mf.
func ReadResult(path string) (slicer.Result, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return slicer.Result{}, fmt.Errorf("read sliced file: %w", err)
	}
	defer zr.Close()
	res := slicer.Result{Path: path}

	raw, err := readZip(&zr.Reader, "Metadata/slice_info.config")
	if err != nil {
		return res, err
	}
	var info sliceInfo
	if err := xml.Unmarshal(raw, &info); err != nil {
		return res, fmt.Errorf("slice_info.config: %w", err)
	}
	if len(info.Plates) == 0 {
		return res, fmt.Errorf("slice_info.config has no plate")
	}
	plate := info.Plates[0]
	var weight float64
	for _, m := range plate.Metadata {
		switch m.Key {
		case "prediction":
			res.Seconds, _ = strconv.Atoi(m.Value)
		case "weight":
			weight, _ = strconv.ParseFloat(m.Value, 64)
		case "support_used":
			res.Supports = m.Value == "true"
		case "outside":
			res.OffPlate = m.Value == "true"
		}
	}
	for _, f := range plate.Filaments {
		g, _ := strconv.ParseFloat(f.UsedG, 64)
		m, _ := strconv.ParseFloat(f.UsedM, 64)
		res.Filaments = append(res.Filaments, slicer.Filament{Type: f.Type, Grams: g, Metres: m})
	}
	if len(res.Filaments) == 0 && weight > 0 {
		res.Filaments = []slicer.Filament{{Grams: weight}}
	}

	if g, err := zr.Open("Metadata/plate_1.gcode"); err == nil {
		res.Layers = headerLayers(g)
		g.Close()
	}
	if raw, err := readZip(&zr.Reader, "Metadata/project_settings.config"); err == nil {
		var ps map[string]any
		if json.Unmarshal(raw, &ps) == nil {
			res.LayerHeight = number(ps["layer_height"])
		}
	}
	return res, nil
}

func readZip(zr *zip.Reader, name string) ([]byte, error) {
	f, err := zr.Open(name)
	if err != nil {
		return nil, fmt.Errorf("sliced file has no %s", name)
	}
	defer f.Close()
	return io.ReadAll(f)
}

// headerLayers reads "; total layer number: N" from the G-code header block.
func headerLayers(r io.Reader) int {
	sc := bufio.NewScanner(r)
	for i := 0; sc.Scan() && i < 200; i++ {
		line := sc.Text()
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(strings.TrimPrefix(k, ";")) == "total layer number" {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
		if strings.HasPrefix(line, "; HEADER_BLOCK_END") {
			break
		}
	}
	return 0
}

// number reads Bambu's settings values, which are strings or lists of strings.
func number(v any) float64 {
	switch x := v.(type) {
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSuffix(x, "%"), 64)
		return f
	case []any:
		if len(x) > 0 {
			return number(x[0])
		}
	case float64:
		return x
	}
	return 0
}
