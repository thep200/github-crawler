package cfg

import (
	"sync"
)

var (
	loader     Loader
	loaderOnce sync.Once
)

type Loader interface {
	Load() (*Config, error)
}

func NewLoader(l Loader) (Loader, error) {
	loaderOnce.Do(func() {
		loader = l
	})
	return loader, nil
}
