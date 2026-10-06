package safe

import (
	"errors"
	"testing"
)

func TestGroupReraisesOnCaller(t *testing.T) {
	defer func() {
		p := Recovered(recover())
		if p == nil || p.Value != "boom" || len(p.Stack) == 0 {
			t.Fatalf("got %#v, want the worker's panic", p)
		}
	}()
	var g Group
	g.Go(func() {})
	g.Go(func() { panic("boom") })
	g.Wait()
}

func TestCall(t *testing.T) {
	var p *PanicError
	if err := Call(func() error { panic("boom") }); !errors.As(err, &p) {
		t.Fatalf("got %v, want *PanicError", err)
	}
	if err := Call(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
