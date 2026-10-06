// Package safe keeps a panic in background work from killing the whole app.
package safe

import (
	"fmt"
	"runtime/debug"
	"sync"
)

// PanicError is a recovered panic together with the stack where it happened.
type PanicError struct {
	Value any
	Stack []byte
}

func (p *PanicError) Error() string { return fmt.Sprintf("internal error: %v", p.Value) }

// Recovered wraps the value returned by recover; nil stays nil and a
// *PanicError raised by Group.Wait keeps its original stack.
func Recovered(r any) *PanicError {
	if r == nil {
		return nil
	}
	if p, ok := r.(*PanicError); ok {
		return p
	}
	return &PanicError{Value: r, Stack: debug.Stack()}
}

// Call runs fn and returns its panic, if any, as a *PanicError.
func Call(fn func() error) (err error) {
	defer func() {
		if p := Recovered(recover()); p != nil {
			err = p
		}
	}()
	return fn()
}

// Group is a WaitGroup for worker goroutines. A worker's panic is re-raised by
// Wait on the caller's goroutine, where the caller's recover can catch it;
// a panic in a bare goroutine can't be caught and ends the process.
type Group struct {
	wg    sync.WaitGroup
	once  sync.Once
	panic *PanicError
}

func (g *Group) Go(fn func()) {
	g.wg.Go(func() {
		defer func() {
			if p := Recovered(recover()); p != nil {
				g.once.Do(func() { g.panic = p })
			}
		}()
		fn()
	})
}

func (g *Group) Wait() {
	g.wg.Wait()
	if g.panic != nil {
		panic(g.panic)
	}
}
