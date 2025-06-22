/*
Package fsets provides a function set for executing a chain of function calls that pass a state object between them.
This is the mutant child of my statemachine package without fancy routing. It provides automatic retries and the idea
is to reduce testing chains like a statemachine, but with a linear call method.

Where this is helpful is avoiding having to do a lot of testing of the entire call chain.  With this, you can test
each function independently without having to mock out the next call. This also will let you still access helper methods
and fields of a struct so you can still use methods as part of the function set.

This also contains methods to run the function set in parallel or concurrently. This is useful for using the function set
in a way that allows for parallel execution of the function calls. It leverages promises to allow for getting the results back.

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

In addition to the serial execution through Run() calls, you can also run the function set in parallel or concurrently.
These allows you to feed in a channel of promises that will be executed in parallel or concurrently with the function set.
Parallel and concurrent execution is simply a function of the number of concurrent goroutines that are running the function set.
In parallel, there is a fixed number for execution say 5 instances are running. In concurrent, there might be 5 instances running
with another len(fset.C) instances running. This mimics the behavior of channel based concurrency execution.

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
	"runtime"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/values/generics/promises"
)

// StateObject is an object passed through the call chain for a single call.
type StateObject[T any] struct {
	// Ctx is the context for this call. If a context is not provided, it will default to context.Background().
	Ctx context.Context
	// Data is any data related to this call.
	Data T

	// err is an error that can be set by any function in the call chain. If this is set, the call chain will stop.
	err  error
	stop bool
}

// Err returns and error if one occurred in the call chain.
func (s *StateObject[T]) Err() error {
	return s.err
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

	promiseDo    sync.Once
	promiseMaker *promises.Maker[StateObject[T], StateObject[T]]
}

// Adds adds calls to the Fset. This should not be called after Run().
func (f *Fset[T]) Adds(calls ...C[T]) {
	f.set = append(f.set, calls...)
}

// Run runs the Fset calls. This is thread-safe as long as all calls are thread-safe.
func (f *Fset[T]) Run(so StateObject[T]) StateObject[T] {
	var err error
	if so.Ctx == nil {
		so.Ctx = context.Background()
	}
	for _, call := range f.set {
		so, err = call.exec(so)
		if err != nil {
			so.err = err
			return so
		}
		if so.stop {
			return so
		}
	}
	return so
}

// Parallel sets up a channel to run the Fset in parallel. This will spawn n goroutines that will execute this
// in parallel and return a channel that you input promises to. Closing the channel will shut down the goroutines.
// n is the number of goroutines to run in parallel. If n < 1, it will use gomaxprocs to determine the number of goroutines to run.
// The context passed cannot be canceled. Control cancelation in the StateObject.Ctx, which works only if your functions support
// it. The returned sync.Group can be used to wait for all goroutines to finish after closing the channel, if that is desired.
// Use the Promise() method to create a new promise for the Fset to use with this channel.
func (f *Fset[T]) Parallel(ctx context.Context, n int) (chan<- promises.Promise[StateObject[T], StateObject[T]], *sync.Group) {
	return f.parallel(ctx, n)
}

// Concurrent sets up a channel to run the Fset concurrently with n numbers of parrallel goroutines that are executing
// each C in the Fset concurrently. This means there are n * len(fset) goroutines running concurrently. Have 4 C's in the Fset, and
// n == 2 will result in up to 8 different function calls running concurrently. If n < 1, it will use gomaxprocs to determine the
// number of goroutines to run. Use the Promise() method to create a new promise for the Fset to use with this channel.
func (f *Fset[T]) Concurrent(ctx context.Context, n int) (chan<- promises.Promise[StateObject[T], StateObject[T]], *sync.Group) {
	// In reality, using numParallel * numStages is the same as spawning X goroutines for stages connected with channels and
	// x parrallel goroutines for a set of stages. This avoids a lot of that overhead.
	if n < 1 {
		n = runtime.GOMAXPROCS(-1)
	}
	n = n * len(f.set)
	return f.parallel(ctx, n)
}

func (f *Fset[T]) parallel(ctx context.Context, n int) (chan<- promises.Promise[StateObject[T], StateObject[T]], *sync.Group) {
	if len(f.set) == 0 {
		panic("Fset.Concurrent called with no functions in the set")
	}

	g := context.Pool(ctx).Limited(n).Group()

	ch := make(chan promises.Promise[StateObject[T], StateObject[T]], 1)
	ctxNoCancel := context.WithoutCancel(ctx)

	// This is not in the limited pool(p) because this is the controller for the parallel execution.
	context.Pool(ctx).Submit(
		ctxNoCancel,
		func() {
			for promise := range ch {
				so := promise.In
				g.Go(
					ctxNoCancel,
					func(ctx context.Context) error {
						// Run the Fset with the provided StateObject.
						result := f.Run(so)
						promise.Set(ctxNoCancel, result, result.Err())
						return nil
					},
				)
			}
		},
	)

	return ch, &g
}

// Promise helps create a new promise for the Fset for use with the Parallel or Concurrent methods.
// This is easier and more efficient than creating a new promise manually.
func (f *Fset[T]) Promise(ctx context.Context, data T) promises.Promise[StateObject[T], StateObject[T]] {
	f.promiseDo.Do(
		func() {
			if f.promiseMaker == nil {
				f.promiseMaker = &promises.Maker[StateObject[T], StateObject[T]]{}
			}
		},
	)

	so := StateObject[T]{Ctx: ctx, Data: data}
	return f.promiseMaker.New(ctx, so)
}
