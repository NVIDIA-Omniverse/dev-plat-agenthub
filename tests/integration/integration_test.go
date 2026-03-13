//go:build integration

// Package integration contains end-to-end tests for agenthub.
// These tests require a running Dolt SQL server and may spin up
// mock HTTP servers to simulate openclaw instances.
//
// Run with: make test-integration
package integration
