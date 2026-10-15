// Package service exposes the fixture's use cases.
package service

import (
	"os"

	"github.com/fixture/lib/store"
)

// Service answers lookups through a Store.
type Service struct{ s store.Store }

// New wires a Service to a Store.
func New(s store.Store) *Service { return &Service{s: s} }

// Lookup returns the value for key, or "" when missing.
func (svc *Service) Lookup(key string) string {
	v, err := svc.s.Get(key)
	if err != nil {
		return fallback(key)
	}
	return v
}

func fallback(key string) string { return "default:" + key }

// Mode returns the library mode from the environment.
func Mode() string { return os.Getenv("LIB_MODE") }
