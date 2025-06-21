package fsets

import (
	"errors"
	"testing"
	"time"

	"github.com/gostdlib/base/retry/exponential"
)

var backoff *exponential.Backoff

func init() {
	var err error
	backoff, err = exponential.New(
		exponential.WithPolicy(
			exponential.Policy{
				InitialInterval: 100,
				MaxInterval:     1 * time.Second,
				Multiplier:      1.1,
				MaxAttempts:     10,
			},
		),
	)
	if err != nil {
		panic(err)
	}
}

type Data struct {
	count int
}

func TestExec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		c       C[Data]
		wantErr bool
	}{
		{
			name: "Run without backoff",
			c: C[Data]{
				F: func(so StateObject[Data]) (StateObject[Data], error) {
					return so, nil
				},
			},
		},
		{
			name: "Run with backoff",
			c: C[Data]{
				F: func(so StateObject[Data]) (StateObject[Data], error) {
					if so.Data.count < 5 {
						so.Data.count++
						return so, errors.New("fail")
					}
					return so, nil
				},
				B: backoff,
			},
		},
	}

	for _, test := range tests {
		so := StateObject[Data]{Ctx: t.Context()}
		so, err := test.c.exec(so)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestExec(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestExec(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	fStandard := func(so StateObject[Data]) (StateObject[Data], error) {
		so.Data.count++
		return so, nil
	}
	fStop := func(so StateObject[Data]) (StateObject[Data], error) {
		so.Data.count++
		so.Stop()
		return so, nil
	}
	fErr := func(so StateObject[Data]) (StateObject[Data], error) {
		so.Data.count++
		return so, errors.New("error occurred")
	}

	tests := []struct {
		name      string
		c         []C[Data]
		wantCount int
		wantErr   bool
	}{
		{
			name:      "Success",
			c:         []C[Data]{{F: fStandard}, {F: fStandard}, {F: fStandard}},
			wantCount: 3,
		},
		{
			name:      "Stop",
			c:         []C[Data]{{F: fStandard}, {F: fStop}, {F: fStandard}},
			wantCount: 2,
		},
		{
			name:      "Error",
			c:         []C[Data]{{F: fStandard}, {F: fErr}, {F: fStandard}},
			wantCount: 2,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		fsets := Fset[Data]{}
		fsets.Adds(test.c...)
		so := StateObject[Data]{Ctx: t.Context(), Data: Data{}}
		so, err := fsets.Run(so)

		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestRun(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestRun(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
		}

		if so.Data.count != test.wantCount {
			t.Errorf("TestRun(%s): got count == %d, want count == %d", test.name, so.Data.count, test.wantCount)
		}
	}
}
