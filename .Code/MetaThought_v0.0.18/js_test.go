package main

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestEvalJSMathAndJSONResults(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{"Math.sqrt(81) + 1", "10"},
		{"({sum: [1, 2, 3].reduce((a, b) => a + b, 0)})", `{"sum":6}`},
		{`"a string"`, `"a string"`},
		{"null", "null"},
	} {
		got, err := EvalJS(context.Background(), test.source)
		if err != nil || got != test.want {
			t.Errorf("EvalJS(%q) = %q, %v; want %q", test.source, got, err, test.want)
		}
	}
}

func TestEvalJSUsesIsolatedRuntimeWithoutHostAPIs(t *testing.T) {
	if _, err := EvalJS(context.Background(), "globalThis.privateState = 42; privateState"); err != nil {
		t.Fatal(err)
	}
	got, err := EvalJS(context.Background(), "[typeof privateState, typeof require, typeof process, typeof fetch, typeof console]")
	if err != nil || got != `["undefined","undefined","undefined","undefined","undefined"]` {
		t.Fatalf("state or host API leaked: %s, %v", got, err)
	}
}

func TestEvalJSErrorsAreReturned(t *testing.T) {
	for _, source := range []string{
		"throw new Error('calculation failed')",
		"undefined",
		"NaN",
		"Infinity",
		"(function () {})",
		"var x = {}; x.self = x; x",
		"({ get x() { throw new Error('getter failed'); } })",
	} {
		result, err := EvalJS(context.Background(), source)
		if err == nil || result != "" {
			t.Errorf("expected error for %q, got %q, %v", source, result, err)
		}
	}
}

func TestEvalJSPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EvalJS(ctx, "while (true) {}"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled context: %v", err)
	}
}

func TestEvalJSCallerCanCancelInfiniteExecution(t *testing.T) {
	for _, source := range []string{"while (true) {}", "({ get x() { while (true) {} } })"} {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		finished := make(chan error)
		go func() {
			close(entered)
			result, err := EvalJS(ctx, source)
			if result != "" {
				t.Errorf("cancelled execution returned a result: %s", result)
			}
			finished <- err
		}()
		<-entered
		runtime.Gosched()
		cancel()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation: %v", err)
		}
	}
	// A previous cancellation must not affect a later, independent invocation.
	if got, err := EvalJS(context.Background(), "2 + 2"); err != nil || got != "4" {
		t.Fatalf("cancellation leaked into next call: %s, %v", got, err)
	}
}
