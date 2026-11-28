// Package fake is a fixture for the tests/identity guards. It plants one
// site of each kind the inventory guard looks for. Go never builds testdata.
package fake

import (
	"net/http"

	"google.golang.org/grpc"
)

type subscriber interface {
	Subscribe(subject, queue string, handler func([]byte) error) error
}

// IssueWorkloadToken stands for a new credential issuer.
func IssueWorkloadToken() string { return "token" }

// Serve stands for a new entry point: a gRPC server, an HTTP server and a
// bus subscription.
func Serve(bus subscriber) (*grpc.Server, *http.Server, error) {
	err := bus.Subscribe("sys.fake.subject", "", func([]byte) error { return nil })
	return grpc.NewServer(), &http.Server{}, err
}
