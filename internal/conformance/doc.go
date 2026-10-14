// Package conformance contains the dual-oracle conformance harness for the
// protocol translation layer.
//
// The harness itself is test-only: all logic lives in *_test.go files. This
// file exists only so that `go build ./...` has a (trivial) package to compile.
//
// Workflow and vocabulary: see conformance/README.md.
// Deviation registry:      conformance/deviations.json.
// Oracle modules:          conformance/oracle-axonhub, conformance/oracle-cliproxyapi.
package conformance
