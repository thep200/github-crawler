package cfg

import (
	"fmt"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var (
	cfgIns     *Config
	cfgInsOnce sync.Once
	cfgMutex   sync.RWMutex
)

type ViperLoader struct {
	configChangeCallbacks []func(*Config)
}

func NewViperLoader() (*ViperLoader, error) {
	return &ViperLoader{
		configChangeCallbacks: make([]func(*Config), 0),
	}, nil
}

func (yl *ViperLoader) Load() (*Config, error) {
	var err error
	cfgInsOnce.Do(func() {
		err = yl.loadConfig()
		if err == nil && yl.IsWatchChange() {
			viper.WatchConfig()
			viper.OnConfigChange(func(e fsnotify.Event) {
				fmt.Printf("[INFO][CONFIG] Config file changed: %s\n", e.Name)
				if errReload := yl.reloadConfig(); errReload != nil {
					fmt.Printf("[ERROR][CONFIG] Failed to reload config: %v\n", errReload)
				}
			})
		}
	})

	if err != nil {
		return nil, err
	}

	cfgMutex.RLock()
	defer cfgMutex.RUnlock()
	return cfgIns, nil
}

func (yl *ViperLoader) IsWatchChange() bool {
	return true
}

func (yl *ViperLoader) RegisterConfigChangeCallback(callback func(*Config)) {
	cfgMutex.Lock()
	yl.configChangeCallbacks = append(yl.configChangeCallbacks, callback)
	cfgMutex.Unlock()
}

func (yl *ViperLoader) loadConfig() error {
	viper.AddConfigPath("cfg/yaml")
	viper.SetConfigName("mode")
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("[ERROR][CONFIG] failed to read config file: %w", err)
	}

	// Unmarshal tinto the config
	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return fmt.Errorf("[ERROR][CONFIG] failed to unmarshal config: %w", err)
	}

	// Assign to the global
	cfgMutex.Lock()
	cfgIns = cfg
	cfgMutex.Unlock()

	return nil
}

func (yl *ViperLoader) reloadConfig() error {
	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return fmt.Errorf("[ERROR][CONFIG] failed to unmarshal config during reload: %w", err)
	}

	// Update the global instance
	cfgMutex.Lock()
	cfgIns = cfg

	// Notify all registered callbacks
	callbacks := make([]func(*Config), len(yl.configChangeCallbacks))
	copy(callbacks, yl.configChangeCallbacks)
	cfgMutex.Unlock()
	for _, callback := range callbacks {
		go callback(cfg)
	}

	fmt.Println("[INFO][CONFIG] Configuration reloaded successfully")
	return nil
}
