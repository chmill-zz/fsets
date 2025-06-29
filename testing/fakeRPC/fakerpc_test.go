package fakerpc

import (
	"testing"

	"github.com/kylelemons/godebug/pretty"
)

func TestRPCServer(t *testing.T) {
	tests := []struct {
		name    string
		req     RPCRequest
		want    RPCResponse
		wantErr bool
	}{
		{
			name: "unauthenticated",
			req: RPCRequest{
				User: "",
				Data: "test data",
			},
			wantErr: true,
		},
		{
			name: "unauthorized",
			req: RPCRequest{
				User: "guest",
				Data: "test data",
			},
			wantErr: true,
		},
		{
			name: "valid request",
			req: RPCRequest{
				User: "admin",
				Data: "test data",
			},
			want: RPCResponse{
				Data: "Processed: test data",
			},
		},
	}

	for _, test := range tests {
		server := New(t.Context())

		got, err := server.Serve(t.Context(), test.req)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestRPCServer(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestRPCServer(%s): got err == %v, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestRPCServer(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
