package store

import "errors"

var (
	ErrNotFound      = errors.New("store: record not found")
	ErrAlreadyExists = errors.New("store: record already exists")
	ErrInvalidInput  = errors.New("store: invalid input")
)
