//go:build autobahn

package main

import "testing"

// TestMain runs the Autobahn echo server. Used only when building the coverage
// test binary for the Autobahn Docker image (go test -c -tags autobahn).
func TestMain(*testing.T) {
	main()
}
