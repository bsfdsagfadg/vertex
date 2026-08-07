package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrCustomUANotFound     = errors.New("custom UA not found")
	ErrCustomUANameConflict = errors.New("custom UA name already exists")
	ErrCustomUAInUse        = errors.New("custom UA is in use")
)

type CustomUA struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UserAgent string `json:"user_agent"`
}

type Subscription struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	UserAgent      string `json:"user_agent,omitempty"`
	CustomUAID     string `json:"custom_ua_id,omitempty"`
	UpdateInterval int    `json:"update_interval"` // In minutes
	LastUpdateTime int64  `json:"last_update_time"`
	LastError      string `json:"last_error"`
	Revision       uint64 `json:"revision"`
}

type SubscriptionConfig struct {
	Subscriptions []Subscription `json:"subscriptions"`
	CustomUAs     []CustomUA     `json:"custom_uas"`
}

var (
	subMu               sync.RWMutex
	globalSubConfig     SubscriptionConfig
	subscriptionsLoaded bool
)

func subscriptionsPath() string {
	return filepath.Join(ConfigDir(), "subscriptions.json")
}

func cloneSubscriptionConfig(conf SubscriptionConfig) SubscriptionConfig {
	return SubscriptionConfig{
		Subscriptions: append([]Subscription(nil), conf.Subscriptions...),
		CustomUAs:     append([]CustomUA(nil), conf.CustomUAs...),
	}
}

func GetSubscriptionConfig() SubscriptionConfig {
	subMu.RLock()
	defer subMu.RUnlock()
	return cloneSubscriptionConfig(globalSubConfig)
}

func newConfigID(prefix string) string {
	var data [8]byte
	if _, err := rand.Read(data[:]); err == nil {
		return prefix + hex.EncodeToString(data[:])
	}
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

func normalizeSubscriptionConfig(conf *SubscriptionConfig) (bool, error) {
	changed := false
	if conf.Subscriptions == nil {
		conf.Subscriptions = []Subscription{}
		changed = true
	}
	if conf.CustomUAs == nil {
		conf.CustomUAs = []CustomUA{}
		changed = true
	}

	uaIDs := make(map[string]struct{}, len(conf.CustomUAs))
	uaNames := make([]string, 0, len(conf.CustomUAs))
	for i := range conf.CustomUAs {
		ua := &conf.CustomUAs[i]
		trimmedName := strings.TrimSpace(ua.Name)
		trimmedValue := strings.TrimSpace(ua.UserAgent)
		if trimmedName == "" || trimmedValue == "" {
			return false, fmt.Errorf("custom UA name and content are required")
		}
		if trimmedName != ua.Name || trimmedValue != ua.UserAgent {
			ua.Name = trimmedName
			ua.UserAgent = trimmedValue
			changed = true
		}
		for _, existingName := range uaNames {
			if strings.EqualFold(existingName, ua.Name) {
				return false, fmt.Errorf("%w: %s", ErrCustomUANameConflict, ua.Name)
			}
		}
		uaNames = append(uaNames, ua.Name)
		if ua.ID == "" {
			ua.ID = newConfigID("ua_")
			changed = true
		}
		if _, duplicate := uaIDs[ua.ID]; duplicate {
			return false, fmt.Errorf("duplicate custom UA ID: %s", ua.ID)
		}
		uaIDs[ua.ID] = struct{}{}
	}

	for i := range conf.Subscriptions {
		sub := &conf.Subscriptions[i]
		if sub.Revision == 0 {
			sub.Revision = 1
			changed = true
		}
		if sub.CustomUAID != "" {
			if _, ok := uaIDs[sub.CustomUAID]; !ok {
				return false, fmt.Errorf("subscription %s references unknown custom UA %s", sub.ID, sub.CustomUAID)
			}
			if sub.UserAgent != "" {
				sub.UserAgent = ""
				changed = true
			}
		}
	}
	return changed, nil
}

func loadSubscriptionsLocked() error {
	path := subscriptionsPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			globalSubConfig = SubscriptionConfig{Subscriptions: []Subscription{}, CustomUAs: []CustomUA{}}
			subscriptionsLoaded = true
			return nil
		}
		return fmt.Errorf("read subscriptions.json failed: %w", err)
	}

	var conf SubscriptionConfig
	if err := json.Unmarshal(b, &conf); err != nil {
		return fmt.Errorf("parse subscriptions.json failed: %w", err)
	}
	changed, err := normalizeSubscriptionConfig(&conf)
	if err != nil {
		return err
	}
	if changed {
		if err := writeSubscriptionConfig(conf); err != nil {
			return fmt.Errorf("migrate subscriptions.json failed: %w", err)
		}
	}
	globalSubConfig = cloneSubscriptionConfig(conf)
	subscriptionsLoaded = true
	return nil
}

func LoadSubscriptions() error {
	subMu.Lock()
	defer subMu.Unlock()
	return loadSubscriptionsLocked()
}

func ensureSubscriptionsLoadedLocked() error {
	if subscriptionsLoaded {
		return nil
	}
	return loadSubscriptionsLocked()
}

func SaveSubscriptions(conf SubscriptionConfig) error {
	subMu.Lock()
	defer subMu.Unlock()
	if _, err := normalizeSubscriptionConfig(&conf); err != nil {
		return err
	}
	if err := writeSubscriptionConfig(conf); err != nil {
		return err
	}
	globalSubConfig = cloneSubscriptionConfig(conf)
	subscriptionsLoaded = true
	return nil
}

