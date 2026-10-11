package main

import "errors"

var errOperation = errors.New("repolint operation failed")

type operationError struct {
	Message string
	Cause   error
}

func (e *operationError) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return e.Message + ": " + e.Cause.Error()
}

func (e *operationError) Unwrap() []error {
	if e.Cause == nil {
		return []error{errOperation}
	}
	return []error{errOperation, e.Cause}
}

func failure(message string, cause error) error {
	return &operationError{Message: message, Cause: cause}
}
