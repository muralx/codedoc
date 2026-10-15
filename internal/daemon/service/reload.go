package service

import (
	"bytes"
	"context"
	"os"
	"time"

	"github.com/muralx/codedoc/internal/config"
)

// watchConfig checks the configuration file every ConfigPoll and sends
// the reloaded configuration on out each time its content changes. A
// file that cannot be read or parsed is logged, once per content, and
// the daemon keeps the configuration it runs.
func (s *Service) watchConfig(ctx context.Context, out chan<- *config.Config) {
	last, _ := os.ReadFile(s.opts.ConfigPath)
	t := time.NewTicker(s.opts.ConfigPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		data, err := os.ReadFile(s.opts.ConfigPath)
		if err != nil || bytes.Equal(data, last) {
			continue
		}
		last = data
		cfg, err := config.Load(s.opts.ConfigPath)
		if err != nil {
			s.log.Printf("daemon: configuration %s changed but cannot be loaded, keeping the current one: %v",
				s.opts.ConfigPath, err)
			continue
		}
		select {
		case out <- cfg:
		case <-ctx.Done():
			return
		}
	}
}
