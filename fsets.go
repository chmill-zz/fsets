/*
Package fsets provides a function set for executing a chain of function calls that pass a state object between them.
This is the mutant child of my statemachine package without fancy routing. It provides automatic retries and the idea
is to reduce testing chains like a statemachine, but with a linear call method.

Where this is helpful is avoiding having to do a lot of testing of the entire call chain.  With this, you can test
each function independently without having to mock out the next call. This also will let you still access helper methods
and fields of a struct so you can still use methods as part of the function set.

This is designed with a state object that is passed through the call chain that is stack allocated (though your data might not be).
This can help keep your allocations down.

Use is easy for regular functions:

	fset := fsets.Fset[MyDataType]{}
	fset.Adds(
		fsets.C[MyDataType]{F: myFunction1, B: myBackoff},
		fsets.C[MyDataType]{F: myFunction2},
	)

	so := fsets.StateObject[MyDataType]{Ctx: ctx, Data: myData}
	var err error
	so, err = fset.Run(so)
	if err != nil {
		log.Error("Error running function set", "error", err)
	}

Use with methods is also easy:

	fset := fsets.Fset[MyDataType]{}
	fset.Adds(
		fsets.C[MyDataType]{F: myStruct.myMethod1, B: myBackoff},
		fsets.C[MyDataType]{F: myStruct.myMethod2},
	)

	so := fsets.StateObject[MyDataType]{Ctx: ctx, Data: myData}
	var err error
	so, err = fset.Run(so)

You can also do this within a method of a struct:

	func (m *MyStruct) MyMethod(ctx context.Context, myData MyDataType) error {
		// Though it is usually better unless you need to do the setup dynamically to just
		// do this in the constructor to avoid the overhead of creating a new Fset every time.
		fset := fsets.Fset[MyDataType]{}
		fset.Adds(
			fsets.C[MyDataType]{F: m.myMethod1, B: myBackoff},
			fsets.C[MyDataType]{F: m.myMethod2},
		)

		so := fsets.StateObject[MyDataType]{Ctx: ctx, Data: myData}
		_, err := fset.Run(so)
		return err
	}

You will also notice that functions can have exponential backoff retries. You can set some to have this, others not and use
different backoffs for different functions. We also return the state object so that you can get return data that was stack
allocated and not heap allocated. This is useful for performance and memory usage.

There is a cost to using fsets vs standard function call chains. The runtime has to do the calling of the function calls, which slows
down the execution. In addition, I don't believe Go is smart enough to inline the function calls, so you lose that optimization as well.

This can be seen with the following benchmark:

BenchmarkCallChain-10           573722551                2.073 ns/op
BenchmarkFset-10                 8607430               141.8 ns/op

This shows that the overhead of using fsets adds about 130-150 ns. But this is overhead that is an additon and not 70x slower
execution time.  This is not a problem for most use cases that do any kind of system call (disk, network, ...). However,
if its a real tight loop using cache lines, you want to use a standard function call chain.
*/
package fsets

import (
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
)

// StateObject is an object passed through the call chain for a single call.
type StateObject[T any] struct {
	// Ctx is the context for this call. If a context is not provided, it will default to context.Background().
	Ctx context.Context
	// Data is any data related to this call.
	Data T

	stop bool
}

// Stop sets the StateObject to stop, which will stop the call chain without erroring.
func (s *StateObject[T]) Stop() {
	s.stop = true
}

// C is a function F and a retrier to use.
type C[T any] struct {
	// F is the function to call.
	F func(so StateObject[T]) (StateObject[T], error)

	// B is retrier to use for F. This can be nil.
	B *exponential.Backoff
	// O are options for the Backoff.Retry call.
	O []exponential.RetryOption
}

// exec is responsible for executing the function F with the provided StateObject.
// If a Backoff is provided, it will retry the function call using the Backoff.Retry method.
func (c C[T]) exec(so StateObject[T]) (StateObject[T], error) {
	if c.B == nil {
		return c.F(so)
	}
	err := c.B.Retry(
		so.Ctx,
		func(context.Context, exponential.Record) error {
			var err error
			so, err = c.F(so)
			return err
		},
		c.O...,
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
func (f *Fset[T]) Run(so StateObject[T]) (StateObject[T], error) {
	var err error
	if so.Ctx == nil {
		so.Ctx = context.Background()
	}
	for _, call := range f.set {
		so, err = call.exec(so)
		if err != nil {
			return so, err
		}
		if so.stop {
			return so, nil
		}
	}
	return so, nil
}
