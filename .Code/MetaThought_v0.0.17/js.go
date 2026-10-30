package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"
)

// EvalJS evaluates source in a fresh runtime with only built-in JavaScript
// globals. Successful results are compact JSON, including quoted strings and
// null. Undefined and values that cannot be exported as JSON are errors.
// Caller cancellation interrupts execution; no time or work quota is imposed.
// Goja is not a memory sandbox.
func EvalJS(ctx context.Context, source string) (result string, err error) {
	if ctx == nil {
		return "", fmt.Errorf("javascript: context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("javascript: %w", err)
	}
	vm := goja.New()
	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		vm.Interrupt(ctx.Err())
	})
	defer func() {
		// Stop a callback that has not begun, and join it if it has. No
		// interrupt callback can outlive this call or affect a later runtime.
		if !stopCancel() {
			<-cancelDone
		}
		if ctx.Err() != nil {
			result, err = "", fmt.Errorf("javascript: %w", ctx.Err())
		}
	}()
	defer func() {
		// Exporting an object can invoke a JavaScript getter. Goja exposes
		// exceptions/interruptions from that path as panics rather than errors.
		if recovered := recover(); recovered != nil {
			switch failure := recovered.(type) {
			case *goja.Exception:
				result, err = "", fmt.Errorf("javascript export: %w", failure)
			case *goja.InterruptedError:
				result, err = "", fmt.Errorf("javascript export: %w", failure)
			default:
				panic(recovered)
			}
		}
	}()
	value, err := vm.RunString(source)
	if err != nil {
		return "", fmt.Errorf("javascript execution: %w", err)
	}
	if value == nil || goja.IsUndefined(value) {
		return "", fmt.Errorf("javascript result is undefined")
	}
	if goja.IsNull(value) {
		return "null", nil
	}
	encoded, err := json.Marshal(value.Export())
	if err != nil {
		return "", fmt.Errorf("javascript result is not JSON: %w", err)
	}
	return string(encoded), nil
}
