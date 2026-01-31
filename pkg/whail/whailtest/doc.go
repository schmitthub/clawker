// Package whailtest provides test doubles and helpers for testing code that
// uses the whail engine. It follows the standard library pattern (like
// net/http/httptest) of providing a testable fake alongside the real package.
//
// The core type is FakeAPIClient, a function-field based fake that implements
// the moby client.APIClient interface. Each method has a corresponding Fn field
// that can be set to control behavior. Unset methods panic with "not implemented"
// to fail loudly if unexpected calls are made.
//
// Usage:
//
//	fake := whailtest.NewFakeAPIClient()
//	engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())
//
//	// Configure specific behavior
//	fake.ContainerStopFn = func(ctx context.Context, container string, opts client.ContainerStopOptions) (client.ContainerStopResult, error) {
//	    return client.ContainerStopResult{}, nil
//	}
//
//	// Assert calls were made
//	whailtest.AssertCalled(t, fake, "ContainerStop")
package whailtest
