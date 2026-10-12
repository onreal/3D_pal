package slicer_test

import (
	"context"
	"testing"

	"github.com/margarit/printpipe/internal/slicer"
)

type stub struct{}

func (stub) Kind() string                                               { return "stub" }
func (stub) Slice(context.Context, slicer.Input) (slicer.Result, error) { return slicer.Result{}, nil }

func TestRegistry(t *testing.T) {
	slicer.Register(slicer.Driver{Kind: "stub", New: func(slicer.Deps) slicer.Slicer { return stub{} }})
	set := slicer.Registered(slicer.Deps{})
	if s, err := set.Get("stub"); err != nil || s.Kind() != "stub" {
		t.Fatal(s, err)
	}
	if _, err := set.Get("missing"); err == nil {
		t.Fatal("missing kind found")
	}
}

func TestKindsSortedAndDuplicatePanics(t *testing.T) {
	slicer.Register(slicer.Driver{Kind: "aaa", New: func(slicer.Deps) slicer.Slicer { return stub{} }})
	if k := slicer.Kinds(); len(k) < 2 || k[0] != "aaa" {
		t.Fatalf("%v", k)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate kind must panic")
		}
	}()
	slicer.Register(slicer.Driver{Kind: "aaa", New: func(slicer.Deps) slicer.Slicer { return stub{} }})
}
