package store

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentOpensMigrateOnce(t *testing.T) {
	for round := 0; round < 40; round++ {
		path := filepath.Join(t.TempDir(), "ws.db")
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Go(func() {
				s, err := Open(path)
				if err == nil {
					err = s.Close()
				}
				errs[i] = err
			})
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