func MutateSubscriptionConfig(mutate func(*SubscriptionConfig) error) error {
	subMu.Lock()
	defer subMu.Unlock()
	if err := ensureSubscriptionsLoadedLocked(); err != nil {
		return err
	}
	next := cloneSubscriptionConfig(globalSubConfig)
	if err := mutate(&next); err != nil {
		return err
	}
	if _, err := normalizeSubscriptionConfig(&next); err != nil {
		return err
	}
	if err := writeSubscriptionConfig(next); err != nil {
		return err
	}
	globalSubConfig = cloneSubscriptionConfig(next)
	return nil
}

func UpdateSubscription(sub Subscription) error {
	return MutateSubscriptionConfig(func(conf *SubscriptionConfig) error {
		for i, current := range conf.Subscriptions {
			if current.ID != sub.ID {
				continue
			}
			sub.LastUpdateTime = current.LastUpdateTime
			sub.LastError = current.LastError
			sub.Revision = current.Revision + 1
			conf.Subscriptions[i] = sub
			return nil
		}
		if sub.Revision == 0 {
			sub.Revision = 1
		}
		conf.Subscriptions = append(conf.Subscriptions, sub)
		return nil
	})
}

func GetSubscription(id string) (Subscription, bool) {
	subMu.RLock()
	defer subMu.RUnlock()
	for _, sub := range globalSubConfig.Subscriptions {
		if sub.ID == id {
			return sub, true
		}
	}
	return Subscription{}, false
}

func DeleteSubscription(id string) (Subscription, error) {
	var deleted Subscription
	err := MutateSubscriptionConfig(func(conf *SubscriptionConfig) error {
		kept := make([]Subscription, 0, len(conf.Subscriptions))
		found := false
		for _, sub := range conf.Subscriptions {
			if sub.ID == id {
				deleted = sub
				found = true
				continue
			}
			kept = append(kept, sub)
		}
		if !found {
			return ErrSubscriptionNotFound
		}
		conf.Subscriptions = kept
		return nil
	})
	return deleted, err
}

func UpdateSubscriptionStatus(id string, revision uint64, lastUpdate int64, lastError string) (bool, error) {
	updated := false
	err := MutateSubscriptionConfig(func(conf *SubscriptionConfig) error {
		for i := range conf.Subscriptions {
			sub := &conf.Subscriptions[i]
			if sub.ID != id {
				continue
			}
			if revision != 0 && sub.Revision != revision {
				return nil
			}
			sub.LastUpdateTime = lastUpdate
			sub.LastError = lastError
			updated = true
			return nil
		}
		return nil
	})
	return updated, err
}

func SaveCustomUA(ua CustomUA) (CustomUA, error) {
	ua.Name = strings.TrimSpace(ua.Name)
	ua.UserAgent = strings.TrimSpace(ua.UserAgent)
	if ua.Name == "" || ua.UserAgent == "" {
		return CustomUA{}, fmt.Errorf("custom UA name and content are required")
	}
	err := MutateSubscriptionConfig(func(conf *SubscriptionConfig) error {
		for _, existing := range conf.CustomUAs {
			if existing.ID != ua.ID && strings.EqualFold(existing.Name, ua.Name) {
				return fmt.Errorf("%w: %s", ErrCustomUANameConflict, ua.Name)
			}
		}
		if ua.ID == "" {
			ua.ID = newConfigID("ua_")
			conf.CustomUAs = append(conf.CustomUAs, ua)
			return nil
		}
		for i := range conf.CustomUAs {
			if conf.CustomUAs[i].ID == ua.ID {
				changed := conf.CustomUAs[i].Name != ua.Name || conf.CustomUAs[i].UserAgent != ua.UserAgent
				conf.CustomUAs[i] = ua
				if changed {
					for subIndex := range conf.Subscriptions {
						if conf.Subscriptions[subIndex].CustomUAID == ua.ID {
							conf.Subscriptions[subIndex].Revision++
						}
					}
				}
				return nil
			}
		}
		return ErrCustomUANotFound
	})
	return ua, err
}

func FindCustomUAByName(name string) (CustomUA, bool) {
	subMu.RLock()
	defer subMu.RUnlock()
	for _, ua := range globalSubConfig.CustomUAs {
		if strings.EqualFold(ua.Name, strings.TrimSpace(name)) {
			return ua, true
		}
	}
	return CustomUA{}, false
}

func GetCustomUA(id string) (CustomUA, bool) {
	subMu.RLock()
	defer subMu.RUnlock()
	for _, ua := range globalSubConfig.CustomUAs {
		if ua.ID == id {
			return ua, true
		}
	}
	return CustomUA{}, false
}

func DeleteCustomUA(id string) error {
	return MutateSubscriptionConfig(func(conf *SubscriptionConfig) error {
		for _, sub := range conf.Subscriptions {
			if sub.CustomUAID == id {
				return fmt.Errorf("%w: %s", ErrCustomUAInUse, sub.Name)
			}
		}
		kept := make([]CustomUA, 0, len(conf.CustomUAs))
		found := false
		for _, ua := range conf.CustomUAs {
			if ua.ID == id {
				found = true
				continue
			}
			kept = append(kept, ua)
		}
		if !found {
			return ErrCustomUANotFound
		}
		conf.CustomUAs = kept
		return nil
	})
}

func ResolveSubscriptionUserAgent(sub Subscription) (string, error) {
	if sub.CustomUAID == "" {
		return sub.UserAgent, nil
	}
	ua, ok := GetCustomUA(sub.CustomUAID)
	if !ok {
		return "", ErrCustomUANotFound
	}
	return ua.UserAgent, nil
}

func writeSubscriptionConfig(conf SubscriptionConfig) error {
	b, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return err
	}
	path := subscriptionsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".subscriptions-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err = temp.Write(b); err != nil {
		_ = temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tempPath, 0644); err != nil {
		return err
	}
	return replaceFile(tempPath, path)
}
