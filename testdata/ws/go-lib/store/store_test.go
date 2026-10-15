package store

import "testing"

func TestMemStorePutGet(t *testing.T) {
	m := NewMemStore()
	if err := m.Put("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := m.Get("k"); err != nil || v != "v" {
		t.Fatalf("got %q %v", v, err)
	}
}
