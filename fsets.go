/*
Package fsets provides a function set for executing a chain of function calls that pass a state object between them.
This is the mutant child of my statemachine package without fancy routing. It provides automatic retries and the idea
is to reduce testing chains like a statemachine, but with a more linear call method.
*/
package fsets

import (
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
)

// StateObject is an object passed through the call chain for a single call.
type StateObject[T any] struct {
	// Ctx is the context for this call.
	Ctx context.Context
	// Data is any data related to this call.
	Data T

	stop bool
}

// Stop sets the StateObject to stop, which will stop the call chain without erroring.
func (s *StateObject[T]) Stop() {
	s.stop = true
}
	

// F is a function to call. Con
type F[T any] func(so StateObject[T]) (StateObject[T], error)

// C is a function F and a retrier to use. 
type C[T any] struct {
	// F is the function to call.
	F F[T]
	// Backoff is retrier to use for F. This can be nil.
	Backoff *exponential.Backoff
	// RetryOptions are options for the Backoff.Retry call.
	RetryOptions []exponential.RetryOption
}

func (c C[T]) exec(so StateObject[T]) (StateObject[T], error) {
	if c.Backoff == nil {
		return c.F(so)
	}
	err := c.Backoff.Retry(
		so.Ctx, 
		func(context.Context, exponential.Record) error{
			var err error
			so, err = c.F(so)
			return err
		},
		c.RetryOptions...,
	)
	return so, err
}

// Fset is a set of functions to call. If any return an error, the calls stop.
type Fset[T any] struct {
	set []C[T]
}

// Adds adds calls to the Fset. This should not be called after Run().
func (f *Fset[T]) Adds(calls ...C[T]) {
	f.set = append(f.set, calls...)
}

// Run runs the Fset calls. This is thread-safe as long as all calls are thread-safe.
func (f *Fset[T]) Run(so StateObject[T]) error {
	var err error
	for _, call := range f.set {
		so, err = call.exec(so)
		if err != nil {
			return err
		}
		if so.stop {
			return nil
		}
	}
	return nil
}
