package fsets

import "testing"

type args struct {
	i int
}

func a(a args) args {
	a.i++
	return b(a)
}

func b(a args) args {
	a.i += 2
	return c(a)
}

func c(a args) args {
	a.i += 3
	return a
}

var result args

func BenchmarkCallChain(b *testing.B) {
	for b.Loop() {
		args := args{i: 0}
		result = a(args)
	}
}

func fa(so StateObject[args]) (StateObject[args], error) {
	so.Data.i++
	return so, nil
}

func fb(so StateObject[args]) (StateObject[args], error) {
	so.Data.i += 2
	return so, nil
}

func fc(so StateObject[args]) (StateObject[args], error) {
	so.Data.i += 3
	return so, nil
}

func BenchmarkFset(b *testing.B) {
	set := Fset[args]{}
	set.Adds(C[args]{F: fa}, C[args]{F: fb}, C[args]{F: fc})

	for b.Loop() {
		so := StateObject[args]{Data: args{i: 0}}
		so, _ = set.Run(so)
		result = so.Data
	}
}
