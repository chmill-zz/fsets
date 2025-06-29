package compiler

import (
	"testing"

	"github.com/johnsiilver/fsets"
)

var result Args

func BenchmarkFset(b *testing.B) {
	for b.Loop() {
		so := fsets.StateObject[Args]{Data: Args{I: 0}}
		MyFsets.Run(b.Context(), so)
		result = so.Data
	}
}

func BenchmarkCompiledFset(b *testing.B) {
	for b.Loop() {
		so := fsets.StateObject[Args]{Data: Args{I: 0}}
		so = MyFsetsCompiled(b.Context(), so)
		result = so.Data
	}
}
