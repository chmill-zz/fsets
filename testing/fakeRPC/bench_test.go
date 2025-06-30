package fakerpc

import (
	"testing"

	"github.com/johnsiilver/fsets"
)

var resp RPCResponse

func BenchmarkInline(b *testing.B) {
	serv := New(b.Context())
	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		resp, _ = serv.Serve(b.Context(), RPCRequest{User: "admin", Data: "data"})
	}
}

func BenchmarkCallChain(b *testing.B) {
	serv := New(b.Context())
	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		so := fsets.StateObject[serveSO]{Data: serveSO{req: RPCRequest{User: "admin", Data: "data"}}}
		so, _ = serv.handleAuth(so)
		so, _ = serv.handleData(so)
		resp = so.Data.resp
	}
}
