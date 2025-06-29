package compiler

import (
	"github.com/Azure/retry/exponential"
	"github.com/johnsiilver/fsets"
)

type Args struct {
	I int
}

func A(a Args) Args {
	a.I++
	return B(a)
}

func B(a Args) Args {
	a.I += 2
	return C(a)
}

func C(a Args) Args {
	a.I += 3
	return a
}

func Fa(so fsets.StateObject[Args]) (fsets.StateObject[Args], error) {
	so.Data.I++
	return so, nil
}

func Fb(so fsets.StateObject[Args]) (fsets.StateObject[Args], error) {
	so.Data.I += 2
	return so, nil
}

func Fc(so fsets.StateObject[Args]) (fsets.StateObject[Args], error) {
	so.Data.I += 3
	return so, nil
}

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

type Stuff struct{}

func (s *Stuff) Fa(so fsets.StateObject[Args]) (fsets.StateObject[Args], error) {
	so.Data.I++
	return so, nil
}

func (s *Stuff) Fb(so fsets.StateObject[Args]) (fsets.StateObject[Args], error) {
	so.Data.I += 2
	return so, nil
}

func (s *Stuff) Fc(so fsets.StateObject[Args]) (fsets.StateObject[Args], error) {
	so.Data.I += 3
	return so, nil
}

//go:generate fsetcodegen -var=MyFsets2

var MyFsets2 = &fsets.Fset[Args]{}

func init() {
	MyFsets2.Adds(
		fsets.C[Args]{F: (&Stuff{}).Fa},
		fsets.C[Args]{F: (&Stuff{}).Fb},
		fsets.C[Args]{F: (&Stuff{}).Fc},
	)
}
