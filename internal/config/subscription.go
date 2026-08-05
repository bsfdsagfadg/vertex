package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type CustomUA struct {
	Name      string `json:"name"`
	UserAgent string `json:"user_agent"`
}

type Subscription struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	UserAgent      string `json:"user_agent"`
	UpdateInterval int    `json:"update_interval"` // In minutes
	LastUpdateTime int64  `json:"last_update_time"`
	LastError      string `json:"last_error"`
}

type SubscriptionConfig struct {
	Subscriptions []Subscription `json:"subscriptions"`
	CustomUAs     []CustomUA     `json:"custom_uas"`
}

var (
	subMu                 sync.RWMutex
	globalSubConfig       SubscriptionConfig
	subscriptionsLoaded   bool
)

func subscriptionsPath() string {
	return filepath.Join(ConfigDir(), "subscriptions.json")
}

func GetSubscriptionConfig() SubscriptionConfig {
	subMu.RLock()
	defer subMu.RUnlock()
	return globalSubConfig
}

func LoadSubscriptions() error {
	subMu.Lock()
	defer subMu.Unlock()

	path := subscriptionsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			globalSubConfig = SubscriptionConfig{
				Subscriptions: []Subscription{},
				CustomUAs:     []CustomUA{},
			}
			subscriptionsLoaded = true
			return nil
		}
		return fmt.Errorf("read subscriptions.json failed: %w", err)
	}

	var conf SubscriptionConfig
	if err := json.Unmarshal(b, &conf); err != nil {
		return fmt.Errorf("parse subscriptions.json failed: %w", err)
	}

	if conf.Subscriptions == nil {
		conf.Subscriptions = []Subscription{}
	}
	if conf.CustomUAs == nil {
		conf.CustomUAs = []CustomUA{}
	}

	globalSubConfig = conf
	subscriptionsLoaded = true
	return nil
}

func SaveSubscriptions(conf SubscriptionConfig) error {
	subMu.Lock()
	defer subMu.Unlock()

	b, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return err
	}

	path := subscriptionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	if err := os.WriteFile(path, b, 0644); err != nil {
		return err
	}

	globalSubConfig = conf
	return nil
}

func UpdateSubscription(sub Subscription) error {
	subMu.Lock()
	defer subMu.Unlock()

	found := false
	for i, s := range globalSubConfig.Subscriptions {
		if s.ID == sub.ID {
			globalSubConfig.Subscriptions[i] = sub
			found = true
			break
		}
	}
	if !found {
		globalSubConfig.Subscriptions = append(globalSubConfig.Subscriptions, sub)
	}

	return saveLocked()
}

func saveLocked() error {
	b, err := json.MarshalIndent(globalSubConfig, "", "  ")
	if err != nil {
		return err
	}

	path := subscriptionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, b, 0644)
}
