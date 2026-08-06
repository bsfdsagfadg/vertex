package transport

import (
	"fmt"

	"github.com/sagernet/sing-box"
)

// adoptForTest 仅供包内单元测试直接向 entryBoxPoolManager 注入测试实例。
func (m *entryBoxPoolManager) adoptForTest(uri string, newBox *box.Box, socksAddr string) error {
	key := normalizeURI(uri)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		if newBox != nil {
			_ = newBox.Close()
		}
		return fmt.Errorf("entry proxy pool已停止")
	}
	if m.instances == nil {
		m.instances = make(map[string]*entryBoxInstance)
	}
	if old, exists := m.instances[key]; exists {
		if old.box != nil {
			_ = old.box.Close()
		}
	}
	m.instances[key] = &entryBoxInstance{uri: uri, box: newBox, socksAddr: socksAddr}
	m.rebuildOrderLocked()
	return nil
}
