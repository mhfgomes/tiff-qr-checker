package discovery

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Candidate struct {
	Path     string
	Explicit bool
}

type Filters struct {
	Includes []string
	Excludes []string
}

type Control struct {
	mu     sync.Mutex
	cond   *sync.Cond
	paused bool
}

func NewControl() *Control {
	c := &Control{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *Control) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = true
}

func (c *Control) Resume() {
	c.mu.Lock()
	c.paused = false
	c.mu.Unlock()
	c.cond.Broadcast()
}

func (c *Control) Paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

func (c *Control) WaitIfPaused(ctx context.Context) error {
	for {
		c.mu.Lock()
		paused := c.paused
		c.mu.Unlock()
		if !paused {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func Walk(ctx context.Context, inputs []string, recursive bool, filters Filters, control *Control, emit func(Candidate) error) error {
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(input)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if matches(input, filters) {
				if err := emit(Candidate{Path: input, Explicit: true}); err != nil {
					return err
				}
			}
			continue
		}

		if recursive {
			err = filepath.WalkDir(input, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					if errors.Is(walkErr, os.ErrNotExist) {
						return nil
					}
					return walkErr
				}
				if control != nil {
					if err := control.WaitIfPaused(ctx); err != nil {
						return err
					}
				}
				if path == input {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if !matches(path, filters) {
					return nil
				}
				return emit(Candidate{Path: path, Explicit: false})
			})
			if err != nil {
				return err
			}
			continue
		}

		entries, err := os.ReadDir(input)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if control != nil {
				if err := control.WaitIfPaused(ctx); err != nil {
					return err
				}
			}
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(input, entry.Name())
			if !matches(path, filters) {
				continue
			}
			if err := emit(Candidate{Path: path, Explicit: false}); err != nil {
				return err
			}
		}
	}
	return nil
}

func matches(path string, filters Filters) bool {
	normalized := filepath.ToSlash(path)
	base := filepath.Base(path)

	if len(filters.Includes) > 0 {
		matched := false
		for _, pattern := range filters.Includes {
			if globMatch(pattern, normalized, base) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	for _, pattern := range filters.Excludes {
		if globMatch(pattern, normalized, base) {
			return false
		}
	}
	return true
}

func globMatch(pattern, normalized, base string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if ok, _ := filepath.Match(pattern, normalized); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, base); ok {
		return true
	}
	return strings.Contains(normalized, pattern)
}
