// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package errs is the single error model of protolift. Every failure a user
// can see is an *Error with a Code (stable, for scripts and exit codes), a
// Message (what went wrong, in plain words) and a Fix (what to do next).
// Adapters translate provider errors into these codes; nothing above the
// adapters ever inspects an AWS, SSH or HTTP error directly.
package errs

import (
	"context"
	"errors"
	"fmt"
)

// Code is a stable error category. Scripts may depend on these values.
type Code string

// Canonical codes.
const (
	CodeInvalidInput Code = "INVALID_INPUT" // the user asked for something that cannot work
	CodeNotSetUp     Code = "NOT_SET_UP"    // `protolift init` has not been run here
	CodeAuthExpired  Code = "AUTH_EXPIRED"  // the AWS login is missing or expired
	CodeNotAllowed   Code = "NOT_ALLOWED"   // the AWS user lacks a permission
	CodeQuota        Code = "QUOTA"         // an AWS account limit was reached
	CodeCapacity     Code = "CAPACITY"      // AWS has no capacity for this box type now
	CodeNotFound     Code = "NOT_FOUND"     // a box or resource does not exist
	CodeConflict     Code = "CONFLICT"      // state does not allow the action (e.g. boxes still exist)
	CodePortBusy     Code = "PORT_BUSY"     // a laptop port is already in use
	CodeToolMissing  Code = "TOOL_MISSING"  // a helper program (ssh, plugin) is missing
	CodeNetwork      Code = "NETWORK"       // a connection failed
	CodeTimeout      Code = "TIMEOUT"       // something took longer than its limit
	CodeRemoteFailed Code = "REMOTE_FAILED" // a command on the box failed
	CodeCancelled    Code = "CANCELLED"     // the user pressed Ctrl-C
	CodeInternal     Code = "INTERNAL"      // a bug in protolift
)

// Error is the canonical typed error.
type Error struct {
	Code    Code
	Message string // what went wrong
	Fix     string // what the user should do; may be empty
	Cause   error  // the underlying error, kept for --debug and errors.Is
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap keeps errors.Is / errors.As working through the chain.
func (e *Error) Unwrap() error { return e.Cause }

// New builds a typed error.
func New(code Code, message, fix string) *Error {
	return &Error{Code: code, Message: message, Fix: fix}
}

// Wrap builds a typed error around a cause.
func Wrap(code Code, cause error, message, fix string) *Error {
	return &Error{Code: code, Message: message, Fix: fix, Cause: cause}
}

// CodeOf returns the code of the first *Error in the chain. Context
// cancellation and deadlines map to CANCELLED and TIMEOUT so callers never
// have to special-case them.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return CodeCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	}
	return CodeInternal
}

// As returns the *Error in the chain, converting a plain error into an
// INTERNAL (or CANCELLED / TIMEOUT) one so the CLI always has a Message.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	switch CodeOf(err) {
	case CodeCancelled:
		return Wrap(CodeCancelled, err, "stopped because you pressed Ctrl-C", "")
	case CodeTimeout:
		return Wrap(CodeTimeout, err, "this took longer than its time limit", "check your internet connection and try again")
	}
	return Wrap(CodeInternal, err, "something unexpected went wrong",
		"run again with --debug and report it at https://github.com/Prabhudas111/protolift/issues")
}

// ExitCode maps a code to a process exit status. 0 is success, 1 is a
// generic failure; the rest let scripts tell failures apart.
func ExitCode(err error) int {
	switch CodeOf(err) {
	case "":
		return 0
	case CodeInvalidInput:
		return 2
	case CodeNotSetUp:
		return 3
	case CodeAuthExpired, CodeNotAllowed:
		return 4
	case CodeQuota, CodeCapacity:
		return 5
	case CodePortBusy, CodeConflict:
		return 6
	case CodeToolMissing:
		return 7
	case CodeNetwork, CodeTimeout:
		return 8
	case CodeCancelled:
		return 130
	}
	return 1
}
