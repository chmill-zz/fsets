package fakerpc

import (
	"context"
	"errors"

	"github.com/johnsiilver/fsets"
)

type RPCRequest struct {
	User string
	Data string
}

type RPCResponse struct {
	Data string
}

type RPCServer struct {
	serveFset *fsets.Fset[serveSO]
}

//go:generate fsetcodegen -var=serveFset -func=New --inline

func New(ctx context.Context) *RPCServer {
	// Create a new Fset for the RPC server.
	serveFset := fsets.Fset[serveSO]{}

	r := &RPCServer{}

	// Add the functions to the Fset. These can be methods or functions.
	serveFset.Adds(
		fsets.C[serveSO]{F: r.handleAuth},
		fsets.C[serveSO]{F: r.handleData},
	)
	serveFset.Compiled(newServeFsetCompiled(r))

	r.serveFset = &serveFset
	return r
}

type serveSO struct {
	req  RPCRequest
	resp RPCResponse
}

func (s *RPCServer) Serve(ctx context.Context, req RPCRequest) (RPCResponse, error) {
	so := s.serveFset.Run(ctx, fsets.StateObject[serveSO]{Data: serveSO{req: req}})
	return so.Data.resp, so.Err()
}

func (s *RPCServer) handleAuth(so fsets.StateObject[serveSO]) (fsets.StateObject[serveSO], error) {
	// Handle authentication logic here.
	// For example, check if the user is authenticated.
	if so.Data.req.User == "" {
		return so, errors.New("user not authenticated")
	}
	if so.Data.req.User != "admin" {
		return so, errors.New("unauthorized user")
	}
	return so, nil
}

func (s *RPCServer) handleData(so fsets.StateObject[serveSO]) (fsets.StateObject[serveSO], error) {
	// Handle data processing logic here.
	// For example, process the data and return a response.
	if so.Data.req.Data == "" {
		return so, errors.New("no data provided")
	}
	so.Data.resp.Data = "Processed: " + so.Data.req.Data
	return so, nil
}
