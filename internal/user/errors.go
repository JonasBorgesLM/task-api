package user

import "errors"

// ErrNotFound is returned when a user or session does not exist.
var ErrNotFound = errors.New("user not found")

// ErrAlreadyExists is returned by Register when the given email is already
// registered.
var ErrAlreadyExists = errors.New("user already exists")

// ErrInvalidInput is returned when the caller provides invalid data (a
// malformed email, a too-short password, ...).
var ErrInvalidInput = errors.New("invalid input")

// ErrInvalidCredentials is returned by Authenticate for both an unknown
// email and a correct email with the wrong password — deliberately the
// same error either way, so a caller can never distinguish "no such
// account" from "wrong password" and enumerate registered emails.
var ErrInvalidCredentials = errors.New("invalid email or password")
