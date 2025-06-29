# fsetcodegen

## Introduction

This is an experiment in compiling down an fset using a generator. It's a little awkward. It is also `vibe coded`.  Yeah, I know, but I hate doing AST work and it finally made something that would work how I needed it after a few hours of tinkering.

Simple memory bound benchmarks show a 3x improvement on running:

  BenchmarkFset-10                13589334                79.53 ns/op
  BenchmarkCompiledFset-10        61548324                19.36 ns/op

This still is multiple times longer than:

  BenchmarkCallChain-10           573722551                2.073 ns/op

I'm assuming that is due to inlining, without having done much investigation.

Basically, if you do this:

```go
//go:generate fsetcodegen -var=MyFsets

var back *exponential.Backoff

func init() {
	var err error
	back, err = exponential.New()
	if err != nil {
		panic(err)
	}
}
var MyFsets = fsets.Fset[Args]{}

func init() {
	MyFsets.Adds(fsets.C[Args]{F: Fa, B: back}, fsets.C[Args]{F: Fb}, fsets.C[Args]{F: Fc})
}
````

It will generate a file called `fset_compiled_myfsets.go`. This will contain `MyFsetsCompiled(ctx context.Context, so fsets.StateObject[Args]) fsets.StateObject[Args] ` which is a compiled version.

If you are usng methods on a single object, it will generate constructor for you to pass that object to. This doesn't support multiple objects:

```go
type Stuff struct{}

// ... methods

//go:generate fsetcodegen -var=MyFsets2

var MyFsets2 = &fsets.Fset[Args]{}

func init() {
	MyFsets2.Adds(
		fsets.C[Args]{F: (&Stuff{}).Fa},
		fsets.C[Args]{F: (&Stuff{}).Fb},
		fsets.C[Args]{F: (&Stuff{}).Fc},
	)
}
```

Will generate `fset_compiled_myfsets2.go` with `func NewMyFsets2Compiled(ctx context.Context, obj *Stuff) func(so fsets.StateObject[Args]) fsets.StateObject[Args]`.

If you try to get clever with doing things like:

```go
var MyFsets2 = (&fsets.Fset[Args]{}).Adds(fsets.C[Args]{F: (&Stuff{}).Fa})
```

This is going to break, this tool is just not that intelligent.

It also supports fsets inside functions. Using this syntax: `//go:generate fsetcodegen -var=serveFset -func=New` .

`-func=New` indicates the fset will be found inside the `New()` function.
